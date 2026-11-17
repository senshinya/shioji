package app

import (
	"encoding/json"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestDeferredCompletionNotifiesOnlyOnPublication(t *testing.T) {
	for _, outcome := range []string{"completed", "interrupted"} {
		t.Run(outcome, func(t *testing.T) {
			m := NewModel()
			m.WorkerMinor = 47
			m.Preferences.Notifications.Enabled = true
			m.ApplySnapshot(domain.Snapshot{Sessions: []domain.Session{{ID: "s", Title: "task"}}})
			var pending domain.ReplyCompletion
			if err := json.Unmarshal([]byte(`{"pending":true,"turnId":"t","order":1}`), &pending); err != nil {
				t.Fatal(err)
			}
			session := domain.Session{ID: "s", Title: "task", Revision: 1, Completion: &pending}
			if err := m.ApplyEvent(domain.Event{Sequence: 1, SessionID: "s", Type: "turn", Session: &session, Turn: &domain.Turn{ID: "t", Order: 1, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			if len(m.Notifications) != 0 || m.HasCompletedReply("s") {
				t.Fatal("parent completion notified before native work settled", m.Notifications)
			}
			var published domain.ReplyCompletion
			raw, _ := json.Marshal(map[string]any{"id": "publication", "turnId": "t", "order": 1, "unread": true, "completedAt": time.Now().UTC(), "outcome": outcome})
			json.Unmarshal(raw, &published)
			session.Completion = &published
			session.Revision++
			for i := int64(2); i <= 3; i++ {
				if err := m.ApplyEvent(domain.Event{Sequence: i, SessionID: "s", Type: "work.upsert", Session: &session, Work: &domain.WorkActivity{ID: "child", State: "completed"}}); err != nil {
					t.Fatal(err)
				}
				delete(m.Details, "s")
			}
			expected := "completed"
			if outcome == "interrupted" {
				expected = "failed"
			}
			if len(m.Notifications) != 1 || m.Notifications[0].Kind != expected || !m.HasCompletedReply("s") {
				t.Fatal("settlement publication missing, duplicated or reported as success", m.Notifications)
			}
		})
	}
}
