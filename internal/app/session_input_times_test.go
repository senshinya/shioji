package app

import (
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestInputDateUpdatesFromMainConversationEvent(t *testing.T) {
	now := time.Now()
	previous := now.Add(-time.Hour)
	m := NewModel()
	m.Snapshot = domain.Snapshot{DeviceID: "d", Sessions: []domain.Session{{ID: "s"}},
		SessionInputTimes: map[string]time.Time{"s": previous}}
	oldSnapshot := m.Snapshot
	for i, item := range []domain.Item{
		{ID: "live", Kind: "user", SentAt: &now},
		{ID: "late", Kind: "user", SentAt: new(now.Add(-time.Minute))},
		{ID: "queued", Kind: "queued", SentAt: new(now.Add(time.Minute))},
		{ID: "child", Kind: "user", WorkID: "child", SentAt: new(now.Add(time.Minute))},
	} {
		event := domain.Event{Type: "item.upsert", SessionID: "s", Sequence: int64(i + 1), Item: &item}
		if err := m.ApplyEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if !m.Snapshot.SessionInputTimes["s"].Equal(now) || !oldSnapshot.SessionInputTimes["s"].Equal(previous) {
		t.Fatalf("session dates: current %v, prior %v", m.Snapshot.SessionInputTimes, oldSnapshot.SessionInputTimes)
	}
}
