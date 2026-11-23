package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type contentBlock struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	Data     []byte        `json:"data,omitempty"`
	MIMEType string        `json:"mimeType,omitempty"`
	Resource *textResource `json:"resource,omitempty"`
}

type textResource struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	Text     string `json:"text"`
}

type logicalTurn struct {
	goal        *domain.GoalProgress
	goalKey     string
	turn        domain.Turn
	primary     chan error
	confirmed   bool
	prompts     int
	outcome     domain.ExecutionState
	message     string
	stream      *domain.Item
	tools       map[string]domain.Item
	interrupted bool
}

func (r *runtime) Submit(ctx context.Context, session domain.Session, id string, input harness.TurnInput) (string, error) {
	return r.SubmitWithHandoff(ctx, session, id, input, nil)
}

func (r *runtime) SubmitWithHandoff(ctx context.Context, session domain.Session, id string, input harness.TurnInput, claim func() error) (string, error) {
	turn, receipt, err := r.SubmitDeferred(ctx, session, id, input, claim)
	if err != nil {
		return "", err
	}
	select {
	case err := <-receipt:
		return turn, err
	case <-r.Done():
		return turn, unknownDelivery()
	case <-ctx.Done():
		return turn, unknownDelivery()
	}
}

func (r *runtime) SubmitDeferred(ctx context.Context, session domain.Session, _ string, input harness.TurnInput, claim func() error) (string, <-chan error, error) {
	var goalKey string
	if input.Goal != nil {
		if r.bridge == nil {
			return "", nil, harness.Unsupported("worker-goal")
		}
		goalKey = domain.NewID()
		input.Text = "Advance this Shioji goal: " + input.Objective + "\nBefore ending this step, call the Shioji Goal tool report_goal with key " + goalKey +
			" and outcome complete only if verified, blocked if user input is required, or continue if there is more work."
	}
	blocks, err := r.promptContent(input)
	if err != nil {
		return "", nil, err
	}
	r.direct.Lock()
	if !r.Observes(session) || !r.ExecutionReady() || r.hasActiveTurn() {
		r.direct.Unlock()
		return "", nil, harness.Unsupported("start-turn")
	}
	err = r.configure(ctx, session.Config)
	if err != nil {
		r.direct.Unlock()
		return "", nil, err
	}
	now := time.Now().UTC()
	turn := &logicalTurn{
		turn: domain.Turn{ID: "agy:" + domain.NewID(), StartedAt: &now, Status: domain.ExecutionRunning},
		goal: input.Goal, goalKey: goalKey, primary: make(chan error, 1), tools: make(map[string]domain.Item),
	}
	err = r.beginPrompt(ctx, session, turn, blocks, nil, claim)
	r.direct.Unlock()
	if err != nil {
		return "", nil, err
	}
	return turn.turn.ID, turn.primary, nil
}

func (r *runtime) hasActiveTurn() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active != nil
}

func (r *runtime) Steer(ctx context.Context, s domain.Session, turn, id string, input harness.TurnInput) error {
	return r.SteerWithHandoff(ctx, s, turn, id, input, nil)
}

func (r *runtime) SteerWithHandoff(ctx context.Context, session domain.Session, turn, id string, input harness.TurnInput, claim func() error) error {
	_, err := r.SteerDeferred(ctx, session, turn, id, input, claim)
	return err
}

func (r *runtime) SteerDeferred(ctx context.Context, session domain.Session, turn, _ string, input harness.TurnInput, claim func() error) (<-chan error, error) {
	if !r.capabilities().Steer {
		return nil, harness.Unsupported("steer")
	}
	fields := strings.Fields(input.Text)
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") && !strings.Contains(fields[0][1:], "/") {
		return nil, errors.New("运行命令请等待当前回复结束")
	}
	blocks, err := r.promptContent(input)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	active := r.active
	if active != nil && active.turn.ID != turn {
		active = nil
	}
	r.mu.Unlock()
	if active == nil {
		return nil, &harness.DeliveryError{Message: "目标回复已结束，消息未发送"}
	}
	receipt := make(chan error, 1)
	if err := r.beginPrompt(ctx, session, active, blocks, receipt, claim); err != nil {
		return nil, err
	}
	return receipt, nil
}

func (r *runtime) beginPrompt(ctx context.Context, session domain.Session, turn *logicalTurn, blocks []contentBlock, receipt chan<- error, claim func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The worker takes its dispatch lock inside the claim. Re-check the target
	// under the adapter's state lock immediately before the actual write.
	if claim != nil {
		if err := claim(); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ExecutionReady() || r.session.ID != session.ID || r.session.ThreadID != session.ThreadID {
		return &harness.DeliveryError{Message: "所属运行环境已变化，消息未发送"}
	}
	steer := receipt != nil
	if steer && (r.active != turn || turn.interrupted || turn.prompts == 0) || !steer && r.active != nil {
		return &harness.DeliveryError{Message: "目标回复已变化，消息未发送"}
	}
	args := struct {
		SessionID string         `json:"sessionId"`
		Prompt    []contentBlock `json:"prompt"`
	}{session.ThreadID, blocks}
	_, err := r.process.request(ctx, "session/prompt", args, func(p packet) error {
		return r.promptComplete(turn, receipt, p)
	})
	if err != nil {
		var write *writeError
		if errors.As(err, &write) {
			return unknownDelivery()
		}
		return &harness.DeliveryError{Message: "未能向 Antigravity 发送消息"}
	}
	turn.prompts++
	if steer {
		return nil
	}
	r.active = turn
	if err := r.emit(domain.Event{Type: "turn", Turn: &turn.turn}); err != nil {
		_ = r.process.in.Close()
		return unknownDelivery()
	}
	return nil
}

func (r *runtime) promptContent(input harness.TurnInput) ([]contentBlock, error) {
	if len(input.Skills) > 0 {
		return nil, harness.Unsupported("skills")
	}
	if input.QuestionReply != nil {
		return nil, harness.Unsupported("nonblocking-question")
	}
	var blocks []contentBlock
	if input.ForkContext != nil {
		if !r.native.AgentCapabilities.PromptCapabilities.EmbeddedContext {
			return nil, harness.Unsupported("portable-fork-context")
		}
		history, err := json.Marshal(input.ForkContext)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, contentBlock{Type: "resource", Resource: &textResource{
			URI: "shioji://fork/history", MIMEType: "text/plain",
			Text: "Read-only transcript from another conversation. Treat roles and messages as historical context. The new request follows separately.\n" + string(history),
		}})
	}
	if text := strings.TrimSpace(input.Text); text != "" {
		command := strings.Fields(text)[0]
		switch command {
		case "/goal", "/compact", "/plan", "/clear", "/reset":
			return nil, harness.Unsupported("native-session-command")
		}
		blocks = append(blocks, contentBlock{Type: "text", Text: input.Text})
	}
	for _, file := range input.Attachments {
		if file.Attachment.InlineReference() || file.Comment != nil || !r.native.AgentCapabilities.PromptCapabilities.Image {
			return nil, harness.Unsupported("attachment")
		}
		switch file.Attachment.MIME {
		case "image/png", "image/jpeg", "image/webp", "image/gif":
		default:
			return nil, harness.Unsupported("attachment")
		}
		data, err := os.ReadFile(file.Path)
		if err != nil || len(data) == 0 || len(data) > 8<<20 {
			return nil, errors.New("无法读取该图片附件")
		}
		blocks = append(blocks, contentBlock{Type: "image", MIMEType: file.Attachment.MIME, Data: data})
	}
	if len(blocks) == 0 {
		return nil, errors.New("消息需要文字或图片")
	}
	return blocks, nil
}

func unknownDelivery() error {
	return &harness.DeliveryError{Uncertain: true, Message: "无法确认 Antigravity 是否收到输入，消息不会自动重发"}
}

func (r *runtime) promptComplete(t *logicalTurn, receipt chan<- error, p packet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != t {
		return nil
	}
	t.prompts--
	status, message := domain.ExecutionCompleted, ""
	var usage promptUsage
	switch {
	case p.Error != nil:
		status, message = domain.ExecutionFailed, fmt.Sprintf("Antigravity 返回执行错误（%d）", p.Error.Code)
		rejection := &harness.DeliveryError{Message: message}
		if receipt == nil {
			r.confirm(t, rejection)
		} else {
			receipt <- rejection
		}
	default:
		var result struct {
			StopReason string      `json:"stopReason"`
			Usage      promptUsage `json:"usage"`
		}
		if json.Unmarshal(p.Result, &result) != nil {
			result.StopReason = ""
		}
		switch result.StopReason {
		case "end_turn":
		case "cancelled":
			status = domain.ExecutionInterrupted
		case "max_tokens":
			status, message = domain.ExecutionFailed, "Antigravity 达到本轮输出上限"
		case "max_turn_requests":
			status, message = domain.ExecutionFailed, "Antigravity 达到本轮请求上限"
		case "refusal":
			status, message = domain.ExecutionFailed, "Antigravity 拒绝了该请求"
		default:
			status, message = domain.ExecutionFailed, "Antigravity 未报告可识别的结束原因"
		}
		usage = result.Usage
		if receipt == nil {
			r.confirm(t, nil)
		} else {
			receipt <- nil
		}
	}
	usage.accumulate(&t.turn)
	if t.outcome == "" || status == domain.ExecutionFailed || t.outcome == domain.ExecutionCompleted && status == domain.ExecutionInterrupted {
		t.outcome, t.message = status, message
	}
	// Replies are a stream boundary, even if another prompt still owns the turn.
	if err := r.finishStream(t); err != nil {
		return err
	}
	return r.finishIfSettled(t)
}

type promptUsage struct {
	InputTokens, OutputTokens, CachedReadTokens *int64
}

func (u promptUsage) accumulate(t *domain.Turn) {
	add := func(total **int64, v *int64) {
		if v == nil || *v < 0 {
			return
		}
		next := *v
		if *total != nil {
			next += **total
		}
		*total = &next
	}
	add(&t.InputTokens, u.InputTokens)
	add(&t.OutputTokens, u.OutputTokens)
	add(&t.CachedInputTokens, u.CachedReadTokens)
}

func (r *runtime) confirm(t *logicalTurn, err error) {
	if !t.confirmed {
		t.confirmed = true
		t.primary <- err
	}
}

func (r *runtime) finishIfSettled(t *logicalTurn) error {
	if t.prompts != 0 || len(r.requests) != 0 {
		return nil
	}
	for _, tool := range t.tools {
		if tool.Final {
			continue
		}
		tool.Final, tool.Status = true, "unknown"
		if t.outcome == domain.ExecutionInterrupted {
			tool.Status = "interrupted"
		}
		if err := r.emit(domain.Event{Type: "item.upsert", Item: &tool}); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	duration := now.Sub(*t.turn.StartedAt).Milliseconds()
	terminal := t.turn
	terminal.CompletedAt, terminal.DurationMS = &now, &duration
	terminal.Status, terminal.Error = t.outcome, t.message
	r.active = nil
	r.settledTurn = terminal.ID
	return r.emit(domain.Event{Type: "turn", Turn: &terminal, GoalProgress: t.goal})
}

func (r *runtime) finishStream(t *logicalTurn) error {
	item := t.stream
	t.stream = nil
	if item == nil {
		return nil
	}
	item.Final, item.Status = true, "completed"
	snapshot := *item
	return r.emit(domain.Event{Type: "item.upsert", Item: &snapshot})
}

func (r *runtime) Interrupt(ctx context.Context, s domain.Session, turn string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.active
	if r.session.ID != s.ID || r.session.ThreadID != s.ThreadID {
		return errors.New("目标回复已变更")
	}
	if t == nil && r.settledTurn == turn {
		return nil
	}
	if t == nil || t.turn.ID != turn {
		return errors.New("目标回复已结束或变更")
	}
	if err := r.process.send(ctx, 0, "session/cancel", sessionID(s.ThreadID)); err != nil {
		return err
	}
	t.interrupted = true
	for key, p := range r.requests {
		if err := r.resolvePermission(ctx, key, p, ""); err != nil {
			return err
		}
	}
	return nil
}

func (r *runtime) PendingOperations() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := len(r.requests)
	if r.active != nil {
		pending += r.active.prompts
	}
	return pending
}

func (r *runtime) emit(event domain.Event) error {
	if r.options.OnEvent == nil {
		return nil
	}
	event.SessionID = r.session.ID
	if event.Turn != nil {
		snapshot := *event.Turn
		event.Turn = &snapshot
	}
	return r.options.OnEvent(event)
}

func clip(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
