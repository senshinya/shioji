package app

import (
	"fmt"
	"shioji/internal/domain"
	"slices"
)

// SessionNotification is a confirmed lifecycle transition awaiting client delivery.
// It contains session context, never assistant text, tool output or error details.
type SessionNotification struct {
	ID, ConnectionID, SessionID, WorkspaceID string
	Kind, Title, Body                        string
}

func (m *Model) notificationForEvent(e domain.Event) *SessionNotification {
	p := m.Preferences.Normalized().Notifications
	if !p.Enabled {
		return nil
	}
	notice := SessionNotification{ConnectionID: m.sourceConnection, SessionID: e.SessionID, Body: "会话"}
	if notice.ConnectionID == "" {
		notice.ConnectionID = "local"
	}
	for _, session := range m.Snapshot.Sessions {
		if session.ID == e.SessionID {
			notice.Body = session.Title
			notice.WorkspaceID = session.WorkspaceID
			break
		}
	}
	if e.Turn != nil && (e.Turn.Status == "completed" || e.Turn.Status == "failed") {
		for _, previous := range m.Details[e.SessionID].Turns {
			if previous.ID == e.Turn.ID && previous.Status == e.Turn.Status {
				return nil
			}
		}
		notice.Kind = e.Turn.Status
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
	} else {
		return nil
	}
	notice.ID = notice.ConnectionID + ":" + m.Snapshot.Generation + ":" + notice.SessionID + ":" + notice.ID
	if slices.Contains(m.notificationSeen, notice.ID) {
		return nil
	}
	return &notice
}
