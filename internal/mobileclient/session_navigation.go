package mobileclient

import "shioji/internal/domain"

// WorkspaceLocation describes the inbox filter. Its zero value includes every
// workspace and never selects an execution target.
type WorkspaceLocation struct {
	DeviceID    string
	WorkspaceID string
}

func (l WorkspaceLocation) Includes(workspace domain.Workspace) bool {
	return l == (WorkspaceLocation{}) ||
		(l.DeviceID == workspace.DeviceID && l.WorkspaceID == workspace.ID)
}

type SessionCatalogEntry struct {
	Session   domain.Session
	Workspace domain.Workspace
	Pending   int
}

func BrowseSessionCatalog(catalog domain.Snapshot, scope WorkspaceLocation, archived bool) []SessionCatalogEntry {
	if catalog.DeviceID == "" || (scope.DeviceID != "" && scope.DeviceID != catalog.DeviceID) {
		return nil
	}
	workspaces := make(map[string]domain.Workspace)
	for _, workspace := range catalog.Workspaces {
		if workspace.ID != "" && workspace.DeviceID == catalog.DeviceID && scope.Includes(workspace) {
			workspaces[workspace.ID] = workspace
		}
	}
	requests := make(map[string]int)
	for _, request := range catalog.PendingRequests {
		if request.State == "" || request.State.AwaitingResolution() {
			requests[request.SessionID]++
		}
	}
	var rows []SessionCatalogEntry
	for _, session := range catalog.Sessions {
		workspace, ok := workspaces[session.WorkspaceID]
		if !ok || session.ID == "" || (workspace.Archived || session.Archived) != archived ||
			(session.Context.DeviceID != "" && session.Context.DeviceID != catalog.DeviceID) {
			continue
		}
		rows = append(rows, SessionCatalogEntry{Session: session, Workspace: workspace, Pending: requests[session.ID]})
	}
	return rows
}
