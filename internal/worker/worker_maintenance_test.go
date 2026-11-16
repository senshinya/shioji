package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
)

func workerMaintenanceFixture() bool {
	if len(os.Args) == 2 && os.Args[1] == "--session-detached-fixture" {
		child := exec.Command("/bin/sleep", "60")
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if child.Start() != nil {
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		bufio.NewReader(os.Stdin).ReadString('\n')
		return true
	}
	if len(os.Args) != 3 || os.Args[1] != "--worker-maintenance-fixture" {
		return false
	}
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGTERM)
	fmt.Println("ready")
	for range ch {
		f, _ := os.OpenFile(os.Args[2], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if f != nil {
			f.WriteString("T")
			f.Close()
		}
	}
	return true
}

func TestMaintenanceDirectForceClosesActiveDetachedEnvironmentWithoutUpdate(t *testing.T) {
	s, w := memoryWorker(t, maintenanceHarness{})
	selected := submitIdleFixture(t, s, w, "selected-force", "")
	other := submitIdleFixture(t, s, w, "unrelated-force", "")
	for _, d := range []domain.SessionDetail{selected, other} {
		if _, err := s.store.Apply(d.Session.ID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "live-" + d.Session.ID, Status: "running"}}); err != nil {
			t.Fatal(err)
		}
	}
	scope, err := s.executions.Existing(selected.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, stdout, err := scope.StartOwned(ctx, "detached", []string{os.Args[0], "--session-detached-fixture"}, managedexec.BootstrapEnv(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err = fmt.Fscanln(bufio.NewReader(stdout), &pid); err != nil {
		t.Fatal(err)
	}
	list, _ := managedexec.Processes()
	var child managedexec.ProcessInfo
	for _, p := range list {
		if p.Identity.PID == pid {
			child = p
		}
	}
	t.Cleanup(func() {
		managedexec.SignalExact(child, unix.SIGKILL)
		stdin.Close()
		scope.Stop(context.Background())
		cmd.Wait()
	})
	diagnosis, err := s.diagnoseWorkerUpdate()
	if err != nil {
		t.Fatal(err)
	}
	in := domain.WorkerMaintenanceInput{ID: "direct-force", ActionID: "force-once", Action: "force-close", DeviceID: s.store.DeviceID(), ExpectedBuildID: s.build.BuildID, Standalone: true}
	for _, b := range diagnosis.Blockers {
		if b.SessionID == selected.Session.ID && b.ForceTarget != nil {
			in.Targets = append(in.Targets, *b.ForceTarget)
		}
	}
	if len(in.Targets) != 1 {
		t.Fatal("active detached environment cannot be selected", diagnosis)
	}
	if _, err = s.applyWorkerMaintenance(in); err == nil {
		t.Fatal("unregistered environment accepted without risk confirmation")
	}
	in.RiskAccepted = true
	if _, err = s.store.Apply(selected.Session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "streamed-after-preview", Kind: "assistant", Text: "streamed content does not change the execution"}}); err != nil {
		t.Fatal(err)
	}
	op, err := s.applyWorkerMaintenance(in)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.applyWorkerMaintenance(in); err != nil || again.ID != op.ID {
		t.Fatal("repeated force changed operation", again, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err = s.advanceWorkerMaintenance(time.Now()); err != nil {
			t.Fatal(err)
		}
		op, _ = s.store.WorkerMaintenance(op.ID)
		if !op.Phase.Active() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.Phase != domain.WorkerMaintenanceCompleted || managedexec.ProcessState(child) == "running" {
		t.Fatal("standalone force did not complete", op)
	}
	d, _ := s.store.Session(selected.Session.ID, 0, 100)
	untouched, _ := s.store.Session(other.Session.ID, 0, 100)
	if !d.Queue.Paused || d.Session.Status != "unknown" || len(d.Items) == 0 {
		t.Fatal("force lost history or pretended completion", d)
	}
	if untouched.Session.Status != "running" {
		t.Fatal("unselected session interrupted", untouched.Session)
	}
	if s.updating.Load() {
		t.Fatal("standalone close initiated an update")
	}
}

func TestMaintenanceCanExplicitlyCloseUnassociatedProcess(t *testing.T) {
	s, _ := memoryWorker(t, maintenanceHarness{})
	cmd := exec.Command("/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	preview, err := InspectDeviceRecovery(context.Background(), s.config.Directory, s.store.DeviceID())
	if err != nil {
		t.Fatal(err)
	}
	in := domain.WorkerMaintenanceInput{ID: "unassociated", ActionID: "explicit-pid", Action: "force-close", DeviceID: s.store.DeviceID(), ExpectedBuildID: s.build.BuildID, Standalone: true, RiskAccepted: true, ProcessPreviewRef: preview.Ref}
	for _, p := range preview.Processes {
		if p.PID == cmd.Process.Pid {
			in.ProcessRefs = append(in.ProcessRefs, p.Ref)
		}
	}
	if len(in.ProcessRefs) != 1 {
		t.Fatal("unassociated process not selectable")
	}
	op, err := s.applyWorkerMaintenance(in)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.advanceWorkerMaintenance(time.Now())
		op, _ = s.store.WorkerMaintenance(op.ID)
		if !op.Phase.Active() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.Phase != domain.WorkerMaintenanceCompleted || len(op.Processes) != 1 || op.Processes[0].State != "stopped" {
		t.Fatal("explicit process close remained blocked", op)
	}
}

type maintenanceHarness struct{}

func (maintenanceHarness) Descriptor() domain.HarnessDescriptor { return (idleHarness{}).Descriptor() }
func (maintenanceHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := (idleHarness{}).Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &maintenanceRuntime{r.(*idleTestRuntime)}, nil
}

type maintenanceRuntime struct{ *idleTestRuntime }

func (*maintenanceRuntime) ManualShutdownSafe() bool { return true }

func maintenanceTool(t *testing.T, s *Server, sid string) (*exec.Cmd, string) {
	t.Helper()
	scope, err := s.executions.Existing(sid)
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Bind("executor", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ticket, err := scope.Prepare("executor", "independent-background")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "--managed-exec", ticket.ID)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(stdout)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var registration managedexec.Registration
	if err = json.Unmarshal([]byte(strings.TrimPrefix(line, managedexec.RegistrationPrefix)), &registration); err != nil {
		t.Fatal(err)
	}
	if err = scope.Register(registration); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "term-signals")
	if err = json.NewEncoder(stdin).Encode(managedexec.Acknowledgement{ID: ticket.ID, Argv: []string{os.Args[0], "--worker-maintenance-fixture", log}, Env: []string{"PATH=/usr/bin:/bin"}, PipeStdin: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var state struct{ Manual *struct{ Operation string } }
		b, _ := os.ReadFile(filepath.Join(scope.Directory, "scope.json"))
		json.Unmarshal(b, &state)
		if state.Manual != nil {
			scope.ForceManualStop(state.Manual.Operation, "fixture-cleanup")
		}
		scope.Stop(context.Background())
		stdin.Close()
		cmd.Wait()
	})
	return cmd, log
}

func maintenanceInput(t *testing.T, s *Server, id string) domain.WorkerMaintenanceInput {
	t.Helper()
	diagnosis, err := s.diagnoseWorkerUpdate()
	if err != nil {
		t.Fatal(err)
	}
	in := domain.WorkerMaintenanceInput{ID: id, ActionID: id + "-start", Action: "stop", DeviceID: s.store.DeviceID(), ExpectedBuildID: s.build.BuildID, BuildID: "candidate"}
	for _, blocker := range diagnosis.Blockers {
		if blocker.Target != nil {
			in.Targets = append(in.Targets, *blocker.Target)
		}
	}
	if len(in.Targets) == 0 {
		t.Fatal("no selectable background environment", diagnosis)
	}
	return in
}

func enableMaintenance(s *Server) {
	s.config.ReplaceWorker = func(domain.WorkerReplacement) error { return nil }
	s.config.VerifyReplacement = func(context.Context, domain.WorkerReplacement) error { return nil }
	s.build.Maintenance = true
}

func TestWorkerMaintenancePreflightAndPreviewRaceNeverStopAnything(t *testing.T) {
	s, w := memoryWorker(t, maintenanceHarness{})
	enableMaintenance(s)
	d := submitIdleFixture(t, s, w, "work", "")
	cmd, _ := maintenanceTool(t, s, d.Session.ID)
	in := maintenanceInput(t, s, "preflight")
	s.config.VerifyReplacement = func(context.Context, domain.WorkerReplacement) error {
		return errors.New("candidate signature rejected")
	}
	if _, err := s.applyWorkerMaintenance(in); err == nil {
		t.Fatal("invalid candidate admitted")
	}
	if err := cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatal("preflight stopped process", err)
	}
	s.config.VerifyReplacement = func(context.Context, domain.WorkerReplacement) error { return nil }
	if _, err := s.store.Enqueue(domain.Submission{OperationID: "queue-race", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "keep queued"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyWorkerMaintenance(in); err == nil {
		t.Fatal("stale queue admitted")
	}
	scope, _ := s.executions.Existing(d.Session.ID)
	if !scope.Admitting() {
		t.Fatal("rejected selection fenced runtime")
	}
	q, _ := s.store.Queue(d.Session.ID)
	if q.Paused {
		t.Fatal("rejected selection paused queue")
	}
	in = maintenanceInput(t, s, "preflight")
	accepted, err := s.applyWorkerMaintenance(in)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Phase != domain.WorkerMaintenanceStopping || scope.ForceAllowed() {
		t.Fatal("normal acceptance granted force", accepted)
	}
	again, err := s.applyWorkerMaintenance(in)
	if err != nil || again.ID != accepted.ID {
		t.Fatal("lost receipt was not queryable", again, err)
	}
	if _, err = s.submit(domain.Submission{OperationID: "must-not-accept", SessionID: d.Session.ID, WorkspaceID: w.ID, HarnessID: "idle", Text: "new input", Config: d.Session.Config}); err == nil {
		t.Fatal("cleanup admitted selected session input")
	}
}

func TestWorkerMaintenanceForceWaitsForOtherSessionsAndCancelRevokesUpdate(t *testing.T) {
	s, w := memoryWorker(t, maintenanceHarness{}, memoryHarness{id: "busy", hold: true})
	enableMaintenance(s)
	var replaced atomic.Bool
	s.config.ReplaceWorker = func(domain.WorkerReplacement) error { replaced.Store(true); return nil }
	d := submitIdleFixture(t, s, w, "selected", "")
	maintenanceTool(t, s, d.Session.ID)
	other, err := s.submit(domain.Submission{OperationID: "other", WorkspaceID: w.ID, HarnessID: "busy", Text: "continue independently", Config: domain.RunConfig{Model: "busy-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, other.ID)
	in := maintenanceInput(t, s, "operation")
	op, err := s.applyWorkerMaintenance(in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.advanceWorkerMaintenance(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	op, _ = s.store.WorkerMaintenance(op.ID)
	if op.Phase != domain.WorkerMaintenanceNeedsForce {
		t.Fatal("missing separate force decision", op)
	}
	force := in
	force.Action = "force"
	force.ActionID = "force-once"
	force.Revision = op.Revision
	force.Targets = nil
	for _, target := range op.Targets {
		ref := target.Target
		ref.ProcessRef = target.ProcessRef
		force.Targets = append(force.Targets, ref)
	}
	if _, err = s.applyWorkerMaintenance(force); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.advanceWorkerMaintenance(time.Now())
		op, _ = s.store.WorkerMaintenance(op.ID)
		if op.Phase == domain.WorkerMaintenanceWaiting {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if op.Phase != domain.WorkerMaintenanceWaiting || replaced.Load() {
		t.Fatal("updated while another session was running", op, replaced.Load())
	}
	otherDetail, _ := s.store.Session(other.SessionID, 0, 1)
	if otherDetail.Session.Status != "running" {
		t.Fatal("cleanup interrupted unrelated session")
	}
	if _, err = s.cancelWorkerMaintenance(domain.WorkerMaintenanceInput{ID: op.ID, ActionID: "cancel-once", DeviceID: s.store.DeviceID(), Revision: op.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Apply(other.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn/other", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.advanceWorkerMaintenance(time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if result, err := s.prepareWorkerUpdate(domain.WorkerReplacement{ExpectedBuildID: s.build.BuildID, BuildID: "candidate"}); err != nil || result.Ready {
		t.Fatal("automatic retry ignored cancellation", result, err)
	}
	if replaced.Load() {
		t.Fatal("cancelled update executed")
	}
}

func TestWorkerMaintenanceRestartObservesAndNeverRepeatsUnknownStop(t *testing.T) {
	s, w := memoryWorker(t, maintenanceHarness{})
	enableMaintenance(s)
	d := submitIdleFixture(t, s, w, "selected", "")
	cmd, log := maintenanceTool(t, s, d.Session.ID)
	in := maintenanceInput(t, s, "restart")
	op, err := s.applyWorkerMaintenance(in)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(log)
		if len(b) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	before, _ := os.ReadFile(log)
	if len(before) != 1 {
		t.Fatal("normal stop signal not observed", string(before))
	}
	s.Close()
	config := s.config
	config.Harnesses = []harness.Adapter{maintenanceHarness{}}
	restarted, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err = cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatal("restart killed unforced process", err)
	}
	after, _ := os.ReadFile(log)
	if string(after) != string(before) {
		t.Fatal("restart repeated unknown stop", string(before), string(after))
	}
	got, err := restarted.store.WorkerMaintenance(op.ID)
	if err != nil || got.Phase != domain.WorkerMaintenanceNeedsForce {
		t.Fatal("recovery did not restore explicit decision", got, err)
	}
	scope, _ := restarted.executions.Existing(d.Session.ID)
	if scope.ForceAllowed() {
		t.Fatal("restart granted force")
	}
	if _, err = restarted.cancelWorkerMaintenance(domain.WorkerMaintenanceInput{ID: got.ID, ActionID: "cancel-after-restart", DeviceID: restarted.store.DeviceID(), Revision: got.Revision}); err != nil {
		t.Fatal(err)
	}
	if err = restarted.sessionMaintenanceAdmission(d.Session.ID); err == nil {
		t.Fatal("cancelled live generation admitted new execution")
	}
}

func TestWorkerMaintenancePreparedSelectionRecoveryNeverSignals(t *testing.T) {
	s, w := memoryWorker(t, maintenanceHarness{})
	enableMaintenance(s)
	d := submitIdleFixture(t, s, w, "prepared", "")
	// Another member of the preview has not yet acquired its per-generation
	// fence when the worker dies. No DB operation has been accepted.
	submitIdleFixture(t, s, w, "not-yet-prepared", "")
	cmd, log := maintenanceTool(t, s, d.Session.ID)
	in := maintenanceInput(t, s, "before-db-commit")
	scope, _ := s.executions.Existing(d.Session.ID)
	if _, err := scope.BeginManualStop(in.ID, in.Targets[0].ProcessRef); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if signals, _ := os.ReadFile(log); len(signals) != 0 {
		t.Fatal("unaccepted guard sent a shutdown signal", string(signals))
	}
	restarted, err := New(context.Background(), s.config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err = cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatal("recovery killed prepared scope", err)
	}
	if err = restarted.sessionMaintenanceAdmission(d.Session.ID); err == nil {
		t.Fatal("orphan generation reopened execution")
	}
	fresh := maintenanceInput(t, restarted, "fresh-after-orphan")
	if _, err = restarted.applyWorkerMaintenance(fresh); err != nil {
		t.Fatal("orphan guard permanently fenced fresh explicit maintenance", err)
	}
}

func TestArchiveDeletionCannotBypassUnforcedMaintenance(t *testing.T) {
	s, w := memoryWorker(t, maintenanceHarness{})
	enableMaintenance(s)
	d := submitIdleFixture(t, s, w, "archived-maintenance", "")
	archived, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	cmd, log := maintenanceTool(t, s, d.Session.ID)
	if _, err = s.applyWorkerMaintenance(maintenanceInput(t, s, "archive-maintenance")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(log); string(b) == "T" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	before, _ := os.ReadFile(log)
	if string(before) != "T" {
		t.Fatal("normal shutdown was not observed", string(before))
	}
	input, _ := json.Marshal(protocol.ArchiveDeleteInput{SessionID: archived.ID, Revision: archived.Revision})
	if _, err = s.handle("session.delete", input); err == nil {
		t.Fatal("permanent deletion bypassed a surviving no-force generation")
	}
	if err = s.cleanupArchives(archived.ArchivedAt.Add(31 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Session(archived.ID, 0, 1); err != nil {
		t.Fatal("retention erased protected session", err)
	}
	scope, err := s.executions.Existing(archived.ID)
	if err != nil || scope == nil || scope.ForceAllowed() {
		t.Fatal("archive cleanup changed force authorization", err)
	}
	if err = cmd.Process.Signal(unix.Signal(0)); err != nil {
		t.Fatal("archive cleanup killed unforced work", err)
	}
	after, _ := os.ReadFile(log)
	if string(after) != string(before) {
		t.Fatal("archive cleanup repeated the normal shutdown", string(after))
	}
}
