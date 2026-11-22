package store_test

import (
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestSnapshotUsesActualInputTimeAcrossOffsetsAndItemOrder(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "date-order", WorkspaceID: w.ID, Text: "initial", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Now().UTC().Truncate(time.Millisecond).Add(2 * time.Hour)
	local := want.In(time.FixedZone("worker-west", -7*60*60))
	laterInList := want.Add(-time.Minute).In(time.FixedZone("worker-east", 8*60*60))
	for _, item := range []domain.Item{
		{ID: "preferred-date", Kind: "user", SentAt: &local},
		{ID: "older-date-in-a-later-row", Kind: "user", SentAt: &laterInList},
		{ID: "queue-date", Kind: "queued", SentAt: new(want.Add(time.Minute))},
		{ID: "reply-date", Kind: "assistant", SentAt: new(want.Add(time.Hour))},
	} {
		if _, err := s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.SessionInputTimes[op.SessionID]; !got.Equal(want) {
		t.Fatalf("last input = %v, want %v", got, want)
	}
}
