package store_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestRuntimeReleasePreservesUnknownInputAndRejectsLateSettlement(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	in := domain.Submission{OperationID: "unknown", WorkspaceID: w.ID, Text: "once"}
	op, err := s.Accept(in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, "dispatched", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "running"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(domain.Submission{OperationID: "queued", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "keep queued"}); err != nil {
		t.Fatal(err)
	}
	if err = s.GuardExecution(op.SessionID, "old"); err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseRuntime(op.SessionID, "old", "old result unknown", false); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(op.SessionID, 0, 100)
	if err != nil || !d.Session.Status.CanSubmit() || d.Session.ExecutionGuard != "" || !d.Queue.Paused || len(d.Queue.Items) != 1 || d.Queue.Items[0].Input.Text != "keep queued" {
		t.Fatal("release lost history or queue state", d, err)
	}
	if again, err := s.Accept(in); err != nil || again.State != "abandoned" || again.Error != "old result unknown" {
		t.Fatal("old input became replayable or falsely confirmed", again, err)
	}
	if err = s.SetOperation(op.ID, "confirmed", "late old finalizer"); err == nil {
		t.Fatal("retired finalizer can overwrite the unknown result")
	}
}
