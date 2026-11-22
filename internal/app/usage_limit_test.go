package app

import (
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestUsageLimitStaysUntilRetryStarts(t *testing.T) {
	m := model()
	session := domain.Session{ID: "limited", WorkspaceID: "w", Status: domain.ExecutionFailed}
	m.Snapshot.Sessions = []domain.Session{session}
	m.SessionID = session.ID
	limit := domain.Turn{ID: "limited-turn", Order: 2, Status: domain.ExecutionFailed, UsageLimit: &domain.UsageLimit{}}
	m.Details[session.ID] = domain.SessionDetail{Session: session, Turns: []domain.Turn{limit}}
	m.Drafts[m.DraftKey()] = "retry manually"
	now := time.Now()

	if got := m.UsageLimitForSession(session.ID, now); got.ID != limit.ID {
		t.Fatalf("missing notice for latest limit: %+v", got)
	}
	if got := m.UsageLimitForSession("another-session", now); got.ID != "" {
		t.Fatal("limit leaked to another composer")
	}
	submission, _, _, err := m.BeginMessage(false)
	if err != nil || submission.Text != "retry manually" || m.Drafts[m.DraftKey()] != submission.Text {
		t.Fatalf("manual retry did not preserve draft before confirmation: %v", err)
	}
	detail := m.Details[session.ID]
	detail.Session.Status = domain.ExecutionStarting
	m.Details[session.ID] = detail
	if got := m.UsageLimitForSession(session.ID, now); got.ID != limit.ID {
		t.Fatal("attempting to send hid the notice before the next turn")
	}
	detail.Turns = []domain.Turn{{ID: "later-turn", Order: 3, Status: domain.ExecutionRunning}, limit}
	m.Details[session.ID] = detail
	if got := m.UsageLimitForSession(session.ID, now); got.ID != "" {
		t.Fatal("the latest turn started but an older failure remained visible")
	}
	detail.Turns[0].UsageLimit = &domain.UsageLimit{ResetAt: &now}
	detail.Turns[0].Status = domain.ExecutionFailed
	m.Details[session.ID] = detail
	if m.UsageLimitForSession(session.ID, now.Add(-time.Second)).ID != "later-turn" ||
		m.UsageLimitForSession(session.ID, now).ID != "" {
		t.Fatal("the subsequent limit did not appear and expire at its own reset")
	}
}
