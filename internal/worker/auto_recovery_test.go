package worker

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/testutil"
	"syscall"
	"testing"
	"time"
)

func TestNewConnectionChecksAgainAfterOldProcessExits(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-reconnect-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, _ := db.CreateWorkspace("Reconnect", []string{folder})
	op, _ := db.Accept(domain.Submission{HarnessID: "codex", OperationID: "lost", WorkspaceID: w.ID, Text: "do not replay"})
	if err = db.SetOperation(op.ID, domain.OperationDispatched, ""); err != nil {
		t.Fatal(err)
	}
	db.Close()
	old := exec.Command("/bin/sleep", "60")
	old.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = old.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { old.Process.Kill(); old.Wait() }()
	if err = recordProcess(dir, old.Process.Pid); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go s.Serve()
	until := time.Now().Add(10 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 1)
		if d.Session.Recovery != nil && d.Session.Recovery.NeedsStop {
			break
		}
		if time.Now().After(until) {
			t.Fatal("initial automatic check did not discover previous owner")
		}
		time.Sleep(10 * time.Millisecond)
	}
	old.Process.Kill()
	old.Wait()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var snapshot domain.Snapshot
	if err = c.Call(context.Background(), "snapshot", nil, &snapshot); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(5 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 1)
		info := d.Session.Recovery
		if d.Session.Status == domain.ExecutionFailed && (info == nil || !info.CanEnd) {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("reconnection did not automatically retire the old input: %+v", d.Session)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(p.Inputs()) != 0 {
		t.Fatal("reconnection replayed old user input")
	}
	receipt, err := s.store.Operation(op.ID)
	if err != nil || receipt.State != domain.OperationAbandoned {
		t.Fatal("unconfirmed input did not retain a terminal receipt", receipt, err)
	}
	// Repeated snapshots on one connection do not restart the same check.
	before, _ := s.store.Session(op.SessionID, 0, 1)
	for i := 0; i < 20; i++ {
		if err = c.Call(context.Background(), "snapshot", nil, &snapshot); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(30 * time.Millisecond)
	after, _ := s.store.Session(op.SessionID, 0, 1)
	if after.Session.Revision != before.Session.Revision {
		t.Fatal("snapshot polling repeatedly rewrote recovery progress")
	}
}

func TestExitedIdleRuntimeIsRestartedWithoutSendingAnyInput(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-idle-restart-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := s.currentRuntime()
	old.Close(context.Background())
	until := time.Now().Add(5 * time.Second)
	for {
		r := s.currentRuntime()
		if r != nil && r != old && r.Readiness().State == "ready" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("idle runtime exit left worker permanently unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(p.Inputs()) != 0 {
		t.Fatal("runtime restart sent a model prompt")
	}
}

type restartContinuationHarness struct {
	idleHarness
	hold      bool
	submitted chan string
}

func (a restartContinuationHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	runtime, err := a.idleHarness.Start(ctx, o)
	if err != nil {
		return nil, err
	}
	r := runtime.(*idleTestRuntime)
	r.adapter.hold = a.hold
	return &restartContinuationRuntime{idleTestRuntime: r, submitted: a.submitted}, nil
}

type restartContinuationRuntime struct {
	*idleTestRuntime
	submitted chan string
}

func (r *restartContinuationRuntime) Observes(s domain.Session) bool {
	return r.options.Scope != nil && r.options.Scope.Session == s.ID && !r.Stopped()
}

func (r *restartContinuationRuntime) Submit(ctx context.Context, s domain.Session, op string, input harness.TurnInput) (string, error) {
	r.submitted <- input.Text
	return r.memoryRuntime.Submit(ctx, s, op, input)
}

func (r *restartContinuationRuntime) Inspect(context.Context, domain.Session) (domain.RecoveryHistory, error) {
	return domain.RecoveryHistory{
		Available: true, State: domain.SourceIdle,
		Turns:  []domain.Turn{{ID: "turn/original", Status: domain.ExecutionInterrupted}},
		Inputs: map[string]string{"input:original": "turn/original"},
	}, nil
}

func TestWorkerRestartContinuesAnInterruptedTurnOnce(t *testing.T) {
	a := restartContinuationHarness{hold: true, submitted: make(chan string, 8)}
	s, w := memoryWorker(t, a)
	op, err := s.submit(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "original request", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if err != nil || awaitReceipt(t, s, op.ID).State != domain.OperationConfirmed {
		t.Fatal("original request did not start", op, err)
	}
	before, err := s.store.SessionRecord(op.SessionID)
	if err != nil || before.Status != domain.ExecutionRunning || <-a.submitted != "original request" {
		t.Fatal("original request is not running", before.Status, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	a.hold = false
	continuationID := domain.RecoveryContinuationID(op.SessionID, "turn/original")
	for restart := 0; restart < 2; restart++ {
		recovered, err := New(context.Background(), Config{Directory: s.Directory(), StartPaused: true, Harnesses: []harness.Adapter{a}})
		if err != nil {
			t.Fatal(err)
		}
		defer recovered.Close()
		recovered.dispatchMu.Lock()
		recovered.pressureSample = func() uint32 { return 1 }
		recovered.updating.Store(false)
		recovered.dispatchMu.Unlock()
		recovered.wakeRecovery()
		if receipt := awaitReceipt(t, recovered, continuationID); receipt.State != domain.OperationConfirmed {
			t.Fatal("continuation did not settle", receipt)
		}
		if restart == 0 {
			select {
			case text := <-a.submitted:
				if text != "continue" {
					t.Fatal("original request was replayed", text)
				}
			default:
				t.Fatal("continuation was not handed to the Agent")
			}
		}
		recovered.recoverSession(op.SessionID)
		after, err := recovered.store.Session(op.SessionID, 0, 100)
		if err != nil || after.Session.ThreadID != before.ThreadID || after.Session.Status != domain.ExecutionCompleted || !after.Queue.Paused || len(after.Turns) != 2 {
			t.Fatal("restart changed conversation identity, queue or turn history", after, err)
		}
		if err = recovered.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.submitted) != 0 {
		t.Fatal("restarting dispatched more than one continuation")
	}
}
