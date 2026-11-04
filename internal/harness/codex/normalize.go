package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

type pending struct {
	request   domain.PendingRequest
	sourceID  json.RawMessage
	thread    string
	responded bool
}
type Normalizer struct {
	mu             sync.Mutex
	sessions       map[string]string
	requests       map[string]*pending
	sourceRequests map[string]string
	items          map[string]domain.Item
	turns          map[string]domain.Turn
	totals         map[string]wire.Counters
	baselines      map[string]wire.Counters
	usageObserved  map[string]bool
}

func NewNormalizer() *Normalizer {
	return &Normalizer{usageObserved: map[string]bool{}, sessions: map[string]string{}, requests: map[string]*pending{}, sourceRequests: map[string]string{}, items: map[string]domain.Item{}, turns: map[string]domain.Turn{}, totals: map[string]wire.Counters{}, baselines: map[string]wire.Counters{}}
}
func (n *Normalizer) Bind(thread, session string, fresh bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.sessions[thread]; ok {
		return
	}
	n.sessions[thread] = session
	if fresh {
		z := int64(0)
		n.totals[thread] = wire.Counters{Input: &z, Output: &z, Cached: &z}
	}
}
func sourceKey(thread string, id json.RawMessage) string { return thread + ":" + string(id) }
func itemKey(turn, item string) string                   { return turn + "/" + item }
func bounded(s string) string {
	if len(s) <= 128<<10 {
		return s
	}
	return strings.ToValidUTF8(s[:128<<10], "�") + "\n[Output limited]"
}
func stamp(v *int64) *time.Time {
	if v == nil {
		return nil
	}
	t := time.Unix(*v, 0).UTC()
	return &t
}
func difference(end, start *int64) *int64 {
	if end == nil || start == nil || *end < *start {
		return nil
	}
	v := *end - *start
	return &v
}
func (n *Normalizer) Feed(data []byte) ([]domain.Event, []byte, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var m wire.Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, err
	}
	var p wire.Params
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return nil, nil, err
	}
	sid := n.sessions[p.ThreadID]
	event := domain.Event{SessionID: sid}
	emit := func(e domain.Event) ([]domain.Event, []byte, error) {
		e.SessionID = sid
		return []domain.Event{e}, nil, nil
	}
	if len(m.ID) > 0 && string(m.ID) != "null" {
		req := domain.PendingRequest{ID: domain.NewID(), SessionID: sid, TurnID: p.TurnID, ItemID: itemKey(p.TurnID, p.ItemID), State: "pending", Revision: 1}
		switch m.Method {
		case "item/tool/requestUserInput":
			req.Kind = "questions"
			req.Title = "需要回答"
			for _, q := range p.Questions {
				if q.IsSecret {
					return nil, unsupported(m.ID, "Secret question forms are not enabled in stage 1"), nil
				}
				question := domain.Question{ID: q.ID, Header: q.Header, Text: bounded(q.Question), Custom: q.IsOther}
				for _, o := range q.Options {
					question.Options = append(question.Options, domain.Option{Label: o.Label, Description: o.Description})
				}
				req.Questions = append(req.Questions, question)
			}
			if len(req.Questions) == 0 {
				return nil, unsupported(m.ID, "Empty question set"), nil
			}
		case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
			req.Kind = "approval"
			req.Title = "执行审批"
			req.Text = bounded(p.Command)
			if req.Text == "" {
				req.Text = bounded(p.Reason)
			}
			if m.Method == "item/fileChange/requestApproval" {
				req.Title = "文件改动审批"
			}
		default:
			return nil, unsupported(m.ID, "Unsupported server request"), nil
		}
		if sid == "" {
			return nil, unsupported(m.ID, "No bound session"), nil
		}
		encoded, _ := json.Marshal(req)
		active := 0
		for _, entry := range n.requests {
			if entry.thread == p.ThreadID && (entry.request.State == "pending" || entry.request.State == "submitting") {
				active++
			}
		}
		if len(encoded) > 16<<10 || active >= 4 {
			return nil, unsupported(m.ID, "Question/approval resource limit exceeded"), nil
		}
		n.requests[req.ID] = &pending{request: req, sourceID: append(json.RawMessage(nil), m.ID...), thread: p.ThreadID}
		n.sourceRequests[sourceKey(p.ThreadID, m.ID)] = req.ID
		event.Type = "request.open"
		event.Request = &req
		return emit(event)
	}
	if sid == "" {
		return nil, nil, nil
	}
	switch m.Method {
	case "turn/started", "turn/completed":
		t := n.turns[p.Turn.ID]
		t.ID = p.Turn.ID
		t.StartedAt = stamp(p.Turn.StartedAt)
		t.CompletedAt = stamp(p.Turn.CompletedAt)
		t.DurationMS = p.Turn.DurationMS
		t.Status = p.Turn.Status
		if t.Status == "inProgress" {
			t.Status = "running"
			n.baselines[t.ID] = n.totals[p.ThreadID]
		}
		if p.Turn.Error != nil {
			t.Error = bounded(p.Turn.Error.Message)
		}
		if m.Method == "turn/completed" && n.usageObserved[p.Turn.ID] {
			base := n.baselines[t.ID]
			total := n.totals[p.ThreadID]
			t.InputTokens = difference(total.Input, base.Input)
			t.OutputTokens = difference(total.Output, base.Output)
			t.CachedInputTokens = difference(total.Cached, base.Cached)
		}
		n.turns[t.ID] = t
		event.Type = "turn"
		event.Turn = &t
		return emit(event)
	case "thread/tokenUsage/updated":
		n.totals[p.ThreadID] = p.TokenUsage.Total
		n.usageObserved[p.TurnID] = true
		return nil, nil, nil
	case "item/started", "item/completed":
		if p.Item.Type == "userMessage" {
			return nil, nil, nil
		} // The accepted input already has a stable worker item.
		key := itemKey(p.TurnID, p.Item.ID)
		old := n.items[key]
		if old.Final && m.Method == "item/started" {
			return nil, nil, nil
		}
		i := domain.Item{ID: key, TurnID: p.TurnID, Final: m.Method == "item/completed", Status: p.Item.Status}
		switch p.Item.Type {
		case "agentMessage":
			i.Kind = "assistant"
			i.Text = bounded(p.Item.Text)
		case "commandExecution":
			i.Kind = "tool"
			i.Name = bounded(p.Item.Command)
			i.Text = bounded(p.Item.AggregatedOutput)
			i.ExitCode = p.Item.ExitCode
		case "reasoning":
			return nil, nil, nil
		default:
			i.Kind = "unsupported"
			i.Name = p.Item.Type
			i.Text = "暂不支持的内容：" + p.Item.Type
		}
		n.items[key] = i
		event.Type = "item.upsert"
		event.Item = &i
		return emit(event)
	case "item/agentMessage/delta", "item/commandExecution/outputDelta":
		key := itemKey(p.TurnID, p.ItemID)
		i, ok := n.items[key]
		if !ok {
			return nil, nil, fmt.Errorf("delta before item start: %s", key)
		}
		if i.Final {
			return nil, nil, nil
		}
		i.Text = bounded(i.Text + p.Delta)
		n.items[key] = i
		event.Type = "item.upsert"
		event.Item = &i
		return emit(event)
	case "serverRequest/resolved":
		key := n.sourceRequests[sourceKey(p.ThreadID, p.RequestID)]
		pending := n.requests[key]
		if pending == nil {
			return nil, nil, nil
		}
		r := pending.request
		if r.State != "pending" && r.State != "submitting" {
			return nil, nil, nil
		}
		if pending.responded {
			r.State = "answered"
		} else {
			r.State = "resolved"
		}
		r.Revision++
		pending.request = r
		event.Type = "request.resolve"
		event.Request = &r
		return emit(event)
	}
	return nil, nil, nil
}
func unsupported(id json.RawMessage, message string) []byte {
	b, _ := json.Marshal(wire.Message{ID: id, Error: &wire.RPCError{Code: -32601, Message: message}})
	return b
}
func (n *Normalizer) Response(answer domain.Answer) ([]byte, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	p := n.requests[answer.RequestID]
	if p == nil || p.request.State != "pending" || p.responded || answer.Revision != p.request.Revision {
		return nil, errors.New("request no longer pending or revision conflict")
	}
	var result any
	switch p.request.Kind {
	case "approval":
		if answer.Decision != "accept" && answer.Decision != "decline" {
			return nil, errors.New("expected one-time accept or decline")
		}
		result = struct {
			Decision string `json:"decision"`
		}{answer.Decision}
	case "questions":
		if len(answer.Answers) != len(p.request.Questions) {
			return nil, errors.New("answer every question in this request")
		}
		answers := map[string]struct {
			Answers []string `json:"answers"`
		}{}
		for _, q := range p.request.Questions {
			a := answer.Answers[q.ID]
			if len(a) != 1 || strings.TrimSpace(a[0]) == "" || len(a[0]) > 16<<10 {
				return nil, errors.New("each question requires one nonempty answer")
			}
			if !q.Custom && len(q.Options) > 0 {
				found := false
				for _, o := range q.Options {
					if o.Label == a[0] {
						found = true
					}
				}
				if !found {
					return nil, errors.New("answer is not an available option")
				}
			}
			answers[q.ID] = struct {
				Answers []string `json:"answers"`
			}{a}
		}
		result = struct {
			Answers any `json:"answers"`
		}{answers}
	default:
		return nil, errors.New("unsupported answer kind")
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(wire.Message{ID: p.sourceID, Result: b})
	if err != nil {
		return nil, err
	}
	p.responded = true
	p.request.State = "submitting"
	return out, nil
}
func (n *Normalizer) Pending(key string) (domain.PendingRequest, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	p, ok := n.requests[key]
	if !ok {
		return domain.PendingRequest{}, false
	}
	return p.request, true
}
func (n *Normalizer) Invalidate() []domain.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := []domain.Event{}
	for _, p := range n.requests {
		if p.request.State == "pending" || p.request.State == "submitting" {
			r := p.request
			r.State = "invalidated"
			r.Revision++
			p.request = r
			out = append(out, domain.Event{Type: "request.resolve", SessionID: r.SessionID, Request: &r})
		}
	}
	for thread, sid := range n.sessions {
		_ = thread
		out = append(out, domain.Event{Type: "runtime.lost", SessionID: sid})
	}
	return out
}
