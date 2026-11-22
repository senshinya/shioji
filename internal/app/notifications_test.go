package app

import (
	"shioji/internal/domain"
	"testing"
)

func TestCompactionDoesNotNotifyAsAnAgentReply(t *testing.T) {
	m := NewModel()
	m.Preferences.Notifications.Enabled = true
	m.ApplySnapshot(domain.Snapshot{Sessions: []domain.Session{{ID: "s", Title: "keep"}}})
	for i, status := range []domain.ExecutionState{"completed", "failed"} {
		m.ApplyEvent(domain.Event{Sequence: int64(i + 1), SessionID: "s", Type: "turn", Turn: &domain.Turn{ID: string(status), Status: status, Presentation: "compaction"}})
	}
	if len(m.Notifications) != 0 {
		t.Fatal("context maintenance sent a reply notification", m.Notifications)
	}
}

func TestNotificationsUseNewConfirmedEventsNotSnapshotsOrLateUsage(t *testing.T) {
	m := NewModel()
	m.Preferences.Notifications.Enabled = true
	m.ApplySnapshot(domain.Snapshot{DeviceID: "d", Sessions: []domain.Session{{ID: "s", WorkspaceID: "w", ThreadID: "thread", Title: "A task", Status: "running"}}})
	if len(m.Notifications) != 0 {
		t.Fatal("history snapshot produced notifications")
	}
	m.Details["s"] = domain.SessionDetail{Turns: []domain.Turn{{ID: "t", Status: "running"}}}
	completed := domain.Event{Sequence: 1, SessionID: "s", Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}}
	if err := m.ApplyEvent(completed); err != nil {
		t.Fatal(err)
	}
	// A background conversation may have been evicted from the reading cache.
	delete(m.Details, "s")
	completed.Sequence = 2
	if err := m.ApplyEvent(completed); err != nil {
		t.Fatal(err)
	}
	if len(m.Notifications) != 1 || m.Notifications[0].Kind != "completed" || m.Notifications[0].SessionID != "s" {
		t.Fatal("completion notification missing or repeated", m.Notifications)
	}
	req := domain.PendingRequest{ID: "r", SessionID: "s", WorkID: "child", State: "pending", Kind: "questions", Revision: 1}
	if err := m.ApplyEvent(domain.Event{Sequence: 3, SessionID: "s", Type: "request.open", Request: &req}); err != nil {
		t.Fatal(err)
	}
	req.State = "resolved"
	if err := m.ApplyEvent(domain.Event{Sequence: 4, SessionID: "s", Type: "request.resolve", Request: &req}); err != nil {
		t.Fatal(err)
	}
	if len(m.Notifications) != 2 || m.Notifications[1].Kind != "attention" {
		t.Fatal("request lifecycle was not distinguished", m.Notifications)
	}
	if notice := m.Notifications[1]; notice.SessionLocation != (SessionLocation{DeviceID: "d", WorkspaceID: "w", SessionID: "s"}) ||
		notice.ThreadID != "thread" || notice.RequestID != req.ID || notice.WorkID != req.WorkID || notice.RequestRevision != req.Revision {
		t.Fatalf("request notification lost its exact target: %+v", notice)
	}
	if err := m.ApplyEvent(domain.Event{Sequence: 5, SessionID: "s", Type: "turn", Turn: &domain.Turn{ID: "t2", Status: "interrupted"}}); err != nil {
		t.Fatal(err)
	}
	if len(m.Notifications) != 2 {
		t.Fatal("user stop was reported as completion or failure")
	}
}

func TestDisabledNotificationKindsDoNotEnterDeliveryQueue(t *testing.T) {
	m := NewModel()
	m.Preferences.Notifications.Enabled = true
	m.Preferences.Notifications.Completed = false
	m.ApplySnapshot(domain.Snapshot{Sessions: []domain.Session{{ID: "s", WorkspaceID: "w", Title: "A task"}}})
	m.ApplyEvent(domain.Event{Sequence: 1, SessionID: "s", Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
	if len(m.Notifications) != 0 {
		t.Fatal("disabled completion notification was queued")
	}
	m.ApplyEvent(domain.Event{Sequence: 2, SessionID: "s", Type: "turn", Turn: &domain.Turn{ID: "t2", Status: "failed", Error: "private output"}})
	if len(m.Notifications) != 1 || m.Notifications[0].Kind != "failed" || m.Notifications[0].Body != "A task" {
		t.Fatal("failure did not use private-safe session context", m.Notifications)
	}
}
