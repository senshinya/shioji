package store_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"shioji/internal/domain"
)

func TestHistoryRoundsCountInsertedUserMessagesInsteadOfHarnessTurns(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "rounds", WorkspaceID: w.ID, Text: "initial", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 25; round++ {
		items := []domain.Item{{ID: fmt.Sprintf("u%02d", round), Kind: "user", TurnID: "same-harness-turn", Text: fmt.Sprintf("inserted prompt %d", round), Final: true}}
		for tool := 0; tool < 8; tool++ {
			items = append(items, domain.Item{ID: fmt.Sprintf("t%02d-%d", round, tool), Kind: "tool", TurnID: "same-harness-turn", Text: "output", Final: true})
		}
		for _, item := range items {
			if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
				t.Fatal(err)
			}
		}
	}
	read := func(q domain.HistoryQuery, first, last string, users int) domain.SessionDetail {
		t.Helper()
		q.SessionID = op.SessionID
		d, e := s.SessionHistory(q)
		if e != nil {
			t.Fatal(e)
		}
		var prompts []string
		for _, item := range d.Items {
			if item.Kind == "user" && item.WorkID == "" {
				prompts = append(prompts, item.ID)
			}
		}
		if len(prompts) != users || prompts[0] != first || prompts[len(prompts)-1] != last || d.Items[0].ID != first || len(d.Items) != users*9 {
			t.Fatal("round boundaries split or miscounted", prompts, len(d.Items), d.History)
		}
		return d
	}
	latest := read(domain.HistoryQuery{Rounds: 20}, "u05", "u24", 20)
	if !latest.History.Older || latest.History.Newer {
		t.Fatal(latest.History)
	}
	older := read(domain.HistoryQuery{Rounds: 3, Before: latest.History.First}, "u02", "u04", 3)
	read(domain.HistoryQuery{Rounds: 3, After: older.History.Last}, "u05", "u07", 3)
	read(domain.HistoryQuery{Rounds: 3, AroundID: "t07-4"}, "u07", "u09", 3)
}

func TestHistoryRoundsPageGoalContinuations(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "goal-rounds", WorkspaceID: w.ID, Text: "/goal keep going", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 25; round++ {
		turn := fmt.Sprint(round)
		for _, item := range []domain.Item{
			{ID: "goal-" + turn, TurnID: turn, Kind: "activity", Presentation: domain.GoalContinuationPresentation, Final: true},
			{ID: "reply-" + turn, TurnID: turn, Kind: "assistant", Text: "Progress", Final: true},
		} {
			if _, err := s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
				t.Fatal(err)
			}
		}
	}
	read := func(q domain.HistoryQuery, first, last string, count int) domain.SessionDetail {
		t.Helper()
		q.SessionID = op.SessionID
		d, err := s.SessionHistory(q)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Items) != count || d.Items[0].ID != first || d.Items[count-1].ID != last || d.History.ContextID != "" {
			t.Fatalf("expected %d items from %s through %s, got %d items with history %+v", count, first, last, len(d.Items), d.History)
		}
		return d
	}
	latest := read(domain.HistoryQuery{Rounds: domain.DefaultHistoryRounds}, "goal-6", "reply-25", 40)
	if !latest.History.Older || latest.History.Newer {
		t.Fatal(latest.History)
	}
	older := read(domain.HistoryQuery{Rounds: 2, Before: latest.History.First}, "goal-4", "reply-5", 4)
	read(domain.HistoryQuery{Rounds: 2, After: older.History.Last}, "goal-6", "reply-7", 4)
	read(domain.HistoryQuery{Rounds: 2, AroundID: "reply-8"}, "goal-8", "reply-9", 4)
}

func TestHistoryRoundBodiesContinueByBytesWithoutAnItemLimit(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "large-round", WorkspaceID: w.ID, Text: "one round", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 130; i++ {
		item := domain.Item{ID: fmt.Sprint("tool-", i), Kind: "tool", TurnID: "turn", Text: strings.Repeat("output ", 4096), Final: true}
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
			t.Fatal(err)
		}
	}
	q := domain.HistoryQuery{SessionID: op.SessionID, Rounds: 1}
	seen := map[string]bool{}
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("round continuation made no progress")
		}
		d, e := s.SessionHistory(q)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(d)
		if len(b) > 768<<10 {
			t.Fatal("unbounded round frame", len(b))
		}
		for _, item := range d.Items {
			seen[item.ID] = true
		}
		if d.History.Older || d.History.Newer {
			t.Fatal("transport chunk masquerades as another conversation round", d.History)
		}
		if d.History.Last == d.History.WindowLast {
			break
		}
		if d.History.Last <= q.After {
			t.Fatal("non-advancing round cursor", q, d.History)
		}
		q.After, q.WindowFirst, q.WindowLast = d.History.Last, d.History.WindowFirst, d.History.WindowLast
	}
	if len(seen) != 131 {
		t.Fatal("single round was cut at a message count", len(seen))
	}
}
