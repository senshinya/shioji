package store_test

import (
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"testing"
)

func TestQueueDurableFIFOEditingAndAtomicDispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "initial", WorkspaceID: w.ID, Text: "first"})
	in := domain.Submission{OperationID: "queued", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "second", Config: domain.RunConfig{Model: "captured"}}
	if _, err = s.Enqueue(in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(in); err != nil {
		t.Fatal(err)
	}
	bad := in
	bad.Text = "different"
	if _, err = s.Enqueue(bad); err == nil {
		t.Fatal("operation ID reused")
	}
	q, _ := s.Queue(op.SessionID)
	if len(q.Items) != 1 || q.Items[0].Input.Config.Model != "captured" {
		t.Fatalf("queue %+v", q)
	}
	d, _ := s.Session(op.SessionID, 0, 100)
	if len(d.Items) != 1 {
		t.Fatal("queued input entered history")
	}
	if _, err = s.EditQueue(op.SessionID, q.Items[0].ID, q.Revision-1, "edited"); err == nil {
		t.Fatal("stale edit accepted")
	}
	q, err = s.EditQueue(op.SessionID, q.Items[0].ID, q.Revision, "edited")
	if err != nil {
		t.Fatal(err)
	}
	session := d.Session
	session.Status = "completed"
	s.Apply(session.ID, domain.Event{Type: "session", Session: &session})
	next := q.Items[0].Input
	if _, err = s.AcceptQueued(next, q.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptQueued(next, q.Revision); err != nil {
		t.Fatal("idempotent dispatch", err)
	}
	q, _ = s.Queue(session.ID)
	d, _ = s.Session(session.ID, 0, 100)
	if len(q.Items) != 1 || q.Items[0].State != "dispatching" || len(d.Items) != 1 {
		t.Fatal("unconfirmed delivery consumed queue", q, d.Items)
	}
	if err = s.SetOperation(next.OperationID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	s.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + next.OperationID, Kind: "user", Text: next.Text, Status: "sent", Final: true}})
	q, _ = s.Queue(session.ID)
	d, _ = s.Session(session.ID, 0, 100)
	if len(q.Items) != 0 || len(d.Items) != 2 || d.Items[1].Text != "edited" {
		t.Fatalf("queue/history %+v %+v", q, d.Items)
	}
	s.SetOperation(next.OperationID, "dispatched", "")
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Operation(next.OperationID)
	if got.State != "indeterminate" {
		t.Fatalf("unsafe replay %s", got.State)
	}
}

func TestQueueRecoveryPausesAndRemovalRequiresRevision(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	s.Enqueue(domain.Submission{OperationID: "q", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "retain"})
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	q, _ := s.Queue(op.SessionID)
	if !q.Paused || len(q.Items) != 1 {
		t.Fatalf("recovery %+v", q)
	}
	if _, err := s.RemoveQueue(op.SessionID, q.Items[0].ID, q.Revision-1); err == nil {
		t.Fatal("stale remove")
	}
	if _, err := s.AcceptQueued(q.Items[0].Input, q.Revision); err == nil {
		t.Fatal("paused dispatch")
	}
	q, err := s.RemoveQueue(op.SessionID, q.Items[0].ID, q.Revision)
	if err != nil || len(q.Items) != 0 {
		t.Fatal(q, err)
	}
}

func TestSteerIsIdempotentAndNeverReplacesQueue(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "active", Status: "running"}})
	s.Enqueue(domain.Submission{OperationID: "q", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "keep"})
	in := domain.Submission{OperationID: "steer", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "change direction"}
	if _, err := s.AcceptSteer(in, "old"); err == nil {
		t.Fatal("stale turn steered")
	}
	first, err := s.AcceptSteer(in, "active")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.AcceptSteer(in, "active")
	if err != nil || first.ID != again.ID {
		t.Fatal(again, err)
	}
	q, _ := s.Queue(op.SessionID)
	if len(q.Items) != 1 {
		t.Fatal("steer changed queue")
	}
}

func TestRejectedQueueDeliveryRetainsTextAndRequiresExplicitRetry(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	d, _ := s.Session(op.SessionID, 0, 1)
	d.Session.Status = "completed"
	s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	s.Enqueue(domain.Submission{OperationID: "q", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "keep after rejection"})
	q, _ := s.Queue(op.SessionID)
	input := q.Items[0].Input
	s.AcceptQueued(input, q.Revision)
	s.SetOperation(input.OperationID, "rejected", "configuration unavailable")
	q, _ = s.Queue(op.SessionID)
	if len(q.Items) != 1 || !q.Paused || q.Items[0].Input.Text != input.Text {
		t.Fatal("rejection lost queued input", q)
	}
	q, err := s.ContinueQueue(op.SessionID, q.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if q.Items[0].Input.OperationID == input.OperationID {
		t.Fatal("retry reused rejected dispatch identity")
	}
}
func TestQueuedSteerReservesUntilConfirmed(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "active", Status: "running"}})
	s.Enqueue(domain.Submission{OperationID: "q", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "steer this"})
	q, _ := s.Queue(op.SessionID)
	in := q.Items[0].Input
	if _, err := s.AcceptQueuedSteer(in, "active", q.Revision); err != nil {
		t.Fatal(err)
	}
	q, _ = s.Queue(op.SessionID)
	if len(q.Items) != 1 || q.Items[0].State != "dispatching" {
		t.Fatal(q)
	}
	s.SetOperation(in.OperationID, "confirmed", "")
	q, _ = s.Queue(op.SessionID)
	if len(q.Items) != 0 {
		t.Fatal(q)
	}
}

func TestNegativeRevisionCannotBypassQueueMutationGuard(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	s.Enqueue(domain.Submission{OperationID: "q", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "keep"})
	q, _ := s.Queue(op.SessionID)
	if _, err := s.EditQueue(op.SessionID, q.Items[0].ID, -1, "stale"); err == nil {
		t.Fatal("negative revision edited queue")
	}
	if _, err := s.RemoveQueue(op.SessionID, q.Items[0].ID, -1); err == nil {
		t.Fatal("negative revision removed queue")
	}
	s.PauseQueue(op.SessionID, "stop")
	if _, err := s.ContinueQueue(op.SessionID, -1); err == nil {
		t.Fatal("negative revision resumed queue")
	}
}

func TestImmediateQueuedDispatchSelectsOneAndKeepsPause(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	d, _ := s.Session(op.SessionID, 0, 1)
	d.Session.Status = "interrupted"
	s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	for _, text := range []string{"one", "two"} {
		s.Enqueue(domain.Submission{OperationID: text, WorkspaceID: w.ID, SessionID: op.SessionID, Text: text})
	}
	q, _ := s.PauseQueue(op.SessionID, "stopped")
	in := q.Items[1].Input
	if _, err := s.AcceptQueuedNow(in, q.Revision); err != nil {
		t.Fatal(err)
	}
	s.SetOperation(in.OperationID, "confirmed", "")
	q, _ = s.Queue(op.SessionID)
	if len(q.Items) != 1 || q.Items[0].Input.Text != "one" || !q.Paused {
		t.Fatal("single immediate send drained/resumed remaining queue", q)
	}
}

func TestFreshAcceptedTurnClearsEmptyQueuePause(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	d, _ := s.Session(op.SessionID, 0, 1)
	d.Session.Status = "interrupted"
	s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	s.PauseQueue(op.SessionID, "stopped")
	if _, err := s.Accept(domain.Submission{OperationID: "new", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "new explicit task"}); err != nil {
		t.Fatal(err)
	}
	q, _ := s.Queue(op.SessionID)
	if q.Paused {
		t.Fatal("fresh task inherited empty queue pause")
	}
}

func TestQueueReorderPersistsAndRejectsStaleOrInFlightChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	for _, id := range []string{"one", "two", "three"} {
		s.Enqueue(domain.Submission{OperationID: id, WorkspaceID: w.ID, SessionID: op.SessionID, Text: id})
	}
	q, _ := s.PauseQueue(op.SessionID, "stopped")
	q, err = s.ReorderQueue(op.SessionID, "three", "one", q.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Paused || q.Items[0].ID != "three" || q.Items[1].ID != "one" || q.Items[2].ID != "two" {
		t.Fatal(q)
	}
	if _, err = s.ReorderQueue(op.SessionID, "one", "", q.Revision-1); err == nil {
		t.Fatal("stale reorder")
	}
	if _, err = s.ReorderQueue(op.SessionID, "one", "missing", q.Revision); err == nil {
		t.Fatal("unknown target")
	}
	q, err = s.ReorderQueue(op.SessionID, "three", "", q.Revision)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q, _ = s.Queue(op.SessionID)
	if q.Items[0].ID != "one" || q.Items[2].ID != "three" {
		t.Fatal("reorder not durable", q)
	}
	d, _ := s.Session(op.SessionID, 0, 1)
	d.Session.Status = "completed"
	s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	q, _ = s.ContinueQueue(op.SessionID, q.Revision)
	if _, err = s.AcceptQueued(q.Items[0].Input, q.Revision); err != nil {
		t.Fatal(err)
	}
	q, _ = s.Queue(op.SessionID)
	if _, err = s.ReorderQueue(op.SessionID, "two", "one", q.Revision); err == nil {
		t.Fatal("reordered reserved head")
	}
}
