package app

import (
	"fmt"
	"shioji/internal/domain"
	"slices"
)

// SessionNotification is a confirmed lifecycle transition awaiting client delivery.
// It contains session context, never assistant text, tool output or error details.
type SessionNotification struct {
	SessionLocation
	ID, ConnectionID    string
	Kind, Title, Body   string
	ThreadID, RequestID string
	WorkID              string
	RequestRevision     int64
}

func (m *Model) notificationForEvent(e domain.Event) *SessionNotification {
	p := m.Preferences.Normalized().Notifications
	if !p.Enabled {
		return nil
	}
	notice := SessionNotification{ConnectionID: m.sourceConnection, Body: "会话",
		SessionLocation: SessionLocation{DeviceID: m.Snapshot.DeviceID, SessionID: e.SessionID}}
	if notice.ConnectionID == "" {
		notice.ConnectionID = "local"
	}
	for _, session := range m.Snapshot.Sessions {
		if session.ID == e.SessionID {
			notice.Body = session.Title
			notice.WorkspaceID = session.WorkspaceID
			notice.ThreadID = session.ThreadID
			break
		}
	}
	// Completion publications may arrive on the final work event or runtime-loss
	// metadata, after the parent turn and its reading cache have already settled.
	publication := false
	if e.Session != nil && e.Session.Completion != nil && (e.Type == "turn" || e.Type == "work.upsert" || e.Type == "work.item" || e.Type == "session") {
		c := e.Session.Completion
		publication = !c.Pending && c.ID != "" && c.Unread && !c.CompletedAt.Before(m.completionCutoff())
		for _, old := range m.Snapshot.Sessions {
			if old.ID == e.SessionID && (old.Revision > e.Session.Revision || old.Completion != nil && old.Completion.ID == c.ID) {
				publication = false
			}
		}
		if publication {
			notice.Kind, notice.Title = "completed", "回复已完成"
			if c.Outcome == domain.ExecutionInterrupted {
				notice.Kind, notice.Title = "failed", "后台工作已中断"
			} else if c.AutomaticContinuation {
				return nil
			}
			if notice.Kind == "completed" && !p.Completed || notice.Kind == "failed" && !p.Failed {
				return nil
			}
			notice.ID = "completion:" + c.ID
		}
	}
	if publication {
		// Use the durable publication identity below for cross-cache deduplication.
	} else if e.Turn != nil && (e.Turn.Status == "completed" || e.Turn.Status == "failed") {
		if e.Turn.Presentation == "compaction" || e.Turn.Status == "completed" && e.Session != nil && (e.Session.Completion != nil || m.WorkerMinor >= 39) {
			return nil
		}
		for _, previous := range m.Details[e.SessionID].Turns {
			if previous.ID == e.Turn.ID && previous.Status == e.Turn.Status {
				return nil
			}
		}
		notice.Kind = string(e.Turn.Status)
		if notice.Kind == "completed" {
			if !p.Completed {
				return nil
			}
			notice.Title = "回复已完成"
		} else {
			if !p.Failed {
				return nil
			}
			notice.Title = "执行失败"
		}
		notice.ID = "turn:" + e.Turn.ID + ":" + notice.Kind
	} else if e.Type == "request.open" && e.Request != nil && e.Request.State == "pending" {
		if !p.Attention {
			return nil
		}
		for _, previous := range m.Details[e.SessionID].Requests {
			if previous.ID == e.Request.ID && previous.Revision == e.Request.Revision {
				return nil
			}
		}
		notice.Kind = "attention"
		notice.Title = "需要授权"
		if e.Request.Kind == "questions" || e.Request.Kind == "form" {
			notice.Title = "需要回答"
		}
		notice.ID = fmt.Sprintf("request:%s:%d", e.Request.ID, e.Request.Revision)
		notice.RequestID, notice.WorkID, notice.RequestRevision = e.Request.ID, e.Request.WorkID, e.Request.Revision
	} else {
		return nil
	}
	notice.ID = notice.ConnectionID + ":" + m.Snapshot.Generation + ":" + notice.SessionID + ":" + notice.ID
	if slices.Contains(m.notificationSeen, notice.ID) {
		return nil
	}
	return &notice
}
