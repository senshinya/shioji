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
	request     domain.PendingRequest
	sourceID    json.RawMessage
	thread      string
	responded   bool
	decisions   map[string]json.RawMessage
	permissions json.RawMessage
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
	return boundedTo(s, 128<<10)
}
func boundedTo(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "�") + "\n[Output limited]"
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
		if _, seen := n.sourceRequests[sourceKey(p.ThreadID, m.ID)]; seen {
			return nil, nil, nil
		}
		entry, buildErr := makeRequest(m.Method, m.Params, p)
		if buildErr != nil {
			return nil, unsupported(m.ID, buildErr.Error()), nil
		}
		req := entry.request
		req.SessionID = sid
		if sid == "" {
			return nil, unsupported(m.ID, "No bound session"), nil
		}
		encoded, _ := json.Marshal(req)
		if len(encoded) > 16<<10 {
			return nil, unsupported(m.ID, "Question/approval resource limit exceeded"), nil
		}
		entry.request, entry.sourceID, entry.thread = req, append(json.RawMessage(nil), m.ID...), p.ThreadID
		n.requests[req.ID] = entry
		n.sourceRequests[sourceKey(p.ThreadID, m.ID)] = req.ID
		event.Type = "request.open"
		event.Request = &req
		return emit(event)
	}
	if sid == "" {
		return nil, nil, nil
	}
	switch m.Method {
	case "thread/name/updated":
		if p.ThreadName == nil || strings.TrimSpace(*p.ThreadName) == "" {
			return nil, nil, nil
		}
		event.Type, event.Text = "session.title", boundedTo(strings.TrimSpace(*p.ThreadName), 4<<10)
		return emit(event)
	case "turn/plan/updated":
		plan := domain.Plan{TurnID: p.TurnID, Explanation: boundedTo(p.Explanation, 8<<10), Steps: []domain.PlanStep{}, Truncated: len(p.Explanation) > 8<<10}
		encoded, _ := json.Marshal(plan)
		used := len(encoded)
		for _, step := range p.Plan {
			status := step.Status
			if status == "inProgress" {
				status = "running"
			}
			next := domain.PlanStep{Text: boundedTo(step.Step, 8<<10), Status: status}
			encoded, _ = json.Marshal(next)
			if used+len(encoded)+1 > (128<<10)-64 {
				plan.Truncated = true
				break
			}
			used += len(encoded) + 1
			plan.Truncated = plan.Truncated || len(step.Step) > 8<<10
			plan.Steps = append(plan.Steps, next)
		}
		event.Type, event.Plan = "plan", &plan
		return emit(event)
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
		if p.Item.Type == "plan" {
			event.Type, event.Text = "plan.text", boundedJSONText(p.Item.Text, 128<<10)
			event.Plan = &domain.Plan{TurnID: p.TurnID}
			n.items[key] = domain.Item{ID: key, Text: event.Text, Final: m.Method == "item/completed"}
			return emit(event)
		}
		i := normalizeItem(p.Item, m.Method == "item/completed")
		i.ID, i.TurnID = key, p.TurnID
		n.items[key] = i
		event.Type = "item.upsert"
		event.Item = &i
		return emit(event)
	case "item/plan/delta":
		key := itemKey(p.TurnID, p.ItemID)
		i, ok := n.items[key]
		if !ok || i.Final {
			return nil, nil, nil
		}
		i.Text = boundedJSONText(i.Text+p.Delta, 128<<10)
		n.items[key] = i
		event.Type, event.Text, event.Plan = "plan.text", i.Text, &domain.Plan{TurnID: p.TurnID}
		return emit(event)
	case "item/mcpToolCall/progress":
		key := itemKey(p.TurnID, p.ItemID)
		i, ok := n.items[key]
		if !ok || i.Final {
			return nil, nil, nil
		}
		var progress struct{ Message string }
		json.Unmarshal(m.Params, &progress)
		i.Text = boundedTo(progress.Message, 8<<10)
		n.items[key] = i
		event.Type, event.Item = "item.upsert", &i
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
		limitItem(&i)
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
		events, _, _ := emit(event)
		if r.State == "answered" {
			result := domain.Item{ID: "request-result:" + r.ID, TurnID: r.TurnID, Kind: "request_result", Presentation: r.Kind, Name: r.Title, Text: "请求结果", Status: "answered", Final: true, Parts: append([]domain.ContentPart(nil), r.Receipt...)}
			switch r.Kind {
			case "questions":
				result.Text = "已提交回答"
			case "form":
				result.Text = "表单结果"
			case "approval", "permissions":
				result.Text = "审批结果"
			}
			limitItem(&result)
			events = append(events, domain.Event{Type: "item.upsert", SessionID: sid, Item: &result})
		}
		return events, nil, nil
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
		decision, ok := p.decisions[answer.Decision]
		if !ok {
			return nil, errors.New("decision is not offered by this request")
		}
		result = struct {
			Decision json.RawMessage `json:"decision"`
		}{decision}
	case "permissions":
		if answer.Decision != "turn" && answer.Decision != "session" && answer.Decision != "decline" {
			return nil, errors.New("invalid permission scope")
		}
		selected := answer.Answers["permissions"]
		scope := answer.Decision
		if scope == "decline" {
			selected = nil
			scope = "turn"
		}
		grant, err := permissionGrant(p.permissions, selected)
		if err != nil {
			return nil, err
		}
		result = map[string]any{"permissions": grant, "scope": scope}
	case "form", "external":
		if answer.Decision != "accept" && answer.Decision != "decline" && answer.Decision != "cancel" {
			return nil, errors.New("invalid elicitation action")
		}
		var content any
		if answer.Decision == "accept" {
			if p.request.Kind == "external" {
				return nil, errors.New("external requests complete through their external flow")
			}
			value, err := domain.FormContent(p.request.Fields, answer.Answers)
			if err != nil {
				return nil, err
			}
			content = value
		}
		result = map[string]any{"action": answer.Decision, "content": content, "_meta": nil}
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
	p.request.Receipt = requestReceipt(p.request, answer)
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
