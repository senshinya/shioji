package store_test

import (
	"database/sql"
	"errors"
	"shioji/internal/domain"
	"testing"
)

func TestCompletionInterruptionRequiresSessionOrCommittedDeletion(t *testing.T) {
	s := open(t)
	if err := s.InterruptCompletion("unknown-session"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing nondeleted session must retain its error", err)
	}
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	e, err := s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.SetSessionArchived(op.SessionID, e.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteArchivedSession(op.SessionID, archived.Revision); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.InterruptCompletion(op.SessionID); err != nil {
			t.Fatal("committed deletion must make old completion release idempotent", err)
		}
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 0 {
		t.Fatal("late completion resurrected deleted session", snapshot.Sessions)
	}
}
