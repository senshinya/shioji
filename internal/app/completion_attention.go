package app

import (
	"encoding/json"
	"errors"
	"maps"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// CompletedReply is a per-launch projection. Authoritative completion and read
// state live on the owning worker; client persistence never restores attention.
type CompletedReply struct {
	CompletionID string
	HistoryRef   string
	TurnID       string
	Order        int64
	Unread       bool
}

func (m *Model) HasCompletedReply(session string) bool {
	return m.CompletedReplies[session].Unread
}

func (m *Model) ReadCompletedReply(session string) {
	if reply, ok := m.CompletedReplies[session]; ok && reply.Unread {
		m.CompletedReplies = maps.Clone(m.CompletedReplies)
		reply.Unread = false
		m.CompletedReplies[session] = reply
	}
}

func (m *Model) completionCutoff() time.Time {
	if at, ok := m.completionCutoffs[m.Snapshot.DeviceID]; ok {
		return at
	}
	return m.startedAt
}

func (m *Model) projectCompletion(session domain.Session, cutoff time.Time) {
	c := session.Completion
	if c == nil || c.Pending || c.ID == "" || c.HistoryRef != session.ThreadID {
		m.CompletedReplies = maps.Clone(m.CompletedReplies)
		delete(m.CompletedReplies, session.ID)
		return
	}
	unread := c.Unread && !c.CompletedAt.Before(cutoff) && m.completionReads[session.ID] != c.ID
	next := CompletedReply{CompletionID: c.ID, HistoryRef: c.HistoryRef, TurnID: c.TurnID, Order: c.Order, Unread: unread}
	if previous, ok := m.CompletedReplies[session.ID]; ok && previous == next {
		return
	}
	m.CompletedReplies = maps.Clone(m.CompletedReplies)
	if m.CompletedReplies == nil {
		m.CompletedReplies = map[string]CompletedReply{}
	}
	m.CompletedReplies[session.ID] = next
}

func (m *Model) observeCompletedReply(e domain.Event) {
	if e.Session != nil {
		for _, s := range m.Snapshot.Sessions {
			if s.ID == e.Session.ID && s.Revision > e.Session.Revision {
				return
			}
		}
		if e.Session.Completion != nil || m.WorkerMinor >= 39 {
			m.projectCompletion(*e.Session, m.completionCutoff())
			return
		}
		if previous, ok := m.CompletedReplies[e.Session.ID]; ok && previous.HistoryRef != e.Session.ThreadID {
			m.CompletedReplies = maps.Clone(m.CompletedReplies)
			delete(m.CompletedReplies, e.Session.ID)
		}
	}
	if e.Type == "history.reset" {
		m.CompletedReplies = maps.Clone(m.CompletedReplies)
		delete(m.CompletedReplies, e.SessionID)
		return
	}
	// Older workers have no shared acknowledgement contract. Keep their live
	// completion behavior, while ignoring dated replay from before this launch.
	if e.Type != "turn" || e.Turn == nil || e.SessionID == "" || e.Turn.ID == "" || e.Turn.Status != "completed" || e.Turn.Presentation == "compaction" {
		return
	}
	if e.Turn.CompletedAt != nil && e.Turn.CompletedAt.Before(m.completionCutoff()) {
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

func (m *Model) reconcileCompletedReplies(snapshot domain.Snapshot, requestedAt time.Time) {
	// Translate this launch's monotonic elapsed time into the owning worker's
	// clock. Use request start, never UI application time: readiness/queue delay
	// must not admit pre-launch replies. The first request's transit interval is
	// conservatively excluded when the clocks differ.
	if !snapshot.ObservedAt.IsZero() {
		if _, ok := m.completionCutoffs[snapshot.DeviceID]; !ok {
			m.completionCutoffs[snapshot.DeviceID] = snapshot.ObservedAt.Add(-requestedAt.Sub(m.startedAt))
		}
	}
	cutoff := m.startedAt
	if at, ok := m.completionCutoffs[snapshot.DeviceID]; ok {
		cutoff = at
	}
	for _, session := range snapshot.Sessions {
		if session.Completion != nil {
			m.projectCompletion(session, cutoff)
		} else if previous, ok := m.CompletedReplies[session.ID]; ok && (previous.CompletionID != "" || previous.HistoryRef != session.ThreadID) {
			m.CompletedReplies = maps.Clone(m.CompletedReplies)
			delete(m.CompletedReplies, session.ID)
		}
	}
}

// CompletionReadJob also acknowledges a pre-launch reply when the user actually
// opens it. Hiding old attention at startup never marks other clients' work read.
func (m *Model) CompletionReadJob(id string) (Job, bool) {
	for _, s := range m.Snapshot.Sessions {
		c := s.Completion
		if s.ID != id || c == nil || c.Pending || c.ID == "" || !c.Unread || m.completionReads[id] == c.ID {
			continue
		}
		key := m.WorkspaceConnection(s.WorkspaceID)
		if key == "" {
			return Job{}, false
		}
		m.completionReads[id] = c.ID
		m.ReadCompletedReply(id)
		in := protocol.CompletionReadInput{SessionID: id, CompletionID: c.ID}
		return Job{ConnectionID: key, Method: "session.completion.read", Input: in, Apply: func(root *Model, raw json.RawMessage, err error) {
			apply := func(p *Model) {
				if p.completionReads[id] == in.CompletionID {
					delete(p.completionReads, id)
				}
				if err == nil {
					var result domain.Session
					if json.Unmarshal(raw, &result) != nil || result.ID != id {
						err = errors.New("会话处理状态回执不一致")
					} else {
						for i, current := range p.Snapshot.Sessions {
							if current.ID == id && result.Revision >= current.Revision {
								p.Snapshot.Sessions[i] = result
							}
						}
					}
				}
				for _, current := range p.Snapshot.Sessions {
					if current.ID == id {
						p.projectCompletion(current, p.completionCutoff())
					}
				}
				if err != nil {
					p.Error = "无法同步会话处理状态：" + err.Error()
				}
			}
			if root.DeviceStates[key] != nil {
				root.ApplyDeviceUpdate(key, apply)
			} else {
				apply(root)
			}
		}}, true
	}
	return Job{}, false
}
