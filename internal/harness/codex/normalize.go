package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
)

type pending struct {
	request     domain.PendingRequest
	sourceID    json.RawMessage
	thread      string
	sourceTurn  string
	decisions   map[string]json.RawMessage
	permissions json.RawMessage
}

const turnErrorJSONLimit = 32 << 10

type Normalizer struct {
	manualCompaction  map[string]bool
	goalContinuations map[string]*goalContinuation
	earlyRequests     []*pending
	work              *workTracker
	workControl       bool
	workSteer         bool
	workContinue      bool
	importResource    func(harness.OutputResource) (domain.Attachment, error)
	mu                sync.Mutex
	sessions          map[string]string
	requests          map[string]*pending
	sourceRequests    map[string]string
	items             map[string]domain.Item
	turns             map[string]domain.Turn
	activeTurnThreads map[string]string
	retries           map[string]domain.Item
	totals            map[string]wire.Counters
	usage             map[string]turnUsage
}

// Missing usage and a reported zero are different observations. Baseline
// capture does not erase a usage event received before turn/started.
type usagePhase uint8

const (
	usageUnobserved usagePhase = iota
	usageObserved
)

type turnUsage struct {
	baseline wire.Counters
	phase    usagePhase
}

func NewNormalizer() *Normalizer {
	return &Normalizer{sessions: map[string]string{}, requests: map[string]*pending{}, sourceRequests: map[string]string{}, items: map[string]domain.Item{}, turns: map[string]domain.Turn{}, activeTurnThreads: map[string]string{}, retries: map[string]domain.Item{}, totals: map[string]wire.Counters{}, usage: map[string]turnUsage{}}
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
	var goalMarker *domain.Item
	emit := func(e domain.Event) ([]domain.Event, []byte, error) {
		e.SessionID = sid
		work := n.observeRootWork(m, p, e)
		deferred, reply := n.releaseEarlyRequests()
		work = append(work, deferred...)
		// Publish continuing execution ownership before releasing its parent turn.
		if e.Turn != nil && e.Turn.Status != "running" {
			return append(work, e), reply, nil
		}
		if n.activeTurnThreads[p.TurnID] == p.ThreadID && e.Item != nil && p.Delta != "" &&
			(m.Method == "item/agentMessage/delta" || m.Method == "item/reasoning/summaryTextDelta") {
			if retry, ok := n.retries[p.TurnID]; ok && !retry.Final {
				retry.Name, retry.Status, retry.Final = "模型服务已恢复", "completed", true
				n.retries[p.TurnID] = retry
				return append([]domain.Event{{SessionID: sid, Type: "item.upsert", Item: &retry}, e}, work...), reply, nil
			}
		}
		if goalMarker != nil {
			return append([]domain.Event{{SessionID: sid, Type: "item.upsert", Item: goalMarker}, e}, work...), reply, nil
		}
		return append([]domain.Event{e}, work...), reply, nil
	}
	if len(m.ID) > 0 && string(m.ID) != "null" {
		return n.receiveRequest(m, p)
	}
	if m.Method == "serverRequest/resolved" {
		return n.resolveRequest(p), nil, nil
	}
	if events, handled := n.feedChildWork(m, p); handled {
		deferred, reply := n.releaseEarlyRequests()
		return append(events, deferred...), reply, nil
	}
	if sid == "" {
		n.rememberEarlyWorkTurn(m, p)
		return nil, nil, nil
	}
	switch m.Method {
	case "error":
		var reported wire.ErrorNotification
		if err := json.Unmarshal(m.Params, &reported); err != nil {
			return nil, nil, err
		}
		if reported.WillRetry && n.activeTurnThreads[p.TurnID] == p.ThreadID {
			previous, exists := n.retries[p.TurnID]
			if exists && !previous.Final {
				return nil, nil, nil
			}
			item := domain.Item{ID: "codex:retry:" + p.TurnID, TurnID: p.TurnID, Kind: "activity",
				Presentation: "wait", Name: "模型服务正在重试", Status: "running"}
			n.retries[p.TurnID] = item
			event.Type, event.Item, event.ActivityStart = "item.upsert", &item, !exists
			return emit(event)
		}
		if !reported.WillRetry && p.TurnID != "" && isUsageLimit(reported.Error) {
			turn := n.turns[p.TurnID]
			if turn.CompletedAt == nil && turn.Status != domain.ExecutionFailed {
				applyTurnError(&turn, reported.Error)
				n.turns[p.TurnID] = turn
			}
		}
		return nil, nil, nil
	case "model/rerouted":
		if n.activeTurnThreads[p.TurnID] != p.ThreadID {
			return nil, nil, nil
		}
		var route struct{ FromModel, ToModel string }
		if err := json.Unmarshal(m.Params, &route); err != nil {
			return nil, nil, err
		}
		from, to := strings.TrimSpace(route.FromModel), strings.TrimSpace(route.ToModel)
		if from == "" || to == "" || from == to || len(from) > 128 || len(to) > 128 ||
			strings.ContainsAny(from+to, "\r\n\x00") {
			return nil, nil, nil
		}
		id := "codex:rerouted:" + p.TurnID
		label := "模型已切换 · " + from + " → " + to
		previous, seen := n.items[id]
		if seen && previous.Name == label {
			return nil, nil, nil
		}
		item := domain.Item{ID: id, TurnID: p.TurnID, Kind: "activity", Presentation: "context",
			Name: label, Status: "completed", Final: true}
		n.items[id] = item
		event.Type, event.Item, event.ActivityStart = "item.upsert", &item, !seen
		return emit(event)
	case "thread/goal/updated", "thread/goal/cleared":
		event.Type, event.GoalSource = "goal", p.ThreadID
		if m.Method == "thread/goal/updated" {
			if p.Goal == nil || p.Goal.ThreadID != p.ThreadID {
				return nil, nil, nil
			}
			event.Goal = normalizeGoal(p.Goal)
		}
		n.observeGoal(p.ThreadID, event.Goal)
		return emit(event)
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
			status := "unknown"
			switch step.Status {
			case "pending", "completed":
				status = step.Status
			case "inProgress":
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
		previous := n.turns[p.Turn.ID]
		t := previous
		if m.Method == "turn/started" && n.manualCompaction[p.ThreadID] {
			t.Presentation = "compaction"
			delete(n.manualCompaction, p.ThreadID)
		}
		t.ID = p.Turn.ID
		t.StartedAt = stamp(p.Turn.StartedAt)
		t.CompletedAt = stamp(p.Turn.CompletedAt)
		t.DurationMS = p.Turn.DurationMS
		t.Status = normalizedTurnStatus(p.Turn.Status)
		n.observeGoalTurn(p.ThreadID, m.Method == "turn/started", previous, t)
		if t.Status == "running" {
			n.activeTurnThreads[t.ID] = p.ThreadID
			usage := n.usage[t.ID]
			if usage.phase == usageUnobserved {
				usage.baseline = n.totals[p.ThreadID]
			}
			n.usage[t.ID] = usage
		}
		if p.Turn.Error != nil {
			applyTurnError(&t, p.Turn.Error)
		}
		if t.UsageLimit != nil {
			if t.Status == domain.ExecutionFailed {
				if t.UsageLimit.ResetAt == nil {
					recorded := t.CompletedAt
					if recorded == nil {
						now := time.Now()
						recorded = &now
					}
					t.UsageLimit = &domain.UsageLimit{ResetAt: usageResetTime(t.Error, recorded)}
				}
			} else if !t.Status.Active() {
				t.UsageLimit = nil
			}
		}
		if m.Method == "turn/completed" && n.usage[p.Turn.ID].phase == usageObserved {
			base := n.usage[t.ID].baseline
			total := n.totals[p.ThreadID]
			t.InputTokens = difference(total.Input, base.Input)
			t.OutputTokens = difference(total.Output, base.Output)
			t.CachedInputTokens = difference(total.Cached, base.Cached)
		}
		n.turns[t.ID] = t
		event.Type = "turn"
		event.Turn = &t
		if m.Method == "turn/completed" && !t.Status.Active() && n.activeTurnThreads[t.ID] == p.ThreadID {
			delete(n.activeTurnThreads, t.ID)
			delete(n.items, "codex:rerouted:"+t.ID)
			if retry, ok := n.retries[t.ID]; ok {
				delete(n.retries, t.ID)
				if retry.Final {
					return emit(event)
				}
				retry.Status, retry.Final = string(t.Status), true
				switch t.Status {
				case domain.ExecutionCompleted:
					retry.Name = "模型服务已恢复"
				case domain.ExecutionFailed:
					retry.Name = "模型服务重试失败"
				default:
					retry.Name = "模型服务重试已结束"
				}
				events, reply, err := emit(event)
				return append([]domain.Event{{SessionID: sid, Type: "item.upsert", Item: &retry}}, events...), reply, err
			}
		}
		return emit(event)
	case "thread/tokenUsage/updated":
		usage := n.usage[p.TurnID]
		if usage.phase == usageUnobserved {
			// On resume/fork there may be no observed thread total yet. Only
			// the first request can establish a missing turn baseline: later
			// `last` values cover just that request, not the entire turn.
			baseline := func(known, previous, total, last *int64) *int64 {
				if known != nil {
					return known
				}
				if previous != nil {
					return previous
				}
				return difference(total, last)
			}
			previous := n.totals[p.ThreadID]
			usage.baseline.Input = baseline(usage.baseline.Input, previous.Input, p.TokenUsage.Total.Input, p.TokenUsage.Last.Input)
			usage.baseline.Output = baseline(usage.baseline.Output, previous.Output, p.TokenUsage.Total.Output, p.TokenUsage.Last.Output)
			usage.baseline.Cached = baseline(usage.baseline.Cached, previous.Cached, p.TokenUsage.Total.Cached, p.TokenUsage.Last.Cached)
		}
		n.totals[p.ThreadID] = p.TokenUsage.Total
		usage.phase = usageObserved
		n.usage[p.TurnID] = usage
		if used := p.TokenUsage.Last.Tokens; used != nil && *used >= 0 {
			limit := p.TokenUsage.ModelContextWindow
			if limit != nil && *limit <= 0 {
				limit = nil
			}
			event.Type = "context.usage"
			event.ContextUsage = &domain.ContextUsage{TurnID: p.TurnID, UsedTokens: used, LimitTokens: limit, UpdatedAt: time.Now().UTC()}
			return emit(event)
		}
		return nil, nil, nil
	case "item/started", "item/completed":
		goalMarker = n.goalContinuationItem(p)
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
		i := n.normalizeResourceItem(sid, key, p.Item, m.Method == "item/completed")
		i.ID, i.TurnID = key, p.TurnID
		n.items[key] = i
		event.Type = "item.upsert"
		event.ActivityStart = m.Method == "item/started"
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
	case "item/reasoning/summaryTextDelta":
		i, ok := n.appendReasoningSummary(itemKey(p.TurnID, p.ItemID), p)
		if !ok {
			return nil, nil, nil
		}
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
	if p == nil || p.request.State != "pending" || answer.Revision != p.request.Revision || !n.ownsRequest(p) {
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
	p.request.State = "submitting"
	p.request.Receipt = requestReceipt(p.request, answer)
	return out, nil
}
func (n *Normalizer) retryUnwrittenAnswer(answer domain.Answer) (domain.PendingRequest, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	p := n.requests[answer.RequestID]
	if p == nil || p.request.State != domain.RequestSubmitting || answer.Revision != p.request.Revision {
		return domain.PendingRequest{}, false
	}
	p.request.State = domain.RequestPending
	p.request.Revision++
	p.request.Receipt = nil
	return p.request, true
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
	work := n.invalidateWork()
	n.earlyRequests = nil
	out := []domain.Event{}
	for _, p := range n.requests {
		if p.request.State.AwaitingResolution() {
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
	// Runtime loss establishes the managed-generation guard before independent
	// work becomes unknown and stops contributing active checkout ownership.
	return append(out, work...)
}

func normalizedTurnStatus(status string) domain.ExecutionState {
	switch status {
	case "inProgress":
		return domain.ExecutionRunning
	case "completed", "interrupted", "failed":
		return domain.ExecutionState(status)
	default:
		return domain.ExecutionUnknown
	}
}
