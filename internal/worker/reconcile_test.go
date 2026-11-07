package worker_test

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/worker"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A real app-server persisted a completed turn, but the worker did not record
// its last events. Checking history must repair the existing input, never resend it.
func TestReconcileRepairsHistoryWithoutReplayingInputOrQueue(t *testing.T) {
	server, c, p := startServer(t)
	dir := server.Directory()
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Recovery", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "recover-input", WorkspaceID: w.ID, Text: "hello", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	beforeCalls := len(p.Inputs())
	sourceTurn := d.Turns[0]
	server.Close()
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Enqueue(domain.Submission{OperationID: "waiting", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "do not replay", Config: d.Session.Config}); err != nil {
		t.Fatal(err)
	}
	sourceTurn.Status = "unknown"
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &sourceTurn}); err != nil {
		t.Fatal(err)
	}
	if err = db.SetOperation(op.ID, "indeterminate", "lost receipt"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	recovered, err := worker.New(context.Background(), worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	go recovered.Serve()
	client := connect(t, dir)
	// Startup recovery runs without a GUI or an explicit reconcile command.
	wait(t, func() bool {
		call(t, client, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	call(t, client, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
	var result map[string]any
	call(t, client, "session.reconcile", map[string]any{"sessionId": op.SessionID, "revision": d.Session.Revision}, &result)
	call(t, client, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
	if d.Session.Status != "completed" {
		t.Fatalf("source completion not restored: %+v", d.Session)
	}
	if len(d.Queue.Items) != 1 || !d.Queue.Paused {
		t.Fatalf("recovery changed paused queue: %+v", d.Queue)
	}
	count := 0
	for _, i := range d.Items {
		if i.Kind == "user" {
			count++
			if i.ID != "input:recover-input" || i.TurnID != sourceTurn.ID || i.Status != "sent" {
				t.Fatalf("input not reconciled: %+v", i)
			}
		}
	}
	if count != 1 || len(p.Inputs()) != beforeCalls {
		t.Fatal("reconciliation replayed input or duplicated history")
	}
	call(t, client, "operation", map[string]string{"id": op.ID}, &op)
	if op.State != "confirmed" {
		t.Fatalf("matching client marker did not settle operation: %+v", op)
	}
	// A second client cannot use an old revision to end a reconciled session.
	if err = client.Call(context.Background(), "session.reconcile", map[string]any{"sessionId": op.SessionID, "revision": int64(1), "action": "end"}, &result); err == nil {
		t.Fatal("stale recovery action accepted")
	}
}

func TestReconcileRestartsExitedRuntimeAndKeepsInterruptedInputOutOfDispatch(t *testing.T) {
	server, c, p := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Crash", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "crash", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not start")
	}
	var record struct{ PID int }
	b, err := os.ReadFile(filepath.Join(server.Directory(), "runtimes", op.SessionID, "runtime-process.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(record.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "interrupted"
	})
	var result protocol.RecoveryResult
	result = reconcileCurrent(t, c, op.SessionID, "")
	if result.Status != "interrupted" || !result.CanContinue {
		t.Fatalf("managed execution was not recovered: %+v", result)
	}
	call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
	if !d.Queue.Paused || len(p.Inputs()) != 1 {
		t.Fatal("crash recovery replays or resumes automatically")
	}
}

func TestAppServerCrashDoesNotUnlockCheckoutWhileDetachedToolStillRuns(t *testing.T) {
	server, c, p := startServer(t)
	folder := t.TempDir()
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Detached tool", Folders: []string{folder}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "detached-tool", WorkspaceID: w.ID, Text: "SHIOJI_RECOVERY_EXEC", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "auto-approve"}}, &op)
	var toolPID int
	wait(t, func() bool {
		b, err := os.ReadFile(filepath.Join(folder, ".shioji-recovery-tool-pid"))
		if err != nil {
			return false
		}
		toolPID, _ = strconv.Atoi(string(b))
		return toolPID > 0
	})
	group, err := syscall.Getpgid(toolPID)
	if err != nil {
		t.Fatal(err)
	}
	if group != toolPID {
		t.Fatal("test tool did not create its own process group")
	}
	t.Cleanup(func() { syscall.Kill(-group, syscall.SIGTERM) })
	var record struct{ PID int }
	b, err := os.ReadFile(filepath.Join(server.Directory(), "runtimes", op.SessionID, "runtime-process.json"))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(b, &record)
	if err = syscall.Kill(record.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "interrupted"
	})
	result := reconcileCurrent(t, c, op.SessionID, "check")
	if result.Status != "interrupted" || !result.CanContinue {
		t.Fatal("managed group did not recover", result)
	}
	proc, e := unix.SysctlKinfoProc("kern.proc.pid", toolPID)
	if e == nil && proc.Proc.P_stat != 5 {
		t.Fatal("checkout was released before managed tool stopped")
	}
	if len(p.Inputs()) != 1 {
		t.Fatal("crash replayed input")
	}

}

// Lifecycle events may advance the revision while the click is in transit. Only
// retry an explicitly stale check; an uncertain network send is never retried.
func reconcileCurrent(t *testing.T, c *protocol.Client, id, action string) protocol.RecoveryResult {
	t.Helper()
	var result protocol.RecoveryResult
	wait(t, func() bool {
		var d domain.SessionDetail
		call(t, c, "session", protocol.SessionQuery{ID: id}, &d)
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		err := c.Call(ctx, "session.reconcile", protocol.RecoveryInput{SessionID: id, Revision: d.Session.Revision, Action: action}, &result)
		if err == nil {
			return true
		}
		if strings.Contains(err.Error(), "会话状态已改变") {
			return false
		}
		t.Fatal(err)
		return false
	})
	return result
}
