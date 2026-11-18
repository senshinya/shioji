package app

import (
	"context"
	"testing"

	"shioji/internal/domain"
)

func TestHistoryResetInvalidatesCachedAuthorityAcrossCanceledRefresh(t *testing.T) {
	m := NewModel()
	m.ConnectionEpoch = "epoch"
	s := domain.Session{ID: "s", ThreadID: "old-thread", WorkspaceID: "w", Revision: 1}
	m.Snapshot.Sessions = []domain.Session{s}
	d := domain.SessionDetail{Session: s, History: &domain.HistoryWindow{First: 101, Last: 101, Older: true},
		Turns: []domain.Turn{{ID: "retained", Order: 1}, {ID: "rewritten", Order: 2}},
		Items: []domain.Item{{ID: "old-tail", TurnID: "rewritten", Kind: "assistant", Order: 101}}}
	m.Details["s"] = d
	c := NewController("")
	stop := c.queueInitialHistory(context.Background(), "s", "epoch", "g", true)
	defer stop()
	(<-c.Updates)(m)
	job := <-c.Jobs
	before := m.Details["s"]
	if !before.HistoryLoaded || !before.History.Invalidated {
		t.Fatalf("refresh did not preserve the real cached window: %+v", before)
	}
	s.ThreadID, s.Revision = "replacement-thread", 2
	keep := int64(1)
	if err := m.ApplyEvent(domain.Event{Type: "history.reset", SessionID: "s", Sequence: 1, Session: &s, HistoryTurnOrder: &keep}); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyEvent(domain.Event{Type: "submission", SessionID: "s", Sequence: 2, Session: &s, Item: &domain.Item{ID: "new-question", Kind: "user", Order: 200, TurnID: "new-turn"}}); err != nil {
		t.Fatal(err)
	}
	stop()
	job.Apply(m, nil, context.Canceled)
	got := m.Details["s"]
	if len(got.Items) != 1 || got.Items[0].ID != "new-question" || got.Session.ThreadID != "replacement-thread" {
		t.Fatalf("history reset did not retain precisely the new input: %+v", got)
	}
	if got.HasLoadedHistory() {
		t.Error("cached authority survived the external reset into the new thread")
	}
}
