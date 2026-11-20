package store

import (
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestSessionReadsCommittedProjectionWithoutWaitingForWriter(t *testing.T) {
	s, _, session := archiveFixture(t)
	if _, err := s.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "committed", Kind: "assistant", Text: "saved reply"}}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	tx, err := s.db.Begin()
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE sessions SET body=json_set(body,'$.title','uncommitted') WHERE id=?", session.ID); err == nil {
		_, err = tx.Exec("UPDATE items SET body=json_set(body,'$.text','uncommitted') WHERE session_id=? AND id='committed'", session.ID)
	}
	if err != nil {
		tx.Rollback()
		s.mu.Unlock()
		t.Fatal(err)
	}
	type result struct {
		detail domain.SessionDetail
		err    error
	}
	done := make(chan result, 1)
	go func() {
		detail, err := s.Session(session.ID, 0, 100)
		done <- result{detail, err}
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
		t.Fatal("conversation projection waited for the execution writer")
	}
	if got.err != nil || got.detail.Session.Title != session.Title {
		t.Fatalf("session did not preserve committed metadata: %+v, %v", got.detail.Session, got.err)
	}
	for _, item := range got.detail.Items {
		if item.ID == "committed" && item.Text == "saved reply" {
			return
		}
	}
	t.Fatalf("session did not preserve the committed reply: %+v", got.detail.Items)
}
