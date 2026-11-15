package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/store"
)

func TestCompletionReadPersistsAndLateTurnUpdatesDoNotResurrectIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	e, err := s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed", CompletedAt: &at}})
	if err != nil {
		t.Fatal(err)
	}
	if e.Session.Completion == nil || !e.Session.Completion.CompletedAt.Equal(at) {
		t.Fatal("actual completion time lost")
	}
	id := e.Session.Completion.ID
	if _, err = s.ReadCompletion(op.SessionID, id); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := e.Session.Completion; c == nil || c.ID != id || c.Unread || !c.CompletedAt.Equal(at) {
		t.Fatal("late usage/replay resurrected read state", c)
	}
	// Adapter session snapshots do not own or overwrite the read watermark.
	native := *e.Session
	native.Completion = nil
	e, err = s.Apply(op.SessionID, domain.Event{Type: "session", Session: &native})
	if err != nil {
		t.Fatal(err)
	}
	if c := e.Session.Completion; c == nil || c.Unread || c.ID != id {
		t.Fatal("adapter overwrote worker metadata", c)
	}
}

func TestOnlyNaturalCompletedTurnsCreateCompletionRecords(t *testing.T) {
	for _, turn := range []domain.Turn{
		{ID: "t", Status: "running"}, {ID: "t", Status: "interrupted"}, {ID: "t", Status: "failed"},
		{ID: "t", Status: "completed", Presentation: "compaction"},
	} {
		t.Run(string(turn.Status)+turn.Presentation, func(t *testing.T) {
			s := open(t)
			w := workspace(t, s)
			op, err := s.Accept(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			e, err := s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &turn})
			if err != nil {
				t.Fatal(err)
			}
			if e.Session.Completion != nil {
				t.Fatal("non-reply created completion attention")
			}
		})
	}
}

func TestReconciledCompletionIsDurableAndAcknowledgedAcrossSnapshots(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "recover", WorkspaceID: w.ID, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "lost", Status: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	recovered, err := s.Reconcile(op.SessionID, d.Session.Revision, domain.RecoveryHistory{Available: true, State: "idle", Turns: []domain.Turn{{ID: "lost", Status: "completed", CompletedAt: &at}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Completion == nil || !recovered.Completion.Unread || recovered.Completion.TurnID != "lost" {
		t.Fatal("recovered completion has no worker-owned acknowledgement", recovered.Completion)
	}
	read, err := s.ReadCompletion(op.SessionID, recovered.Completion.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Completion.Unread {
		t.Fatal("recovered completion cannot be acknowledged")
	}
}
