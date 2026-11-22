package store

import (
	"testing"

	"shioji/internal/domain"
)

func TestAsyncQuestionHistoryRestoresOnlyMatchingItems(t *testing.T) {
	s, _, session := archiveFixture(t)
	if _, err := s.db.Exec("UPDATE sessions SET body=json_set(body, '$.threadId', ?) WHERE id=?", "thread", session.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Session(session.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	session = detail.Session
	old := domain.Item{ID: "question", TurnID: "turn", Kind: "assistant", Text: "When?\n- Now", Final: true}
	if _, err := s.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &old}); err != nil {
		t.Fatal(err)
	}
	source := old
	source.Questions = []domain.Question{{ID: "opaque", Text: "When?", Options: []domain.Option{{Label: "Now"}}}}
	missing := source
	missing.ID = "removed-by-history-replacement"
	history := domain.RecoveryHistory{Available: true, Items: []domain.Item{source, missing}}
	stale := session
	stale.ThreadID = "old-thread"
	if err := s.RestoreTranscriptQuestions(stale, history); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Question(session.ID, source.ID, "opaque"); err == nil {
		t.Fatal("outdated thread changed the transcript")
	}
	if err := s.RestoreTranscriptQuestions(session, history); err != nil {
		t.Fatal(err)
	}
	if q, err := s.Question(session.ID, source.ID, "opaque"); err != nil || q.Text != "When?" {
		t.Fatalf("old question was not restored: %+v %v", q, err)
	}
	if _, err := s.Question(session.ID, missing.ID, "opaque"); err == nil {
		t.Fatal("historical hydration resurrected a deleted message")
	}
}
