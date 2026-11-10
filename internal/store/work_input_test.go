package store_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestWorkInputDurableReplayDoesNotMutateParent(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	op, err := db.Accept(domain.Submission{OperationID: "root-input", WorkspaceID: w.ID, Text: "root"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := db.Session(op.SessionID, 0, 100)
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "root-turn", State: "completed", CanContinue: true}
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		t.Fatal(err)
	}
	in := domain.WorkInput{OperationID: "child-input", SessionID: op.SessionID, WorkID: "child", Mode: "continue", Text: "continue child"}
	got, fresh, err := db.AcceptWorkInput(in)
	if err != nil || !fresh || got.State != "accepted" {
		t.Fatal(got, fresh, err)
	}
	if err = db.SetWorkInput(in.OperationID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	got, fresh, err = db.AcceptWorkInput(in)
	if err != nil || fresh || got.State != "confirmed" {
		t.Fatal("replay", got, fresh, err)
	}
	in.Text = "different"
	if _, _, err = db.AcceptWorkInput(in); err == nil {
		t.Fatal("conflicting operation reused")
	}
	after, _ := db.Session(op.SessionID, 0, 100)
	if len(after.Items) != len(before.Items) || after.Session.Status != before.Session.Status {
		t.Fatal("child send changed parent", after)
	}
	d, err := db.Work(op.SessionID, "child")
	if err != nil || len(d.Items) != 1 || d.Items[0].Item.Kind != "user" || d.Items[0].Item.Text != "continue child" {
		t.Fatal("child input missing", d, err)
	}
	if err = db.SetWorkInput("child-input", "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	d, _ = db.Work(op.SessionID, "child")
	if len(d.Items) != 1 {
		t.Fatal("receipt duplicated child input")
	}
}

func TestWorkInputReceiptKeepsAcceptanceOrderAndCrashDoesNotResend(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	op, _ := db.Accept(domain.Submission{OperationID: "root", WorkspaceID: w.ID, Text: "root"})
	db.SetOperation("root", "confirmed", "")
	db.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "t", State: "completed", CanContinue: true}
	db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &work})
	in := domain.WorkInput{OperationID: "input", SessionID: op.SessionID, WorkID: "child", Mode: "continue", Text: "next"}
	if _, _, err := db.AcceptWorkInput(in); err != nil {
		t.Fatal(err)
	}
	db.Apply(op.SessionID, domain.Event{Type: "work.item", WorkID: "child", WorkItem: &domain.Item{ID: "quick-reply", Kind: "assistant", Text: "reply", Final: true}})
	if err := db.SetWorkInput("input", "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	d, _ := db.Work(op.SessionID, "child")
	if len(d.Items) != 2 || d.Items[0].Item.Kind != "user" {
		t.Fatal("fast child response precedes its input", d.Items)
	}
	in.OperationID = "uncertain"
	if _, _, err := db.AcceptWorkInput(in); err != nil {
		t.Fatal(err)
	}
	if err := db.SetWorkInput("uncertain", "dispatched", ""); err != nil {
		t.Fatal(err)
	}
	if result, err := db.PrepareWorkerStop("upgrade"); err != nil || result.Ready {
		t.Fatal("unknown child delivery allowed worker replacement", result, err)
	}
	if err := db.InvalidateWork(); err != nil {
		t.Fatal(err)
	}
	p, fresh, err := db.AcceptWorkInput(in)
	if err != nil || fresh || p.State != "indeterminate" {
		t.Fatal("crash allowed redispatch", p, fresh, err)
	}
}
