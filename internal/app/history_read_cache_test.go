package app

import (
	"context"
	"encoding/json"
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
			if read == nil || m.Details["s"].HasLoadedHistory() != cached {
				t.Fatalf("live event changed initial cache provenance: cached=%t read=%+v", cached, read)
			}
			read.Pending = false
			read.Error = "读取失败"
			m.BeginHistoryRead("s")
			if m.Details["s"].HasLoadedHistory() != cached {
				t.Fatal("retry mistook the event-only tail for cached history")
			}
		})
	}
}

func TestCanceledInitialHistoryDoesNotPromoteLiveTailToCache(t *testing.T) {
	m := NewModel()
	m.ConnectionEpoch = "epoch"
	session := domain.Session{ID: "s", ThreadID: "thread", Revision: 1}
	m.Snapshot.Sessions = []domain.Session{session}
	c := NewController("")
	open := func() (context.CancelFunc, Job) {
		cancel := c.queueInitialHistory(context.Background(), "s", "epoch", "generation", true)
		(<-c.Updates)(m)
		return cancel, <-c.Jobs
	}
	cancel, job := open()
	tail := domain.Item{ID: "tail", Kind: "assistant", Order: 100, Text: "实时消息"}
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Session: &session, Item: &tail}); err != nil {
		t.Fatal(err)
	}
	cancel()
	job.Apply(m, nil, context.Canceled)
	reopen, next := open()
	defer reopen()
	if m.Details["s"].HasLoadedHistory() {
		t.Error("an interrupted read turned a metadata-bearing live event into cached history")
	}
	page := domain.SessionDetail{Session: session, Sequence: 1, Items: []domain.Item{{ID: "prompt", Kind: "user", Order: 10}, tail}, History: &domain.HistoryWindow{First: 10, Last: 100}}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	next.Apply(m, body, nil)
	if got := m.Details["s"]; len(got.Items) != 2 || got.Items[0].ID != "prompt" || got.Items[1].ID != "tail" {
		t.Fatalf("the fresh history did not replace the incomplete projection: %+v", got.Items)
	}
}

func TestInitialHistoryDoesNotTreatPreexistingEventTailAsCache(t *testing.T) {
	m := NewModel()
	m.ConnectionEpoch = "epoch"
	m.Snapshot.Sessions = []domain.Session{{ID: "s", ThreadID: "thread"}}
	// Snapshot requests create metadata-only details for inactive sessions.
	m.Details["s"] = domain.SessionDetail{Requests: []domain.PendingRequest{{ID: "request", SessionID: "s", State: "pending"}}}
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "tail", Kind: "assistant", Text: "后台实时消息", Order: 100}}); err != nil {
		t.Fatal(err)
	}
	c := NewController("")
	cancel := c.queueInitialHistory(context.Background(), "s", "epoch", "generation", true)
	defer cancel()
	select {
	case update := <-c.Updates:
		update(m)
	default:
		t.Fatal("initial history did not enqueue its model update")
	}
	read := m.HistoryReads["s"]
	if read == nil || m.Details["s"].HasLoadedHistory() {
		t.Fatalf("a preexisting live tail became cached history: %+v", read)
	}
	if len(m.Details["s"].Items) != 1 || m.Details["s"].Items[0].ID != "tail" {
		t.Fatal("waiting for authoritative history discarded live output")
	}
}
