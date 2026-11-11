package ui

import "shioji/internal/domain"

// Questions and approvals remain first, followed by each unread natural reply.
// Use the same selection for the button's availability and its action.
func (v *View) attentionSession() (domain.Session, bool) {
	for _, session := range v.Model.Snapshot.Sessions {
		if v.pendingCount(session.ID) > 0 {
			return session, true
		}
	}
	for _, session := range v.Model.Snapshot.Sessions {
		if session.Archived || session.Status != "completed" || !v.Model.HasCompletedReply(session.ID) || v.sessionOutputAnimating(session.ID) {
			continue
		}
		for _, workspace := range v.Model.Snapshot.Workspaces {
			if workspace.ID == session.WorkspaceID && !workspace.Archived {
				return session, true
			}
		}
	}
	return domain.Session{}, false
}
