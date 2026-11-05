package worker

import (
	"context"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness/codex"
	"shioji/internal/store"
	"shioji/internal/testutil"
	"testing"
	"time"
)

func TestQueueCompletionAndStopPreservePendingInput(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, _ := db.CreateWorkspace("test", []string{t.TempDir()})
	op, _ := db.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	db.Enqueue(domain.Submission{OperationID: "second", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "next"})
	s := &Server{store: db, ctx: context.Background(), queueWake: make(chan struct{}, 1)}
	if err = s.onEvent(domain.Event{SessionID: op.SessionID, Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.queueWake:
	default:
		t.Fatal("confirmed completion did not wake queue")
	}
	q, _ := db.Queue(op.SessionID)
	if q.Paused || len(q.Items) != 1 {
		t.Fatal(q)
	}
	if err = s.onEvent(domain.Event{SessionID: op.SessionID, Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "interrupted"}}); err != nil {
		t.Fatal(err)
	}
	q, _ = db.Queue(op.SessionID)
	if !q.Paused || len(q.Items) != 1 {
		t.Fatal("interrupt drained queue", q)
	}
}

func TestRejectedInitialDispatchPausesMessagesEnqueuedWhileStarting(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := &Server{store: db, ctx: ctx, queueWake: make(chan struct{}, 1), bound: map[string]bool{}}
	s.runtime, err = codex.Start(ctx, codex.Options{Home: p.Home, OnEvent: s.onEvent})
	if err != nil {
		t.Fatal(err)
	}
	defer s.runtime.Close()
	w, _ := db.CreateWorkspace("test", []string{t.TempDir()})
	in := domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "initial", Config: domain.RunConfig{Model: s.runtime.Readiness().Models[0].ID, Permission: "read-only"}}
	op, err := db.Accept(in)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := db.Session(op.SessionID, 0, 1)
	d.Session.ThreadID = "missing-thread"
	db.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	db.Enqueue(domain.Submission{OperationID: "later", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "retain", Config: in.Config})
	s.dispatch(in, op)
	q, _ := db.Queue(op.SessionID)
	if !q.Paused || len(q.Items) != 1 {
		t.Fatal("failed initial dispatch advances later input", q)
	}
}
