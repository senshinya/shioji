package store_test

import (
	"encoding/json"
	"fmt"
	"slices"
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

func TestHistoryRoundsKeepEventsReceivedBeforeGoalMarker(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "goal-prefix", WorkspaceID: w.ID, Text: "start", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []domain.Item{
		{ID: "older-reply", TurnID: "initial", Kind: "assistant", Text: "Initial reply", Final: true},
		{ID: "first-context", TurnID: "first", Kind: "activity", Presentation: "context", Final: true},
		{ID: "first-goal", TurnID: "first", Kind: "activity", Presentation: domain.GoalContinuationPresentation, Final: true},
		{ID: "first-reply", TurnID: "first", Kind: "assistant", Text: "First continuation", Final: true},
		{ID: "late-initial", TurnID: "initial", Kind: "tool", Final: true},
		{ID: "second-compaction", TurnID: "second", Kind: "activity", Presentation: "compaction", Final: true},
		{ID: "late-first", TurnID: "first", Kind: "tool", Final: true},
		{ID: "second-context", TurnID: "second", Kind: "activity", Presentation: "context", Final: true},
		{ID: "second-goal", TurnID: "second", Kind: "activity", Presentation: domain.GoalContinuationPresentation, Final: true},
		{ID: "second-reply", TurnID: "second", Kind: "assistant", Text: "Second continuation", Final: true},
	} {
		if _, err := s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(q domain.HistoryQuery, expected ...string) domain.SessionDetail {
		t.Helper()
		q.SessionID, q.Rounds = op.SessionID, 1
		d, err := s.SessionHistory(q)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, item := range d.Items {
			ids = append(ids, item.ID)
		}
		if !slices.Equal(ids, expected) {
			t.Errorf("%+v: got %v, want %v", q, ids, expected)
		}
		return d
	}
	second := []string{"second-compaction", "late-first", "second-context", "second-goal", "second-reply"}
	first := []string{"first-context", "first-goal", "first-reply", "late-initial"}
	latest := read(domain.HistoryQuery{}, second...)
	older := read(domain.HistoryQuery{Before: latest.History.First}, first...)
	read(domain.HistoryQuery{After: older.History.Last}, second...)
	read(domain.HistoryQuery{AroundID: "first-context"}, first...)
	for _, target := range second {
		read(domain.HistoryQuery{AroundID: target}, second...)
	}
	if len(latest.Items) == len(second) {
		prefixChunk := read(domain.HistoryQuery{After: latest.Items[0].Order}, second[1:]...)
		if prefixChunk.History.ContextID != "" {
			t.Errorf("partial prefix received context from a different round: %s", prefixChunk.History.ContextID)
		}
		replyChunk := read(domain.HistoryQuery{After: latest.Items[3].Order}, "second-goal", "second-reply")
		if replyChunk.History.ContextID != "second-goal" {
			t.Errorf("partial reply lost its continuation marker: %s", replyChunk.History.ContextID)
		}
	}
}

func TestHistoryRoundBodiesContinueByBytesWithoutAnItemLimit(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "large-round", WorkspaceID: w.ID, Text: "one round", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	work := domain.WorkActivity{ID: "background", Kind: "background", TurnID: "turn", Title: "check", State: "running"}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
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
		if pages == 0 {
			if len(d.Works) != 1 || d.Works[0].ID != work.ID {
				t.Fatalf("first chunk omitted its work roster: %+v", d.Works)
			}
		} else if len(d.Works) != 0 || d.WorksTruncated {
			t.Fatalf("continuation chunk repeated the first chunk's work roster: %+v", d.Works)
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
