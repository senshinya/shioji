package app

import (
	"shioji/internal/domain"
	"testing"
)

func TestHistoryWindowIgnoresUnloadedOldItemsButFollowsNewOutput(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{Session: domain.Session{ID: "s"}, History: &domain.HistoryWindow{First: 100, Last: 110, Older: true}, Items: []domain.Item{{ID: "tail", Order: 110}}}
	for seq, order := range []int64{50, 111} {
		if e := m.ApplyEvent(domain.Event{Sequence: int64(seq + 1), Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "old-or-new", Order: order}}); e != nil {
			t.Fatal(e)
		}
		if order == 50 && len(m.Details["s"].Items) != 1 {
			t.Fatal("old unloaded output was inserted into the latest window")
		}
	}
	if len(m.Details["s"].Items) != 2 || m.Details["s"].History.Last != 111 {
		t.Fatal("latest window did not follow new output")
	}
}

func TestHistoryWindowInvalidatedByHistoryReset(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{History: &domain.HistoryWindow{First: 100, Last: 110, Older: true}}
	zero := int64(0)
	if e := m.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &zero}); e != nil {
		t.Fatal(e)
	}
	if !m.Details["s"].History.Invalidated {
		t.Fatal("history replacement left an old paging cursor valid")
	}
}

func TestOlderHistoryMergeBridgesEventsAndDoesNotMoveTheJournalCursor(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{Session: domain.Session{ID: "s"}, History: &domain.HistoryWindow{First: 100, Last: 110, Older: true}, Items: []domain.Item{{ID: "tail", Order: 110}}}
	r := m.BeginHistoryRead("s")
	if e := m.ApplyEvent(domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "older", Order: 50, Text: "new version", Final: true}}); e != nil {
		t.Fatal(e)
	}
	page := domain.SessionDetail{Session: domain.Session{ID: "s"}, History: &domain.HistoryWindow{First: 40, Last: 98, Older: true, Newer: true}, Items: []domain.Item{{ID: "older", Order: 50, Text: "stale version"}}}
	if e := m.MergeHistoryPage(page, r, false, domain.HistoryQuery{SessionID: "s", Before: 100}); e != nil {
		t.Fatal(e)
	}
	if m.Sequence != 1 || len(m.Details["s"].Items) != 2 || m.Details["s"].Items[0].Text != "new version" || !m.Details["s"].Items[0].Final {
		t.Fatal("page lost a concurrent event or advanced the global cursor", m.Details["s"])
	}
}

func TestHistoryResetCannotBeResurrectedByAnOutstandingPage(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{Session: domain.Session{ID: "s"}, History: &domain.HistoryWindow{First: 100, Last: 110, Older: true}}
	r := m.BeginHistoryRead("s")
	zero := int64(0)
	m.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &zero})
	page := domain.SessionDetail{Session: domain.Session{ID: "s"}, History: &domain.HistoryWindow{First: 1, Last: 99}, Items: []domain.Item{{ID: "deleted", Order: 5}}}
	if e := m.MergeHistoryPage(page, r, false, domain.HistoryQuery{Before: 100}); e == nil {
		t.Fatal("stale page resurrected replaced history")
	}
	if len(m.Details["s"].Items) != 0 {
		t.Fatal("history reset was undone")
	}
}

func TestHistoryGroupFollowsCallsAcrossSpacersAndCompletion(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{Session: domain.Session{ID: "s"}, History: &domain.HistoryWindow{First: 100, Last: 100, Groups: []domain.HistoryToolGroup{{ID: "first", TurnID: "t", First: 2, Last: 100, Count: 99}}}, Items: []domain.Item{{ID: "last", Kind: "tool", TurnID: "t", Order: 100}}}
	events := []domain.Item{{ID: "spacer", Kind: "assistant", TurnID: "t", Order: 101, Text: "\n"}, {ID: "new", Kind: "tool", TurnID: "t", Order: 102}, {ID: "new", Kind: "tool", TurnID: "t", Order: 102, Final: true, Status: "failed"}}
	for i, item := range events {
		if err := m.ApplyEvent(domain.Event{Sequence: int64(i + 1), SessionID: "s", Type: "item.upsert", Item: &item}); err != nil {
			t.Fatal(err)
		}
	}
	g := m.Details["s"].History.Groups[0]
	if g.Count != 100 || g.Last != 102 || g.Issues != 1 {
		t.Fatal("group summary lost live updates", g)
	}
}

func TestHistoryReadCannotRevertAnAcknowledgedQueueEdit(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{Session: domain.Session{ID: "s", Revision: 1}, Queue: domain.QueueState{SessionID: "s", Revision: 2}}
	r := m.BeginHistoryRead("s")
	page := domain.SessionDetail{Session: domain.Session{ID: "s", Revision: 1}, History: &domain.HistoryWindow{}, Queue: domain.QueueState{SessionID: "s", Revision: 1}}
	if err := m.MergeHistoryPage(page, r, true, domain.HistoryQuery{}); err != nil {
		t.Fatal(err)
	}
	if m.Details["s"].Queue.Revision != 2 {
		t.Fatal("history reverted a newer queue acknowledgement")
	}
}

func TestHistoryQueueFreshnessIsIndependentOfSessionFreshness(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{Session: domain.Session{ID: "s", Revision: 2}, Queue: domain.QueueState{SessionID: "s", Revision: 1}}
	r := m.BeginHistoryRead("s")
	page := domain.SessionDetail{Session: domain.Session{ID: "s", Revision: 1}, History: &domain.HistoryWindow{}, Queue: domain.QueueState{SessionID: "s", Revision: 2}}
	if err := m.MergeHistoryPage(page, r, true, domain.HistoryQuery{}); err != nil {
		t.Fatal(err)
	}
	if got := m.Details["s"]; got.Queue.Revision != 2 || got.Session.Revision != 2 {
		t.Fatal("newer session discarded an independently newer queue", got)
	}
}
