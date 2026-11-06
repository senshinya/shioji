package store_test

import (
	"encoding/json"
	"shioji/internal/domain"
	"testing"
)

func TestLateTerminalEventCannotReleaseManagedExecutionGuard(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "owned", WorkspaceID: w.ID, Text: "work"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "running"}})
	d, _ := s.Session(op.SessionID, 0, 1)
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "checking"}
	if err := s.GuardExecution(op.SessionID, "generation"); err != nil {
		t.Fatal(err)
	}
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}})
	d, _ = s.Session(op.SessionID, 0, 1)
	if d.Session.Status != "unknown" || d.Session.Recovery == nil {
		t.Fatal("late turn completion released an unconfirmed managed execution", d.Session)
	}
}
func TestManagedCleanupProofIsRequiredBeforeReconciliation(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "owned", WorkspaceID: w.ID, Text: "work"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "unknown"}})
	d, _ := s.Session(op.SessionID, 0, 1)
	if err := s.GuardExecution(op.SessionID, "generation"); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Session(op.SessionID, 0, 1)
	source := domain.RecoveryHistory{Available: true, State: "idle", Turns: []domain.Turn{{ID: "turn", Status: "completed"}}}
	if _, err := s.Reconcile(op.SessionID, d.Session.Revision, source, false); err == nil {
		t.Fatal("source completion bypassed managed cleanup")
	}
	var proved domain.RecoveryHistory
	b, _ := json.Marshal(source)
	var fields map[string]json.RawMessage
	json.Unmarshal(b, &fields)
	fields["stoppedGeneration"] = json.RawMessage(`"generation"`)
	b, _ = json.Marshal(fields)
	json.Unmarshal(b, &proved)
	if got, err := s.Reconcile(op.SessionID, d.Session.Revision, proved, false); err != nil || got.Status != "completed" {
		t.Fatal("positive generation proof did not settle completed source", got, err)
	}
}
func TestPositivelyManagedInterruptedTurnRecoversWithoutManualEnd(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "owned", WorkspaceID: w.ID, Text: "work"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "unknown"}})
	d, _ := s.Session(op.SessionID, 0, 1)
	var source domain.RecoveryHistory
	json.Unmarshal([]byte(`{"available":true,"state":"idle","turns":[{"id":"turn","status":"interrupted"}],"inputs":{"input:owned":"turn"},"managedStoppedTurns":{"turn":true}}`), &source)
	got, err := s.Reconcile(op.SessionID, d.Session.Revision, source, false)
	if err != nil || got.Status != "interrupted" {
		t.Fatal("positive process-group cleanup still needed manual abandonment", got, err)
	}
}

func TestMissingExactClientMarkerStaysUnknownUntilExplicitlyEnded(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "missing", WorkspaceID: w.ID, Text: "same text"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, "indeterminate", "lost"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "old", Status: "unknown"}})
	if err != nil {
		t.Fatal(err)
	}
	s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:missing", TurnID: "old", Kind: "user", Text: "same text", Final: true, Status: "unknown"}})
	d, _ := s.Session(op.SessionID, 0, 100)
	source := domain.RecoveryHistory{State: "idle", Turns: []domain.Turn{{ID: "old", Status: "completed"}}, Inputs: map[string]string{"input:different": "old"}}
	got, err := s.Reconcile(op.SessionID, d.Session.Revision, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "unknown" {
		t.Fatal("source turn completion fabricated an input acknowledgement")
	}
	pending, _ := s.HasUnsettledOperation(op.SessionID)
	if !pending {
		t.Fatal("missing marker settled an uncertain operation")
	}
	got, err = s.Reconcile(op.SessionID, got.Revision, source, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "abandoned" {
		t.Fatal("manual end claims source completion")
	}
	pending, _ = s.HasUnsettledOperation(op.SessionID)
	if pending {
		t.Fatal("explicit end did not settle local operation")
	}
	receipt, _ := s.Operation(op.ID)
	if receipt.State != "abandoned" {
		t.Fatal("manual end claims delivery confirmation")
	}
	d, _ = s.Session(op.SessionID, 0, 100)
	if d.Items[0].Status != "unknown" || d.Items[0].Text != "same text" || d.Items[0].TurnID != "old" {
		t.Fatal("manual end changed or confirmed original input")
	}
	if _, err = s.Accept(domain.Submission{OperationID: "next", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "explicit next turn"}); err != nil {
		t.Fatal("explicitly ended session cannot continue", err)
	}
}
func TestActiveSourceCannotBeManuallyEndedOrUnlockCheckout(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "active", WorkspaceID: w.ID, Text: "work"})
	s.Recover()
	d, _ := s.Session(op.SessionID, 0, 100)
	_, err := s.Reconcile(op.SessionID, d.Session.Revision, domain.RecoveryHistory{State: "active"}, true)
	if err == nil {
		t.Fatal("active source abandoned")
	}
	after, _ := s.Session(op.SessionID, 0, 100)
	if after.Session.Revision != d.Session.Revision || after.Session.Status != "unknown" {
		t.Fatal("rejected recovery mutated session")
	}
	pending, _ := s.HasUnsettledOperation(op.SessionID)
	if !pending {
		t.Fatal("active source released unsettled operation")
	}
}

func TestUnknownQueuedDeliveryRemainsPausedAndEditableAfterExplicitEnd(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "first-turn", Status: "completed"}})
	s.Enqueue(domain.Submission{OperationID: "queued", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "uncertain queued input"})
	q, _ := s.Queue(op.SessionID)
	queued, err := s.AcceptQueued(q.Items[0].Input, q.Revision)
	if err != nil {
		t.Fatal(err)
	}
	s.SetOperation(queued.ID, "indeterminate", "lost receipt")
	s.Recover()
	d, _ := s.Session(op.SessionID, 0, 100)
	ended, err := s.Reconcile(op.SessionID, d.Session.Revision, domain.RecoveryHistory{State: "idle"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if ended.Status != "abandoned" {
		t.Fatal("queue unknown not ended")
	}
	q, _ = s.Queue(op.SessionID)
	if len(q.Items) != 1 || !q.Paused {
		t.Fatal("manual end removed or dispatched queue")
	}
	_, err = s.EditQueue(op.SessionID, q.Items[0].ID, q.Revision, "reviewed before resending")
	if err != nil {
		t.Fatal("ended queued delivery remains permanently uneditable", err)
	}
}

func TestLaterCompletedTurnCannotClearEarlierDetachedToolGuard(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "mixed", WorkspaceID: w.ID, Text: "first"})
	s.SetOperation(op.ID, "indeterminate", "lost")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "early", Status: "unknown"}})
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "late", Status: "unknown"}})
	d, _ := s.Session(op.SessionID, 0, 100)
	source := domain.RecoveryHistory{State: "idle", Turns: []domain.Turn{{ID: "early", Status: "interrupted"}, {ID: "late", Status: "completed"}}, Inputs: map[string]string{"input:mixed": "late"}}
	result, err := s.Reconcile(op.SessionID, d.Session.Revision, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unknown" {
		t.Fatal("later completion cleared earlier unknown tool guard", result.Status)
	}
	result, err = s.Reconcile(op.SessionID, result.Revision, source, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "abandoned" {
		t.Fatal("explicit local end was promoted to source completion", result.Status)
	}
}

func TestContinuationDoesNotReleaseAnEmptyPausedQueue(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "old", Status: "interrupted"}})
	s.PauseQueue(op.SessionID, "recovery")
	if _, err := s.Accept(domain.Submission{OperationID: "continue", SessionID: op.SessionID, WorkspaceID: w.ID, Text: domain.ContinuationPrompt, Continue: true}); err != nil {
		t.Fatal(err)
	}
	q, _ := s.Queue(op.SessionID)
	if !q.Paused {
		t.Fatal("continuation released the saved queue pause")
	}
}

func TestLateTurnCompletionDoesNotHideIndeterminateDelivery(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "initial", WorkspaceID: w.ID, Text: "initial"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "active", Status: "running"}})
	steer, err := s.AcceptSteer(domain.Submission{OperationID: "uncertain-steer", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "steer"}, "active")
	if err != nil {
		t.Fatal(err)
	}
	s.SetOperation(steer.ID, "indeterminate", "lost steer receipt")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "active", Status: "completed"}})
	d, _ := s.Session(op.SessionID, 0, 100)
	if d.Session.Status != "unknown" {
		t.Fatal("late completion hid the uncertain delivery", d.Session.Status)
	}
	// A worker restart must also recover the same legacy shape.
	d.Session.Status = "completed"
	s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	s.Recover()
	d, _ = s.Session(op.SessionID, 0, 100)
	if d.Session.Status != "unknown" {
		t.Fatal("restart ignored delivery uncertainty on a completed session")
	}
}

func TestIndeterminateReceiptImmediatelyMakesACompletedSessionRecoverable(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "late-receipt", WorkspaceID: w.ID, Text: "sent"})
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "done", Status: "completed"}})
	s.SetOperation(op.ID, "indeterminate", "lost receipt")
	d, _ := s.Session(op.SessionID, 0, 100)
	if d.Session.Status != "unknown" {
		t.Fatal("late unknown receipt is invisible to automatic reconciliation", d.Session.Status)
	}
}
