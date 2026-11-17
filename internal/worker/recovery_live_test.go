package worker

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/testutil"
	"testing"
	"time"
)

func TestReconcileReattachesAnObservedLiveTurnWithoutInterruptingOrResending(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-live-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.store.CreateWorkspace("Live", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	r := s.currentRuntime()
	op, err := s.submit(domain.Submission{OperationID: "live", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not start")
	}
	r = s.currentRuntime(op.SessionID)
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		session := d.Session
		session.Status = "unknown"
		if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "session", Session: &session}); err != nil {
			t.Fatal(err)
		}
		d, _ = s.store.Session(op.SessionID, 0, 100)
	}()
	result, err := s.reconcile(protocol.RecoveryInput{SessionID: op.SessionID, Revision: d.Session.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" {
		t.Fatalf("observed live turn not attached: %+v", result)
	}
	if s.currentRuntime(op.SessionID) != r || len(p.Inputs()) != 1 {
		t.Fatal("reattach replaced runtime or replayed input")
	}
	close(p.Release)
	deadline := time.Now().Add(10 * time.Second)
	for {
		d, _ = s.store.Session(op.SessionID, 0, 100)
		if d.Session.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reattached events did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDelayedDispatchCannotResurrectAnAbandonedInputOnReplacementRuntime(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-delay-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, _ := s.store.CreateWorkspace("Delayed", []string{folder})
	old := s.currentRuntime()
	config := domain.RunConfig{Model: old.Readiness().Models[0].ID, Permission: "read-only"}
	first, err := s.submit(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "hello", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		d, _ := s.store.Session(first.SessionID, 0, 100)
		if d.Session.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	input := domain.Submission{OperationID: "delayed", WorkspaceID: w.ID, SessionID: first.SessionID, Text: "must never execute", Config: config}
	op, err := s.store.Accept(input)
	if err != nil {
		t.Fatal(err)
	}
	s.store.Enqueue(domain.Submission{OperationID: "saved-queue", WorkspaceID: w.ID, SessionID: first.SessionID, Text: "keep waiting", Config: config})
	old = s.currentRuntime(first.SessionID)
	old.Close(context.Background())
	s.dispatchMu.Lock()
	d, _ := s.store.Session(first.SessionID, 0, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	// Automatic recovery may already own a transition when Close returns.
	// Query first, then use the authoritative revision for any remaining end.
	result, err := s.reconcileLocked(ctx, protocol.RecoveryInput{SessionID: first.SessionID, Action: "check"})
	if err == nil && result.Status == "unknown" {
		d, _ = s.store.Session(first.SessionID, 0, 100)
		result, err = s.reconcileLocked(ctx, protocol.RecoveryInput{SessionID: first.SessionID, Revision: d.Session.Revision, Action: "end"})
	}
	s.dispatchMu.Unlock()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "abandoned" || result.CanContinue {
		t.Fatalf("old input not abandoned: %+v", result)
	}
	before := len(p.Inputs())
	s.dispatch(input, op, old)
	receipt, _ := s.store.Operation(op.ID)
	if receipt.State != "abandoned" || len(p.Inputs()) != before {
		t.Fatal("delayed goroutine resurrected abandoned input", receipt)
	}
	if _, err = s.submit(domain.Submission{OperationID: "wrong-context-continue", SessionID: first.SessionID, WorkspaceID: w.ID, Text: domain.ContinuationPrompt, Config: config, Continue: true}); err == nil {
		t.Fatal("continuation ignored an unconfirmed user message missing from source context")
	}

}

func TestExplicitContinuationKeepsOriginalNativeThreadAndPausedQueue(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-continue-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	folder, _ := filepath.EvalSymlinks(t.TempDir())
	w, _ := s.store.CreateWorkspace("Original context", []string{folder})
	r := s.currentRuntime()
	config := domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only"}
	op, err := s.submit(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("provider did not start")
	}
	s.dispatchMu.Lock()
	original, _ := s.store.Session(op.SessionID, 0, 100)
	s.store.Enqueue(domain.Submission{OperationID: "waiting", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "keep waiting", Config: config})
	s.dispatchMu.Unlock()
	r = s.currentRuntime(op.SessionID)
	// Serialize this explicit recovery action with automatic recovery. Closing
	// the adapter now also retires its owned auxiliary processes.
	s.dispatchMu.Lock()
	r.Close(context.Background())
	close(p.Release)
	d, _ := s.store.Session(op.SessionID, 0, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	result, err := s.reconcileLocked(ctx, protocol.RecoveryInput{SessionID: op.SessionID, Revision: d.Session.Revision, Action: "end"})
	s.dispatchMu.Unlock()
	cancel()
	if err != nil || result.Status != "interrupted" || !result.CanContinue {
		t.Fatal("confirmed source context not available for continuation", result, err)
	}
	// Workspace changes do not relocate an existing session's accepted directory.
	w.Folders = []string{t.TempDir()}
	if _, err = s.store.UpdateWorkspace(w); err != nil {
		t.Fatal(err)
	}
	before := len(p.Inputs())
	_, err = s.submit(domain.Submission{OperationID: "continuation", SessionID: op.SessionID, WorkspaceID: w.ID, Text: domain.ContinuationPrompt, Config: config, Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(10 * time.Second)
	for {
		d, _ = s.store.Session(op.SessionID, 0, 100)
		if d.Session.Status == "completed" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("continuation did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if d.Session.ThreadID != original.Session.ThreadID || d.Session.Context.CWD != folder || !d.Queue.Paused || len(d.Queue.Items) != 1 || len(p.Inputs()) != before+1 {
		t.Fatal("continuation changed context, released queue or replayed input")
	}
}
