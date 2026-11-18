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

func (v *View) attentionSession() (domain.Session, bool) {
	session, count := v.attentionSummary()
	return session, count > 0
}

// Count each navigable session once. Questions and approvals retain priority
// over unread natural replies.
func (v *View) attentionSummary() (domain.Session, int) {
	var firstRequest, firstReply domain.Session
	count := 0
	for _, session := range v.Model.Snapshot.Sessions {
		if v.pendingCount(session.ID) > 0 {
			if firstRequest.ID == "" {
				firstRequest = session
			}
			count++
			continue
		}
		if session.Archived || session.Status != "completed" || !v.Model.HasCompletedReply(session.ID) || v.sessionOutputAnimating(session.ID) {
			continue
		}
		for _, workspace := range v.Model.Snapshot.Workspaces {
			if workspace.ID == session.WorkspaceID && !workspace.Archived {
				if firstReply.ID == "" {
					firstReply = session
				}
				count++
				break
			}
		}
	}
	if firstRequest.ID != "" {
		return firstRequest, count
	}
	return firstReply, count
}
