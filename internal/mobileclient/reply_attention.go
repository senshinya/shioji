package mobileclient

import (
	"time"

	"shioji/internal/domain"
)

type DeviceReply struct {
	DeviceID, Address, Workspace, CompletionID string
	Session                                    domain.Session
}

func UnreadReplySince(session domain.Session, cutoff time.Time) bool {
	c := session.Completion
	return c != nil && c.ID != "" && c.Unread && !c.Pending && c.HistoryRef == session.ThreadID && !c.CompletedAt.Before(cutoff)
}

func DeviceReplies(catalog domain.Snapshot, address string, unread func(domain.Session) bool) []DeviceReply {
	workspaces := make(map[string]domain.Workspace, len(catalog.Workspaces))
	for _, workspace := range catalog.Workspaces {
		if workspace.DeviceID == catalog.DeviceID && !workspace.Archived {
			workspaces[workspace.ID] = workspace
		}
	}
	requests := make(map[string]bool)
	for _, request := range catalog.PendingRequests {
		if request.State == "" || request.State.AwaitingResolution() {
			requests[request.SessionID] = true
		}
	}
	var replies []DeviceReply
	for _, session := range catalog.Sessions {
		workspace, owned := workspaces[session.WorkspaceID]
		if !owned || session.ID == "" || session.Archived || session.Status != domain.ExecutionCompleted || requests[session.ID] ||
			session.Context.DeviceID != "" && session.Context.DeviceID != catalog.DeviceID || !unread(session) {
			continue
		}
		reply := DeviceReply{DeviceID: catalog.DeviceID, Address: address, Workspace: workspace.Name, Session: session}
		if session.Completion != nil {
			reply.CompletionID = session.Completion.ID
		}
		replies = append(replies, reply)
	}
	return replies
}
