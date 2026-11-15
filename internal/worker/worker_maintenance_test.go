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
)

func workerMaintenanceFixture() bool {
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
