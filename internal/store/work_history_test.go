package store_test

import (
	"fmt"
	"strings"
	"testing"

	"shioji/internal/domain"
)

func TestChildHistoryPagesCompleteRoundsAndKeepsOwningScope(t *testing.T) {
	db := open(t)
	ws := workspace(t, db)
	op, err := db.Accept(domain.Submission{OperationID: "child-pages", WorkspaceID: ws.ID, Text: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	w := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "parent-turn", State: "running", Title: "child"}
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &w}); err != nil {
		t.Fatal(err)
	}
	add := func(item domain.Item) {
		t.Helper()
		if _, err := db.Apply(op.SessionID, domain.Event{Type: "work.item", WorkID: "child", WorkItem: &item}); err != nil {
			t.Fatal(err)
		}
	}
	for round := 1; round <= 12; round++ {
		add(domain.Item{ID: fmt.Sprint("u", round), Kind: "user", TurnID: "same-child-turn", Text: fmt.Sprint(round), Final: true})
		for i := 0; i < 2; i++ {
			add(domain.Item{ID: fmt.Sprintf("a%d-%d", round, i), Kind: "assistant", Text: "result", Final: true})
		}
	}
	q := domain.WorkHistoryQuery{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID, Rounds: 5}}
	latest, err := db.WorkHistory(q)
	if err != nil || len(latest.Items) != 15 || latest.Items[0].Item.ID != "u8" || latest.Items[14].Item.ID != "a12-1" || !latest.History.Older || latest.History.Newer {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	q.Before = latest.History.First
	older, err := db.WorkHistory(q)
	if err != nil || len(older.Items) != 15 || older.Items[0].Item.ID != "u3" || older.Items[14].Item.ID != "a7-1" || !older.History.Older || !older.History.Newer {
		t.Fatalf("older: %+v %v", older, err)
	}
	q.Before = older.History.First
	oldest, err := db.WorkHistory(q)
	if err != nil || len(oldest.Items) != 6 || oldest.Items[0].Item.ID != "u1" || oldest.History.Older {
		t.Fatalf("oldest: %+v %v", oldest, err)
	}
	q.Before = oldest.History.First
	empty, err := db.WorkHistory(q)
	if err != nil || len(empty.Items) != 0 {
		t.Fatal("read before first", empty, err)
	}
	for _, bad := range []domain.WorkHistoryQuery{
		{ID: "other", HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID, Rounds: 5}},
		{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: "other", Rounds: 5}},
		{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID, ThreadID: "stale-source", Rounds: 5}},
		{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID, Generation: "stale-generation", Rounds: 5}},
	} {
		if _, err := db.WorkHistory(bad); err == nil {
			t.Fatal("accepted mismatched scope", bad)
		}
	}
}

func TestChildHistoryLargeRoundSpansByteChunksWithoutRecordCap(t *testing.T) {
	db := open(t)
	ws := workspace(t, db)
	op, err := db.Accept(domain.Submission{OperationID: "child-chunks", WorkspaceID: ws.ID, Text: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	w := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "t", State: "completed", Title: "child"}
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &w}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 132; i++ {
		item := domain.Item{ID: fmt.Sprint(i), Kind: "tool", Text: strings.Repeat("内容", 2000), Final: true}
		if i == 0 {
			item.Kind = "user"
		}
		if _, err = db.Apply(op.SessionID, domain.Event{Type: "work.item", WorkID: "child", WorkItem: &item}); err != nil {
			t.Fatal(err)
		}
	}
	q := domain.WorkHistoryQuery{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID, Rounds: 1}}
	count, chunks := 0, 0
	for {
		d, err := db.WorkHistory(q)
		if err != nil {
			t.Fatal(err)
		}
		chunks++
		count += len(d.Items)
		if d.History.Last >= d.History.WindowLast {
			break
		}
		if d.History.Last <= q.After {
			t.Fatal("no progress")
		}
		q.Before = 0
		q.After = d.History.Last
		q.WindowFirst = d.History.WindowFirst
		q.WindowLast = d.History.WindowLast
	}
	if count != 132 || chunks < 3 {
		t.Fatal("round lost records", count, chunks)
	}
}
