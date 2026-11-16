package app

import (
	"context"
	"fmt"
	"testing"

	"shioji/internal/domain"
)

// The initial read's cache provenance must survive events that fill Items.
// Testing the queued controller callback also covers the normal session-entry
// path, rather than only the explicit paging/retry constructor.
func TestInitialHistoryRecordsCacheBeforeLiveEvents(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprint(cached), func(t *testing.T) {
			m := NewModel()
			m.ConnectionEpoch = "epoch"
			d := domain.SessionDetail{Session: domain.Session{ID: "s", ThreadID: "thread"}}
			m.Snapshot.Sessions = []domain.Session{d.Session}
			if cached {
				d.Items = []domain.Item{{ID: "cached", Kind: "assistant", Text: "已读取的历史", Order: 1}}
				d.History = &domain.HistoryWindow{First: 1, Last: 1}
			}
			m.Details["s"] = d
			c := NewController("")
			cancel := c.queueInitialHistory(context.Background(), "s", "epoch", "generation", true)
			defer cancel()
			select {
			case update := <-c.Updates:
				update(m)
			default:
				t.Fatal("initial history did not enqueue its model update")
			}
			if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "live", Kind: "assistant", Text: "实时回复", Order: 2}}); err != nil {
				t.Fatal(err)
			}
			if len(m.Details["s"].Items) == 0 {
				t.Fatal("live event was not projected while history was pending")
			}
			read := m.HistoryReads["s"]
			if read == nil || read.Uncached == cached {
				t.Fatalf("live event changed initial cache provenance: cached=%t read=%+v", cached, read)
			}
			read.Pending = false
			read.Error = "读取失败"
			retry := m.BeginHistoryRead("s")
			if retry.Uncached == cached {
				t.Fatal("retry mistook the event-only tail for cached history")
			}
		})
	}
}
