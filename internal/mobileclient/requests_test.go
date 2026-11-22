package mobileclient

import (
	"slices"
	"testing"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestDeviceRequestsUsesOnlyItsOwnPendingSessions(t *testing.T) {
	catalog := domain.Snapshot{
		DeviceID: "own",
		Workspaces: []domain.Workspace{
			{ID: "project", DeviceID: "own", Name: "Project"},
			{ID: "foreign", DeviceID: "other"},
		},
		Sessions: []domain.Session{
			{ID: "valid", WorkspaceID: "project", Title: "Session", ThreadID: "live"},
			{ID: "wrong-context", WorkspaceID: "project", Context: domain.LaunchContext{DeviceID: "other"}},
			{ID: "wrong-project", WorkspaceID: "foreign"},
		},
		PendingRequests: []domain.PendingRequest{
			{ID: "approve", SessionID: "wrong-context", State: domain.RequestPending},
			{ID: "approve", SessionID: "wrong-project", State: domain.RequestPending},
			{ID: "approve", SessionID: "valid", State: domain.RequestPending, WorkID: "child", Revision: 7},
			{ID: "answered", SessionID: "valid", State: domain.RequestAnswered},
			{ID: "gone", SessionID: "missing", State: domain.RequestPending},
		},
	}
	requests := DeviceRequests(catalog, "peer:22")
	if len(requests) != 1 {
		t.Fatalf("wrong request scope: %+v", requests)
	}
	found := requests[0]
	if found.DeviceID != "own" || found.Address != "peer:22" || found.WorkspaceID != "project" || found.ThreadID != "live" ||
		found.Request.SessionID != "valid" || found.Request.WorkID != "child" || found.Request.Revision != 7 {
		t.Fatalf("wrong navigation target: %+v", found)
	}
}

func TestLivePendingRequestsFollowsResolutionAndNewOpenings(t *testing.T) {
	m := app.NewModel()
	file := domain.PendingRequest{ID: "approval", SessionID: "parent", WorkID: "child", State: domain.RequestPending, Revision: 7}
	other := domain.PendingRequest{ID: file.ID, SessionID: "other", State: domain.RequestPending, Revision: 2}
	m.ApplySnapshot(domain.Snapshot{
		Sessions:        []domain.Session{{ID: file.SessionID}, {ID: other.SessionID}},
		PendingRequests: []domain.PendingRequest{file, other},
	})
	file.State, file.Revision = domain.RequestAnswered, 8
	question := domain.PendingRequest{ID: "question", SessionID: file.SessionID, WorkID: file.WorkID, State: domain.RequestPending, Revision: 1}
	for _, event := range []domain.Event{
		{Type: "request.resolve", SessionID: file.SessionID, Sequence: 1, Request: &file},
		{Type: "request.open", SessionID: question.SessionID, Sequence: 2, Request: &question},
	} {
		if err := m.ApplyEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	m.PruneHistoryCache()
	pending := LivePendingRequests(m)
	got := make([]string, 0, len(pending))
	for _, r := range pending {
		got = append(got, r.SessionID+"/"+r.ID)
	}
	if want := []string{"other/approval", "parent/question"}; !slices.Equal(got, want) {
		t.Fatalf("pending requests = %v, want %v", got, want)
	}
}
