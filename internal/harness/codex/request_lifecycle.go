package codex

import (
	"bytes"
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

// Child wire identities stay inside this adapter. Shared requests point only to
// an already observed, session-owned work record and its root turn.
func (n *Normalizer) bindRequest(p *pending) bool {
	if sid := n.sessions[p.thread]; sid != "" {
		p.request.SessionID = sid
		return true
	}
	if n.work == nil {
		return false
	}
	b, ok := n.work.agents[p.thread]
	if !ok || n.sessions[b.rootThread] != b.session {
		return false
	}
	if saved, ok := n.work.childTurns[p.thread+"\x00"+p.sourceTurn]; ok && saved.id != b.id {
		return false
	}
	w := n.work.works[b.id]
	if w.State == "stopped" || w.State == "failed" || w.State == "unknown" {
		return false
	}
	if b.currentTurn != "" && b.currentTurn != p.sourceTurn {
		return false
	}
	p.request.SessionID, p.request.TurnID, p.request.WorkID = b.session, b.rootTurn, b.id
	return true
}
func (n *Normalizer) ownsRequest(p *pending) bool {
	copy := *p
	copy.request = p.request
	return n.bindRequest(&copy) && copy.request.SessionID == p.request.SessionID && copy.request.WorkID == p.request.WorkID && copy.request.TurnID == p.request.TurnID
}
func (n *Normalizer) openRequest(p *pending) domain.Event {
	n.requests[p.request.ID] = p
	n.sourceRequests[sourceKey(p.thread, p.sourceID)] = p.request.ID
	r := p.request
	return domain.Event{SessionID: r.SessionID, Type: "request.open", Request: &r}
}
func (n *Normalizer) receiveRequest(m wire.Message, p wire.Params) ([]domain.Event, []byte, error) {
	key := sourceKey(p.ThreadID, m.ID)
	if _, ok := n.sourceRequests[key]; ok {
		return nil, nil, nil
	}
	size := 0
	for _, q := range n.earlyRequests {
		if sourceKey(q.thread, q.sourceID) == key {
			return nil, nil, nil
		}
		b, _ := json.Marshal(q.request)
		size += len(b)
	}
	entry, err := makeRequest(m.Method, m.Params, p)
	if err != nil {
		return nil, unsupported(m.ID, err.Error()), nil
	}
	entry.sourceID, entry.thread, entry.sourceTurn = append(json.RawMessage(nil), m.ID...), p.ThreadID, p.TurnID
	b, _ := json.Marshal(entry.request)
	if len(b) > 16<<10 {
		return nil, unsupported(m.ID, "Question/approval resource limit exceeded"), nil
	}
	if n.bindRequest(entry) {
		return []domain.Event{n.openRequest(entry)}, nil, nil
	}
	// A fast child may ask before spawn completion. Hold a bounded, inaccessible
	// request only while an owned structured spawn is in flight; never guess a parent.
	if n.work != nil && len(n.work.spawns) > 0 && len(n.earlyRequests) < 32 && size+len(b) <= 256<<10 {
		n.earlyRequests = append(n.earlyRequests, entry)
		return nil, nil, nil
	}
	return nil, unsupported(m.ID, "No bound session"), nil
}
func (n *Normalizer) releaseEarlyRequests() ([]domain.Event, []byte) {
	var out []domain.Event
	var replies [][]byte
	keep := n.earlyRequests[:0]
	for _, p := range n.earlyRequests {
		if n.bindRequest(p) {
			out = append(out, n.openRequest(p))
		} else if n.work != nil && len(n.work.spawns) > 0 {
			keep = append(keep, p)
		} else {
			replies = append(replies, unsupported(p.sourceID, "No verified child source"))
		}
	}
	n.earlyRequests = keep
	if len(replies) == 0 {
		return out, nil
	}
	return out, bytes.Join(replies, []byte{'\n'})
}
func (n *Normalizer) resolveRequest(p wire.Params) []domain.Event {
	key := n.sourceRequests[sourceKey(p.ThreadID, p.RequestID)]
	entry := n.requests[key]
	if entry == nil {
		for i, q := range n.earlyRequests {
			if sourceKey(q.thread, q.sourceID) == sourceKey(p.ThreadID, p.RequestID) {
				n.earlyRequests = append(n.earlyRequests[:i], n.earlyRequests[i+1:]...)
				break
			}
		}
		return nil
	}
	r := entry.request
	if !r.State.AwaitingResolution() || !n.ownsRequest(entry) {
		return nil
	}
	r.State = "resolved"
	if entry.request.State == domain.RequestSubmitting {
		r.State = "answered"
	}
	r.Revision++
	entry.request = r
	out := []domain.Event{{Type: "request.resolve", SessionID: r.SessionID, Request: &r}}
	if r.State == "answered" {
		result := domain.Item{ID: "request-result:" + r.ID, WorkID: r.WorkID, TurnID: r.TurnID, Kind: "request_result", Presentation: r.Kind, Name: r.Title, Text: "请求结果", Status: "answered", Final: true, Parts: append([]domain.ContentPart(nil), r.Receipt...)}
		switch r.Kind {
		case "questions":
			result.Text = "已提交回答"
		case "form":
			result.Text = "表单结果"
		case "approval", "permissions":
			result.Text = "审批结果"
		}
		limitItem(&result)
		out = append(out, domain.Event{Type: "item.upsert", SessionID: r.SessionID, Item: &result})
	}
	return out
}
func (n *Normalizer) invalidateChildRequests(work, turn string) []domain.Event {
	out := []domain.Event{}
	for _, p := range n.requests {
		if p.request.WorkID != work || turn != "" && p.sourceTurn != turn || !p.request.State.AwaitingResolution() {
			continue
		}
		r := p.request
		r.State = "invalidated"
		r.Revision++
		p.request = r
		out = append(out, domain.Event{Type: "request.resolve", SessionID: r.SessionID, Request: &r})
	}
	return out
}
