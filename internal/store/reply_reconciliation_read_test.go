package store

import (
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestPendingRepliesReadsCommittedHistoryWithoutWaitingForWriter(t *testing.T) {
	s, _, session := archiveFixture(t)
	if _, err := s.db.Exec("UPDATE sessions SET body=json_set(body,'$.threadId','source-thread') WHERE id=?", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "pending", TurnID: "t", Kind: "assistant", Text: "partial"}}); err != nil {
		t.Fatal(err)
	}

	// An unrelated write keeps its transaction and admission mutex until commit.
	// Recovery discovery must read the last committed WAL snapshot independently.
	s.mu.Lock()
	tx, err := s.db.Begin()
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE sessions SET body=json_set(body,'$.title','uncommitted') WHERE id=?", session.ID); err != nil {
		tx.Rollback()
		s.mu.Unlock()
		t.Fatal(err)
	}
	type result struct {
		replies []ReplyReconciliation
		err     error
	}
	done := make(chan result, 1)
	go func() {
		replies, err := s.PendingReplies()
		done <- result{replies, err}
	}()
	var got result
	blocked := false
	select {
	case got = <-done:
	case <-time.After(time.Second):
		blocked = true
	}
	tx.Rollback()
	s.mu.Unlock()
	if blocked {
		<-done
		t.Fatal("background reply discovery waited for the execution writer")
	}
	if got.err != nil || len(got.replies) != 1 || got.replies[0].Session.Title != session.Title || len(got.replies[0].Items) != 1 || got.replies[0].Items[0].ID != "pending" {
		t.Fatalf("discovery did not preserve the committed session and item: %+v, %v", got.replies, got.err)
	}
}

func TestPendingRepliesKeepsOnlyProvisionalRepliesOfCompletedTurnsInOrder(t *testing.T) {
	s, _, session := archiveFixture(t)
	if _, err := s.db.Exec("UPDATE sessions SET body=json_set(body,'$.threadId','source-thread') WHERE id=?", session.ID); err != nil {
		t.Fatal(err)
	}
	for _, turn := range []domain.Turn{{ID: "failed", Status: "failed"}, {ID: "running", Status: "running"}} {
		if _, err := s.Apply(session.ID, domain.Event{Type: "turn", Turn: &turn}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []domain.Item{
		{ID: "first", TurnID: "t", Kind: "assistant"},
		{ID: "final", TurnID: "t", Kind: "assistant", Final: true},
		{ID: "failed-reply", TurnID: "failed", Kind: "assistant"},
		{ID: "running-reply", TurnID: "running", Kind: "assistant"},
		{ID: "tool", TurnID: "t", Kind: "tool"},
		{ID: "last", TurnID: "t", Kind: "assistant"},
	} {
		if _, err := s.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
			t.Fatal(err)
		}
	}
	replies, err := s.PendingReplies()
	if err != nil || len(replies) != 1 || len(replies[0].Items) != 2 || replies[0].Items[0].ID != "first" || replies[0].Items[1].ID != "last" {
		t.Fatalf("wrong recovery candidates or transcript order: %+v, %v", replies, err)
	}
}
