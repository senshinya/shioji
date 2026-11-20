package app

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"shioji/internal/domain"
)

func TestHistoryRoundPreferenceDefaultsAndSurvivesRestart(t *testing.T) {
	m := NewModel()
	if m.Preferences.HistoryPageRounds != 20 {
		t.Fatal(m.Preferences.HistoryPageRounds)
	}
	m.Preferences.HistoryPageRounds = 50
	path := filepath.Join(t.TempDir(), "state.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	next := NewModel()
	if err := next.Load(path); err != nil {
		t.Fatal(err)
	}
	if next.Preferences.HistoryPageRounds != 50 {
		t.Fatal("history round choice was not persisted", next.Preferences)
	}
	for _, value := range []int{-1, 0, 101} {
		p := ClientPreferences{HistoryPageRounds: value}.Normalized()
		if p.HistoryPageRounds != 20 {
			t.Fatal("invalid round preference", p.HistoryPageRounds)
		}
	}
}

func TestHistoryRoundReaderCombinesByteChunksAndRejectsChangedSnapshots(t *testing.T) {
	for _, bad := range []string{"", "sequence", "source", "progress", "cancel"} {
		t.Run(bad, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			out, err := readRoundHistory(ctx, domain.HistoryQuery{SessionID: "s", Rounds: 20, AroundID: "target"}, func(ctx context.Context, q domain.HistoryQuery) (domain.SessionDetail, error) {
				calls++
				first, last := int64(1), int64(2)
				if calls > 1 {
					first, last = q.After+1, min(q.After+2, 5)
					if q.WindowFirst != 1 || q.WindowLast != 5 || q.AroundID != "" || q.ThreadID != "source" {
						t.Fatal("invalid continuation", q)
					}
				}
				d := domain.SessionDetail{Session: domain.Session{ID: "s", ThreadID: "source"}, Sequence: 10, History: &domain.HistoryWindow{First: first, Last: last, WindowFirst: 1, WindowLast: 5, Older: true, Groups: []domain.HistoryToolGroup{{ID: "group", First: 2, Last: 5, Count: 4}}}}
				d.Requests = []domain.PendingRequest{{ID: "z", State: domain.RequestPending}, {ID: "a", State: domain.RequestPending}}
				if calls == 1 {
					d.Turns = []domain.Turn{{ID: "first", Order: 1}, {ID: "active", Order: 3, Status: domain.ExecutionRunning}}
				} else {
					d.Requests = append(d.Requests, domain.PendingRequest{ID: fmt.Sprint("resolved-", calls), State: domain.RequestResolved})
					d.Turns = []domain.Turn{{ID: "middle", Order: 2}, {ID: "active", Order: 3, Status: domain.ExecutionRunning}}
				}
				for i := first; i <= last; i++ {
					d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Order: i, Kind: "tool"})
				}
				if calls > 1 {
					d.Items = append([]domain.Item{{ID: "1", Order: 1, Kind: "user"}}, d.Items...)
					d.History.ContextID = "1"
				}
				if bad == "cancel" {
					cancel()
				}
				if calls == 2 {
					switch bad {
					case "sequence":
						d.Sequence++
					case "source":
						d.Session.ThreadID = "other"
					case "progress":
						d.History.Last = q.After
					}
				}
				return d, nil
			})
			if bad != "" {
				if err == nil {
					t.Fatal("accepted invalid round continuation", bad, out)
				}
				return
			}
			if err != nil || calls != 3 || len(out.Items) != 5 || out.Items[0].Order != 1 || out.Items[4].Order != 5 || out.History.First != 1 || out.History.Last != 5 || !out.History.Older || out.History.Newer || len(out.History.Groups) != 1 || out.History.Groups[0].Count != 4 {
				t.Fatal("round window was not reassembled", out, calls, err)
			}
			if len(out.Requests) != 4 || out.Requests[0].ID != "z" || out.Requests[1].ID != "a" || len(out.Turns) != 3 || out.Turns[0].ID != "first" || out.Turns[1].ID != "middle" || out.Turns[2].ID != "active" {
				t.Fatal("round metadata was dropped or sorted by opaque IDs", out.Requests, out.Turns)
			}
		})
	}
}

func TestHistoryOpeningCacheTrimsAtUserMessageBoundaries(t *testing.T) {
	d := domain.SessionDetail{}
	for round := 0; round < 4; round++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("u", round), Kind: "user", Order: int64(len(d.Items) + 1)})
		for i := 0; i < 110; i++ {
			d.Items = append(d.Items, domain.Item{ID: fmt.Sprintf("%d-%d", round, i), Kind: "tool", Order: int64(len(d.Items) + 1)})
		}
	}
	trimmed := historyOpeningTail(d, 2)
	if len(trimmed.Items) != 222 || trimmed.Items[0].ID != "u2" || !trimmed.History.Older {
		t.Fatal("cache still clips by message count", len(trimmed.Items), trimmed.History)
	}
}

func TestHistoryOpeningCacheTrimsAtGoalContinuationBoundaries(t *testing.T) {
	d := domain.SessionDetail{Items: []domain.Item{
		{ID: "prompt", Kind: "user", Order: 1},
		{ID: "first-goal", Kind: "activity", Presentation: domain.GoalContinuationPresentation, Order: 2},
		{ID: "first-reply", Kind: "assistant", Order: 3},
		{ID: "second-goal", Kind: "activity", Presentation: domain.GoalContinuationPresentation, Order: 4},
		{ID: "second-reply", Kind: "assistant", Order: 5},
		{ID: "third-goal", Kind: "activity", Presentation: domain.GoalContinuationPresentation, Order: 6},
		{ID: "compaction", Kind: "activity", Presentation: "compaction", Order: 7},
		{ID: "third-reply", Kind: "assistant", Order: 8},
	}}
	trimmed := historyOpeningTail(d, 2)
	if len(trimmed.Items) != 5 || trimmed.Items[0].ID != "second-goal" || trimmed.History.First != 4 || trimmed.History.Last != 8 || !trimmed.History.Older {
		t.Fatalf("cache did not keep the two latest Goal rounds: %+v (%+v)", trimmed.Items, trimmed.History)
	}
}
