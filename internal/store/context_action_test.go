package store_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestCompactionRecoveryUsesMaintenanceReceiptNotUserInput(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	root, err := db.Accept(domain.Submission{OperationID: "root", WorkspaceID: w.ID, Text: "keep me"})
	if err != nil {
		t.Fatal(err)
	}
	db.SetOperation("root", "confirmed", "")
	d, _ := db.Session(root.SessionID, 0, 100)
	d.Session.ThreadID = "opaque-source"
	db.Apply(root.SessionID, domain.Event{Type: "session", Session: &d.Session})
	db.Apply(root.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "root-turn", Status: "completed"}})
	d, _ = db.Session(root.SessionID, 0, 100)
	in := domain.ContextActionRequest{OperationID: "compact", SessionID: root.SessionID, Revision: d.Session.Revision}
	if _, err = db.AcceptContextAction(in, in.Revision); err != nil {
		t.Fatal(err)
	}
	db.SetOperation("compact", "dispatched", "")
	if err = db.Recover(); err != nil {
		t.Fatal(err)
	}
	d, _ = db.Session(root.SessionID, 0, 100)
	if d.Session.ContextAction.State != "unknown" || d.Session.Status != "unknown" {
		t.Fatal("crash dropped reservation", d.Session)
	}
	source := domain.RecoveryHistory{Available: true, State: "idle", Inputs: map[string]string{"input:root": "root-turn"}, Turns: []domain.Turn{{ID: "root-turn", Status: "completed"}, {ID: "compact-turn", Presentation: "compaction", Status: "completed"}}, Items: []domain.Item{{ID: "compact-item", TurnID: "compact-turn", Kind: "activity", Presentation: "compaction", Status: "completed", Final: true}}, ContextAction: &domain.ContextObservation{OperationID: "compact", TurnID: "compact-turn", State: "completed"}}
	got, err := db.Reconcile(root.SessionID, d.Session.Revision, source, false)
	if err != nil || got.Status != "completed" || got.ContextAction.State != "completed" {
		t.Fatal("maintenance required a nonexistent user receipt", got, err)
	}
	op, _ := db.ContextOperation(in)
	if op.State != "confirmed" {
		t.Fatal(op)
	}
	after, _ := db.Session(root.SessionID, 0, 100)
	count := 0
	for _, item := range after.Items {
		if item.Kind == "user" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("compaction introduced user history", after.Items)
	}
	if !after.Queue.Paused {
		t.Fatal("recovery resumed queue")
	}
}

func TestOldCompactionTurnCannotSettleNewAction(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	root, _ := db.Accept(domain.Submission{OperationID: "root", WorkspaceID: w.ID, Text: "keep"})
	db.SetOperation("root", "confirmed", "")
	d, _ := db.Session(root.SessionID, 0, 100)
	d.Session.ThreadID = "source"
	db.Apply(root.SessionID, domain.Event{Type: "session", Session: &d.Session})
	db.Apply(root.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "old-compact", Status: "completed", Presentation: "compaction"}})
	db.Apply(root.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "latest", Status: "completed"}})
	d, _ = db.Session(root.SessionID, 0, 100)
	in := domain.ContextActionRequest{OperationID: "new", SessionID: root.SessionID, Revision: d.Session.Revision}
	if _, err := db.AcceptContextAction(in, in.Revision); err != nil {
		t.Fatal(err)
	}
	db.Apply(root.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "old-compact", Status: "completed", Presentation: "compaction"}})
	op, _ := db.ContextOperation(in)
	d, _ = db.Session(root.SessionID, 0, 100)
	if op.State != "accepted" || d.Session.ContextAction.TurnID != "" || d.Session.Status != "starting" {
		t.Fatal("old event settled new operation", op, d.Session)
	}
}

func TestConfirmedCompactionCanRecoverAfterRuntimeLossWithoutToolConfirmation(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	root, _ := db.Accept(domain.Submission{OperationID: "root", WorkspaceID: w.ID, Text: "keep"})
	db.SetOperation("root", "confirmed", "")
	d, _ := db.Session(root.SessionID, 0, 100)
	d.Session.ThreadID = "source"
	db.Apply(root.SessionID, domain.Event{Type: "session", Session: &d.Session})
	db.Apply(root.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "root-turn", Status: "completed"}})
	d, _ = db.Session(root.SessionID, 0, 100)
	in := domain.ContextActionRequest{OperationID: "compact", SessionID: root.SessionID, Revision: d.Session.Revision}
	if _, err := db.AcceptContextAction(in, in.Revision); err != nil {
		t.Fatal(err)
	}
	db.SetOperation("compact", "confirmed", "")
	db.GuardExecution(root.SessionID, "managed-generation")
	lost, _ := db.Session(root.SessionID, 0, 1)
	if lost.Session.ContextAction.State != "unknown" {
		t.Fatal("lost runtime still presents compaction as running", lost.Session.ContextAction)
	}
	source := domain.RecoveryHistory{StoppedGeneration: "managed-generation", Available: true, State: "idle", Inputs: map[string]string{"input:root": "root-turn"}, Turns: []domain.Turn{{ID: "root-turn", Status: "completed"}, {ID: "compact-turn", Presentation: "compaction", Status: "interrupted"}}, Items: []domain.Item{{ID: "compact-item", TurnID: "compact-turn", Kind: "activity", Presentation: "compaction", Status: "interrupted", Final: true}}, ContextAction: &domain.ContextObservation{OperationID: "compact", TurnID: "compact-turn", State: "interrupted"}}
	d, _ = db.Session(root.SessionID, 0, 100)
	got, err := db.Reconcile(root.SessionID, d.Session.Revision, source, false)
	if err != nil || got.Status != "completed" || got.ContextAction.State != "interrupted" || got.ExecutionGuard != "" {
		t.Fatal("confirmed maintenance could not recover automatically", got, err)
	}
}
