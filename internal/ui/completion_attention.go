package ui

import (
	"errors"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func (v *View) readVisibleCompletedReplies() {
	if w := v.nativeWindow; w != nil && (w.IsDestroyed() || !w.IsFocused() || !w.IsVisible() || w.IsMinimized()) {
		v.visibleCompletions = nil
		return
	}
	visible := map[string]app.CompletedReply{}
	for id, reply := range v.Model.CompletedReplies {
		if !v.sessionVisible(id) {
			continue
		}
		unread := reply.Unread
		reply.Unread = false
		visible[id] = reply
		// Try each completion once per visible visit. A failed worker receipt
		// restores attention and its error without retrying on every frame.
		if previous, ok := v.visibleCompletions[id]; unread && (!ok || previous != reply) {
			v.readCompletedReply(id)
		}
	}
	v.visibleCompletions = visible
}

func (v *View) readCompletedReply(id string) {
	root := v.splitRoot()
	if root.Controller != nil {
		if job, ok := root.Model.CompletionReadJob(id); ok {
			if !root.Controller.Do(job) {
				job.Apply(root.Model, nil, errors.New("操作队列已满，请重试"))
			}
			return
		}
	}
	root.Model.ReadCompletedReply(id)
}

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
