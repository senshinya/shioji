package store_test

import (
	"encoding/json"
	"fmt"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestWorkSurvivesParentCompletionWithoutEnteringTranscript(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	op, err := db.Accept(domain.Submission{OperationID: "work-root", WorkspaceID: w.ID, Text: "parallel work"})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(raw string) {
		t.Helper()
		var e domain.Event
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Apply(op.SessionID, e); err != nil {
			t.Fatal("work event rejected", err)
		}
	}
	apply(`{"type":"work.upsert","work":{"id":"child-a","kind":"agent","turnId":"root-turn","title":"检查边界","state":"running"}}`)
	apply(`{"type":"work.item","workId":"child-a","workItem":{"id":"child-message","kind":"assistant","text":"child only","final":true}}`)
	apply(`{"type":"turn","turn":{"id":"root-turn","status":"completed"}}`)
	detail, err := db.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Session.Status != "completed" {
		t.Fatal("child changed parent lifecycle", detail.Session.Status)
	}
	if len(detail.Items) != 1 {
		t.Fatal("child leaked into main transcript", detail.Items)
	}
	b, _ := json.Marshal(detail)
	var wire struct{ Works []struct{ ID, State string } }
	json.Unmarshal(b, &wire)
	if len(wire.Works) != 1 || wire.Works[0].ID != "child-a" || wire.Works[0].State != "running" {
		t.Fatal("parent completion erased running work", string(b))
	}
}

func TestWorkDetailIsSessionScopedAndKeepsItemOrder(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	a, _ := db.Accept(domain.Submission{OperationID: "a", WorkspaceID: w.ID, Text: "a"})
	b, _ := db.Accept(domain.Submission{OperationID: "b", WorkspaceID: w.ID, Text: "b"})
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "t", Title: "审阅", State: "running"}
	if _, err := db.Apply(a.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		t.Fatal(err)
	}
	for _, i := range []domain.Item{{ID: "one", Kind: "assistant", Text: "first"}, {ID: "two", Kind: "tool", Text: "second"}, {ID: "one", Kind: "assistant", Text: "first updated", Final: true}} {
		if _, err := db.Apply(a.SessionID, domain.Event{Type: "work.item", WorkID: "child", WorkItem: &i}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Work(b.SessionID, "child"); err == nil {
		t.Fatal("cross-session detail exposed")
	}
	got, err := db.Work(a.SessionID, "child")
	if err != nil || len(got.Items) != 2 || got.Items[0].Item.Text != "first updated" || got.Items[1].Item.ID != "two" {
		t.Fatal(got, err)
	}
	work.TurnID = "different-root"
	if _, err := db.Apply(a.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err == nil {
		t.Fatal("existing work moved to a different root turn")
	}
}

func TestWorkRestartInvalidationPreservesTerminalResultsAndBoundedReads(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	op, _ := db.Accept(domain.Submission{OperationID: "root", WorkspaceID: w.ID, Text: "work"})
	for _, state := range []string{"running", "completed"} {
		work := domain.WorkActivity{ID: state, Kind: "agent", TurnID: "t", Title: state, State: state, CanStop: state == "running"}
		if _, err := db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 70; i++ {
		item := domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: fmt.Sprint(i)}
		if _, err := db.Apply(op.SessionID, domain.Event{Type: "work.item", WorkID: "completed", WorkItem: &item}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InvalidateWork(); err != nil {
		t.Fatal(err)
	}
	active, _ := db.Work(op.SessionID, "running")
	done, _ := db.Work(op.SessionID, "completed")
	if active.Work.State != "unknown" || active.Work.CanStop || done.Work.State != "completed" || !done.Truncated || len(done.Items) != 64 || done.Items[0].Item.Text != "6" {
		t.Fatal(active, done)
	}
}

func TestLargeWorkRosterIsBoundedWithoutLosingPersistedDetails(t *testing.T) {
	db := open(t)
	workspace := workspace(t, db)
	op, _ := db.Accept(domain.Submission{OperationID: "roster", WorkspaceID: workspace.ID, Text: "work"})
	for i := 0; i < 150; i++ {
		w := domain.WorkActivity{ID: fmt.Sprint(i), Kind: "agent", TurnID: "t", Title: "check", Brief: strings.Repeat("x", 8192), State: "completed"}
		if i == 0 {
			w.State = "running"
		}
		if _, err := db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &w}); err != nil {
			t.Fatal(err)
		}
	}
	d, err := db.Session(op.SessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	if len(b) > 160<<10 || len(d.Works) > 128 {
		t.Fatal("work roster can break session transport", len(b), len(d.Works))
	}
	found := false
	for _, w := range d.Works {
		found = found || w.ID == "0"
		if w.Brief != "" {
			t.Fatal("large detail repeated in roster")
		}
	}
	if !found {
		t.Fatal("older active work hidden by recent completions")
	}
	detail, err := db.Work(op.SessionID, "1")
	if err != nil || len(detail.Work.Brief) != 8192 {
		t.Fatal("limiting projection discarded persisted detail", err)
	}
}

func TestLiveChildBlocksWorkerStopAndHistoricalRestart(t *testing.T) {
	db := open(t)
	w := workspace(t, db)
	op, _ := db.Accept(domain.Submission{OperationID: "busy-child", WorkspaceID: w.ID, Text: "work"})
	db.SetOperation(op.ID, "confirmed", "")
	db.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:busy-child", Kind: "user", TurnID: "t", Text: "work", Status: "sent", Final: true}})
	db.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
	d, _ := db.Session(op.SessionID, 0, 100)
	d.Session.ThreadID = "source"
	db.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "t", Title: "child", State: "running", OriginRef: "source"}
	if _, err := db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		t.Fatal(err)
	}
	result, err := db.PrepareWorkerStop("stop")
	if err != nil || result.Ready {
		t.Error("worker stop ignored active child", result, err)
	}
	d, _ = db.Session(op.SessionID, 0, 100)
	_, err = db.PrepareHistory(domain.HistoryChange{OperationID: "restart-child", SessionID: op.SessionID, Revision: d.Session.Revision, ItemID: "input:busy-child", Restart: true, Text: "new"})
	if err == nil || !strings.Contains(err.Error(), "子 Agent") {
		t.Error("history replacement orphaned active child", err)
	}
}
