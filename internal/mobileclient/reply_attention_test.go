package mobileclient

import (
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestDeviceRepliesExcludeHistoricalAndIneligibleCompletions(t *testing.T) {
	cutoff := time.Date(2026, time.November, 20, 6, 0, 0, 0, time.UTC)
	session := func(id string) domain.Session {
		return domain.Session{ID: id, ThreadID: "thread", WorkspaceID: "project", Status: domain.ExecutionCompleted,
			Completion: &domain.ReplyCompletion{ID: id + "-receipt", HistoryRef: "thread", Unread: true, CompletedAt: cutoff.Add(time.Second)}}
	}
	live, old, pending, closed, replaced, needsAnswer, archived := session("live"), session("old"), session("pending"),
		session("closed"), session("replaced"), session("request"), session("archived")
	old.Completion.CompletedAt = cutoff.Add(-time.Second)
	pending.Completion.Pending = true
	closed.Completion.Unread = false
	replaced.ThreadID = "new-history"
	archived.WorkspaceID = "archive"
	snapshot := domain.Snapshot{DeviceID: "owner",
		Workspaces:      []domain.Workspace{{ID: "project", DeviceID: "owner"}, {ID: "archive", DeviceID: "owner", Archived: true}},
		Sessions:        []domain.Session{live, old, pending, closed, replaced, needsAnswer, archived},
		PendingRequests: []domain.PendingRequest{{ID: "q", SessionID: needsAnswer.ID, State: domain.RequestPending}},
	}
	replies := DeviceReplies(snapshot, "peer:22", func(s domain.Session) bool { return UnreadReplySince(s, cutoff) })
	if len(replies) != 1 || replies[0].Session.ID != live.ID || replies[0].CompletionID != live.Completion.ID {
		t.Fatalf("unexpected attention entries: %+v", replies)
	}
}
