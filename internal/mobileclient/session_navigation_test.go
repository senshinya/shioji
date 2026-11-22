package mobileclient

import (
	"testing"

	"shioji/internal/domain"
)

func TestBrowseSessionCatalogScopesRowsAndRequestsToOwner(t *testing.T) {
	const device = "one"
	catalog := domain.Snapshot{
		DeviceID: device,
		Workspaces: []domain.Workspace{
			{ID: "w", DeviceID: device, Name: "test"},
			{ID: "old", DeviceID: device, Name: "archived", Archived: true},
			{ID: "foreign", DeviceID: "two"},
		},
		Sessions: []domain.Session{
			{ID: "s", WorkspaceID: "w", Context: domain.LaunchContext{DeviceID: device}},
			{ID: "legacy", WorkspaceID: "w"},
			{ID: "old-session", WorkspaceID: "old"},
			{ID: "archived-session", WorkspaceID: "w", Archived: true},
			{ID: "foreign-session", WorkspaceID: "foreign"},
			{ID: "foreign-context", WorkspaceID: "w", Context: domain.LaunchContext{DeviceID: "two"}},
			{ID: "no-workspace", WorkspaceID: "missing"},
		},
		PendingRequests: []domain.PendingRequest{
			{ID: "r1", SessionID: "s"},
			{ID: "r2", SessionID: "s", State: "submitting"},
			{ID: "r3", SessionID: "s", State: "resolved"},
		},
	}
	rows := BrowseSessionCatalog(catalog, WorkspaceLocation{}, false)
	if len(rows) != 2 || rows[0].Session.ID != "s" || rows[0].Pending != 2 || rows[1].Session.ID != "legacy" {
		t.Fatalf("visible rows = %+v", rows)
	}
	if rows := BrowseSessionCatalog(catalog, WorkspaceLocation{DeviceID: "two", WorkspaceID: "w"}, false); len(rows) != 0 {
		t.Fatalf("colliding workspace from another device = %+v", rows)
	}
	rows = BrowseSessionCatalog(catalog, WorkspaceLocation{DeviceID: device, WorkspaceID: "w"}, true)
	if len(rows) != 1 || rows[0].Session.ID != "archived-session" {
		t.Fatalf("archived rows in workspace = %+v", rows)
	}
}
