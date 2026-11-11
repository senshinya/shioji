package app

import (
	"maps"
	"shioji/internal/domain"
)

// CompletedReply tracks the latest natural reply independently of the history
// cache and system notification preferences. Read entries prevent late usage
// updates or reconnect replay from presenting the same reply again.
type CompletedReply struct {
	HistoryRef string `json:"historyRef,omitempty"`
	TurnID     string `json:"turnId"`
	Order      int64  `json:"order"`
	Unread     bool   `json:"unread"`
}

func (m *Model) HasCompletedReply(session string) bool {
	return m.CompletedReplies[session].Unread
}

func (m *Model) ReadCompletedReply(session string) {
	if reply, ok := m.CompletedReplies[session]; ok && reply.Unread {
		reply.Unread = false
		m.CompletedReplies[session] = reply
	}
}

func (m *Model) observeCompletedReply(e domain.Event) {
	if e.Type == "history.reset" {
		// A history edit can reuse turn order after truncation.
		m.CompletedReplies = maps.Clone(m.CompletedReplies)
		delete(m.CompletedReplies, e.SessionID)
		return
	}
	if e.Type != "turn" || e.Turn == nil || e.SessionID == "" || e.Turn.ID == "" || e.Turn.Status != "completed" || e.Turn.Presentation == "compaction" {
		return
	}
	previous := m.CompletedReplies[e.SessionID]
	history := ""
	if e.Session != nil {
		history = e.Session.ThreadID
	} else {
		for _, session := range m.Snapshot.Sessions {
			if session.ID == e.SessionID {
				history = session.ThreadID
				break
			}
		}
	}
	if previous.HistoryRef == history && (previous.TurnID == e.Turn.ID || previous.Order > 0 && e.Turn.Order > 0 && previous.Order >= e.Turn.Order) {
		return
	}
	m.CompletedReplies = maps.Clone(m.CompletedReplies)
	if m.CompletedReplies == nil {
		m.CompletedReplies = map[string]CompletedReply{}
	}
	m.CompletedReplies[e.SessionID] = CompletedReply{HistoryRef: history, TurnID: e.Turn.ID, Order: e.Turn.Order, Unread: true}
}

func (m *Model) reconcileCompletedReplies(snapshot domain.Snapshot) {
	cloned := false
	for _, session := range snapshot.Sessions {
		if previous, ok := m.CompletedReplies[session.ID]; ok && previous.HistoryRef != session.ThreadID {
			if !cloned {
				m.CompletedReplies = maps.Clone(m.CompletedReplies)
				cloned = true
			}
			delete(m.CompletedReplies, session.ID)
		}
	}
}
