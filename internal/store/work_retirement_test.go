package store_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestWorkStartupInvalidationReleasesOldUnknownObservation(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "old", Kind: "background", TurnID: "t", State: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.InvalidateWork(); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Works) != 1 || d.Works[0].State != "unknown" || !d.Works[0].Retired {
		t.Fatal("new owner kept historical observation live or invented stop", d.Works)
	}
	idle, err := s.RuntimeGCIdle(op.SessionID)
	if err != nil || !idle.Idle {
		t.Fatal("old worker observation pins new owner", idle, err)
	}
}
