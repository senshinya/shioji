package worker

import (
	"bufio"
	"context"

	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/transport"
	"testing"
	"time"
)

func recoveryReviewDirectory(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sh-rec-review-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return dir, db
}

func TestRecoveryReservationSurvivesStartupHandoff(t *testing.T) {
	dir, db := recoveryReviewDirectory(t)
	id := db.DeviceID()
	op := domain.WorkerRecoveryOperation{ID: "original", DeviceID: id, Phase: "starting", UpdatedAt: time.Now()}
	if err := recoveryWrite(dir, "original", deviceRecoveryRecord{Operation: op}); err != nil {
		t.Fatal(err)
	}
	if err := recoveryWrite(dir, "active", transport.RecoveryFence{ID: op.ID, DeviceID: id}); err != nil {
		t.Fatal(err)
	}
	preview, err := InspectDeviceRecovery(context.Background(), dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Operation == nil || preview.Operation.ID != op.ID {
		t.Fatal("startup handoff lost active recovery reservation", preview.Operation)
	}
	conflict, err := StartDeviceRecovery(context.Background(), dir, "", domain.WorkerRecoveryInput{ID: "second", ActionID: "second-action", Action: "restart", DeviceID: id, RiskAccepted: true, PreviewRef: preview.Ref})
	if err != nil || conflict.ID != op.ID || !conflict.Conflict {
		t.Fatal("accepted competing recovery during verification", conflict, err)
	}
}
func TestDeviceRecoveryConsentIgnoresContentButRejectsNewWork(t *testing.T) {
	dir, db := recoveryReviewDirectory(t)
	folder := t.TempDir()
	w, err := db.CreateWorkspace("review", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "old", WorkspaceID: w.ID, Text: "old task"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := InspectDeviceRecovery(context.Background(), dir, db.DeviceID())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot recoveryPreviewRecord
	if err = recoveryRead(dir, "preview-"+preview.Ref, &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "output", Kind: "assistant", Text: "stream"}}); err != nil {
		t.Fatal(err)
	}
	if err = validateRecoveryConsent(context.Background(), dir, snapshot); err != nil {
		t.Fatal("content drift invalidated whole-device consent", err)
	}
	if _, err = db.Accept(domain.Submission{OperationID: "new-session", WorkspaceID: w.ID, Text: "new task"}); err != nil {
		t.Fatal(err)
	}
	if err = validateRecoveryConsent(context.Background(), dir, snapshot); err == nil {
		t.Fatal("new task inherited old device consent")
	}
}
func TestExactUnknownProcessDoesNotAuthorizeDescendants(t *testing.T) {
	root := exec.Command("/bin/sh", "-c", "sleep 60 & echo $!; wait")
	stdout, _ := root.StdoutPipe()
	if err := root.Start(); err != nil {
		t.Fatal(err)
	}
	var child int
	if _, err := fmt.Fscanln(bufio.NewReader(stdout), &child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Kill(child, unix.SIGKILL); root.Process.Kill(); root.Wait() })
	list, err := managedexec.Processes()
	if err != nil {
		t.Fatal(err)
	}
	var exact managedexec.ProcessInfo
	for _, p := range list {
		if p.Identity.PID == root.Process.Pid {
			exact = p
		}
	}
	for _, evidence := range []string{"unknown", "worker"} {
		t.Run(evidence, func(t *testing.T) {
			exact.Evidence = evidence
			summary := recoverySummary(exact)
			r := deviceRecoveryRecord{Snapshot: recoveryPreviewRecord{Targets: map[string]managedexec.ProcessInfo{summary.Ref: exact}}, Operation: domain.WorkerRecoveryOperation{Results: []domain.WorkerRecoveryResult{{Process: summary}}}}
			if err := expandRecoveryDescendants(&r); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, p := range r.Operation.Results {
				found = found || p.Process.PID == child
			}
			if found != (evidence != "unknown") {
				t.Fatal("individual PID consent changed into an environment", r.Operation.Results)
			}
		})
	}
}

type staleRecoveryHarness struct{ memoryHarness }
type staleRecoveryRuntime struct{ *memoryRuntime }

func (a staleRecoveryHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := a.memoryHarness.Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &staleRecoveryRuntime{r.(*memoryRuntime)}, nil
}
func (r *staleRecoveryRuntime) Observes(domain.Session) bool { return false }
func (r *staleRecoveryRuntime) Inspect(context.Context, domain.Session) (domain.RecoveryHistory, error) {
	return domain.RecoveryHistory{Available: true, State: domain.SourceActive, Turns: []domain.Turn{{ID: "stale-source-turn", Status: "running"}}}, nil
}
func TestDeviceStopProofKeepsExplicitEndAvailableForStaleSource(t *testing.T) {
	s, w := memoryWorker(t, staleRecoveryHarness{memoryHarness{id: "alpha"}})
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	session := savedUnknownSession(t, s, w)
	_, err := s.executions.Begin(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	consent, e := captureRecoveryConsent(context.Background(), s.Directory())
	if e != nil {
		t.Fatal(e)
	}
	if err = confirmRecoveryScopes(s.Directory(), "device-recovery", recoveryPreviewRecord{Consent: consent}); err != nil {
		t.Fatal(err)
	}
	result, err := s.reconcileLocked(context.Background(), protocol.RecoveryInput{SessionID: session.ID})
	if err != nil || !result.CanEnd || result.Status != "unknown" {
		t.Fatal("device stop lost explicit local end", result, err)
	}
	d, _ := s.store.Session(session.ID, 0, 100)
	history := map[string]string{}
	for _, item := range d.Items {
		history[item.ID] = item.Text
	}
	result, err = s.reconcileLocked(context.Background(), protocol.RecoveryInput{SessionID: session.ID, Revision: d.Session.Revision, Action: "end"})
	if err != nil || result.Status == "active" || result.Status == "unknown" {
		t.Fatal("explicit end remained blocked", result, err)
	}
	d, _ = s.store.Session(session.ID, 0, 100)
	for id, text := range history {
		found := false
		for _, item := range d.Items {
			found = found || item.ID == id && item.Text == text
		}
		if !found {
			t.Fatal("ending old record lost saved message", id)
		}
	}
}

func TestRecoveryNeverSuspendsAReplacedServiceOwner(t *testing.T) {
	child := exec.Command("/bin/sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	list, _ := managedexec.Processes()
	var expected managedexec.ProcessInfo
	for _, p := range list {
		if p.Identity.PID == child.Process.Pid {
			expected = p
		}
	}
	calls := []string{}
	run := func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args[0])
		return []byte(fmt.Sprintf("pid = %d\npath = /isolated/execution.plist\n", expected.Identity.PID+1)), nil
	}
	_, err := suspendRecoveryService(context.Background(), "gui/test/execution", expected, run)
	if err == nil {
		t.Fatal("changed service owner accepted")
	}
	for _, call := range calls {
		if call == "bootout" {
			t.Fatal("replacement received service termination")
		}
	}
	child.Process.Kill()
	child.Wait()
	calls = nil
	_, err = suspendRecoveryService(context.Background(), "gui/test/execution", expected, run)
	if err == nil || len(calls) != 0 {
		t.Fatal("exited owner still controlled a service label", calls, err)
	}
}
func TestRecoveryRetryRetainsOriginalServiceAndBuild(t *testing.T) {
	previous := deviceRecoveryRecord{Operation: domain.WorkerRecoveryOperation{Phase: "partial"}, Service: "gui/test/original", ServicePlist: "/original/service.plist", ServiceSuspended: true, SourceBinary: "/original/worker", SourceDigest: "original-build"}
	next := deviceRecoveryRecord{SourceBinary: "/new/helper", SourceDigest: "new-build"}
	inheritRecoveryRestoration(previous, &next)
	if next.SourceBinary != previous.SourceBinary || next.SourceDigest != previous.SourceDigest || next.Service != previous.Service || !next.ServiceSuspended || next.ServicePlist != previous.ServicePlist {
		t.Fatal("retry silently upgraded or lost registration", next)
	}
	if err := verifyRecoveryBuild(next, domain.WorkerBuild{BuildID: "new-build"}); err == nil {
		t.Fatal("standalone accepted a different build")
	}
	if err := verifyRecoveryBuild(next, domain.WorkerBuild{BuildID: "original-build"}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRecoveryServiceRejectsReplacementInstance(t *testing.T) {
	if os.Getenv("SHIOJI_RECOVERY_LAUNCHD_TEST") == "" {
		t.Skip("isolated per-user launchd fixture")
	}
	label := fmt.Sprintf("dev.shioji.recovery.fixture.%d", time.Now().UnixNano())
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	plist := filepath.Join(t.TempDir(), label+".plist")
	body := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>/bin/sleep</string><string>60</string></array><key>RunAtLoad</key><true/></dict></plist>`, label)
	if err := os.WriteFile(plist, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	domainTarget := fmt.Sprintf("gui/%d", os.Getuid())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := nativeRecoveryServiceCommand(ctx, "bootstrap", domainTarget, plist); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nativeRecoveryServiceCommand(context.Background(), "bootout", target) })
	fetch := func() managedexec.ProcessInfo {
		t.Helper()
		for until := time.Now().Add(2 * time.Second); time.Now().Before(until); {
			body, err := nativeRecoveryServiceCommand(ctx, "print", target)
			if err != nil {
				t.Fatal(err)
			}
			pid, _ := serviceObservation(body)
			list, _ := managedexec.Processes()
			for _, p := range list {
				if p.Identity.PID == pid {
					return p
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("fixture owner absent")
		return managedexec.ProcessInfo{}
	}
	original := fetch()
	if _, err := nativeRecoveryServiceCommand(ctx, "kickstart", "-k", target); err != nil {
		t.Fatal(err)
	}
	replacement := fetch()
	if replacement.Identity == original.Identity {
		t.Fatal("fixture did not replace owner")
	}
	if _, err := suspendRecoveryService(ctx, target, original, nativeRecoveryServiceCommand); err == nil {
		t.Fatal("old preview controlled replacement")
	}
	if managedexec.ProcessState(replacement) != "running" {
		t.Fatal("replacement terminated outside consent")
	}
	if _, err := suspendRecoveryService(ctx, target, replacement, nativeRecoveryServiceCommand); err != nil {
		t.Fatal("fresh registration binding rejected", err)
	}
}

func TestDeviceRecoverySupersedesPendingAutomaticUpdate(t *testing.T) {
	dir, db := recoveryReviewDirectory(t)
	w, err := db.CreateWorkspace("review", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := db.Accept(domain.Submission{OperationID: "old-input", WorkspaceID: w.ID, Text: "done"})
	if err != nil {
		t.Fatal(err)
	}
	db.SetOperation(accepted.ID, "confirmed", "")
	db.Apply(accepted.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "old-turn", Status: "completed"}})
	state, _ := db.MaintenanceSessionState(accepted.SessionID)
	op := domain.WorkerMaintenance{ID: "old-update", DeviceID: db.DeviceID(), ExpectedBuildID: "old", BuildID: "candidate", Targets: []domain.WorkerMaintenanceResult{{Target: domain.WorkerMaintenanceTarget{SessionID: accepted.SessionID, Generation: "old-generation", ProcessRef: "p", Revision: state.Session.Revision, QueueRevision: state.Queue.Revision}}}}
	if _, err = db.AcceptWorkerMaintenance(op); err != nil {
		t.Fatal(err)
	}
	id := db.DeviceID()
	db.Close()
	if err = pauseRecoveryDatabase(context.Background(), dir, id); err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	result, err := again.WorkerMaintenance(op.ID)
	if err != nil || result.Phase.Active() {
		t.Fatal("standalone recovery left an old automatic upgrade armed", result, err)
	}
}
