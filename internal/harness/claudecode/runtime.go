package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type runtime struct {
	opts         harness.StartOptions
	ready        domain.Readiness
	config       configuration
	binary, home string
	env          []string
	mu           sync.Mutex
	writeMu      sync.Mutex
	process      *exec.Cmd
	input        io.WriteCloser
	stopProxy    func()
	done         chan struct{}
	stopOnce     sync.Once
	session      domain.Session
	turn         *activeTurn
	approvals    map[string]approval
	closing      bool
}

type activeTurn struct {
	turn        domain.Turn
	uuid        string
	interrupted bool
	receipt     chan error
	messageID   string
	items       map[string]domain.Item
	tools       map[string]string
}
type approval struct {
	request domain.PendingRequest
	native  string
	input   json.RawMessage
}
type content struct {
	Type, ID, Name, Text, Thinking string
	ToolUseID                      string `json:"tool_use_id"`
	Input                          json.RawMessage
	Content                        json.RawMessage
	IsError                        bool `json:"is_error"`
}
type frame struct {
	Type, Subtype, UUID string
	SessionID           string `json:"session_id"`
	RequestID           string `json:"request_id"`
	IsReplay            bool   `json:"isReplay"`
	IsError             bool   `json:"is_error"`
	Request             struct {
		Subtype   string
		ToolName  string `json:"tool_name"`
		ToolUseID string `json:"tool_use_id"`
		Input     json.RawMessage
	}
	Response struct {
		Subtype   string
		RequestID string `json:"request_id"`
	}
	Message struct {
		ID      string
		Content []content
	}
	Event struct {
		Type         string
		Index        int
		Message      struct{ ID string }
		ContentBlock content `json:"content_block"`
		Delta        struct{ Type, Text, Thinking string }
	}
	Usage struct {
		Input  int64 `json:"input_tokens"`
		Output int64 `json:"output_tokens"`
		Cache  int64 `json:"cache_read_input_tokens"`
	}
}

func (r *runtime) Readiness() domain.Readiness { return r.ready }
func (r *runtime) Done() <-chan struct{}       { return r.done }
func (r *runtime) Stopped() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
func (r *runtime) ManagedCoverage() bool { return r.opts.Scope != nil }
func (r *runtime) ExecutionReady() bool  { return !r.opts.CatalogOnly && !r.Stopped() }
func (r *runtime) Validate(c domain.RunConfig) error {
	if c.Model != r.config.Model || c.Plan || c.Fast || c.Effort != "" || c.Permission != "" && c.Permission != "default" {
		return errors.New("此 Claude Code Profile 不支持所选运行配置")
	}
	return nil
}
func (r *runtime) Create(ctx context.Context, s domain.Session) (string, error) {
	s.ThreadID = domain.NewID()
	return s.ThreadID, r.start(ctx, s, false)
}
func (r *runtime) Resume(ctx context.Context, s domain.Session) error {
	if len(s.ThreadID) != 36 || strings.ContainsAny(s.ThreadID, "/\\\x00") {
		return errors.New("Claude Code 会话标识无效")
	}
	return r.start(ctx, s, true)
}
func (r *runtime) Observes(s domain.Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.Stopped() && r.session.ID == s.ID && r.session.ThreadID == s.ThreadID && r.process != nil
}

func (r *runtime) start(ctx context.Context, s domain.Session, resume bool) error {
	if err := r.Validate(s.Config); err != nil {
		return err
	}
	if !r.ExecutionReady() || !filepath.IsAbs(s.Context.CWD) {
		return harness.Unsupported("claude-execution")
	}
	r.mu.Lock()
	if r.process != nil {
		matches := r.session.ID == s.ID && r.session.ThreadID == s.ThreadID
		r.mu.Unlock()
		if matches {
			return nil
		}
		return errors.New("Claude Code 运行环境已绑定其他会话")
	}
	endpoint, token, stopProxy, err := r.config.connect()
	if err != nil {
		r.mu.Unlock()
		return errors.New("无法准备 Claude Code 的模型连接")
	}
	export := slices.DeleteFunc(slices.Clone(r.env), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_") ||
			name == "NODE_OPTIONS" || name == "DISABLE_AUTOUPDATER" || name == "CI"
	})
	export = append(export, "CLAUDE_CONFIG_DIR="+r.home, "ANTHROPIC_BASE_URL="+endpoint, "ANTHROPIC_API_KEY="+token,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0")
	allowed := append([]string{s.Context.CWD}, s.Context.AdditionalDirectories...)
	settings, _ := json.Marshal(map[string]any{"sandbox": map[string]any{
		"enabled": true, "failIfUnavailable": true, "allowUnsandboxedCommands": false, "autoAllowBashIfSandboxed": false,
		"filesystem": map[string]any{"denyRead": []string{"/Users"}, "allowRead": allowed, "allowWrite": allowed},
	}})
	args := []string{r.binary, "--print", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--replay-user-messages", "--include-partial-messages", "--permission-prompt-tool", "stdio", "--permission-prompts", "host",
		"--restricted", "--no-chrome", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--permission-mode", "default", "--tools", "Read,Glob,Grep,Edit,Write,Bash", "--model", r.config.Model, "--settings", string(settings)}
	if len(s.Context.AdditionalDirectories) > 0 {
		args = append(args, "--add-dir")
		args = append(args, s.Context.AdditionalDirectories...)
	}
	if resume {
		args = append(args, "--resume", s.ThreadID)
	} else {
		args = append(args, "--session-id", s.ThreadID)
	}
	var process *exec.Cmd
	var input io.WriteCloser
	var output io.Reader
	if r.opts.Scope != nil {
		argv := append([]string{"/bin/sh", "-c", `cd "$1" && shift && exec "$@"`, "shioji-claude", s.Context.CWD}, args...)
		process, input, output, err = r.opts.Scope.StartOwned(ctx, s.ID, argv, export, r.opts.OnStart)
	} else {
		process = exec.Command(args[0], args[1:]...)
		process.Dir, process.Env, process.Stderr = s.Context.CWD, export, io.Discard
		process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		input, err = process.StdinPipe()
		if err == nil {
			output, err = process.StdoutPipe()
		}
		if err == nil {
			err = process.Start()
		}
	}
	if err != nil {
		stopProxy()
		r.mu.Unlock()
		return errors.New("无法启动 Claude Code")
	}
	r.process, r.input, r.session, r.stopProxy = process, input, s, stopProxy
	r.mu.Unlock()

	ready := make(chan error, 1)
	initID := domain.NewID()
	go r.receive(output, initID, ready)
	if err = r.send(map[string]any{"type": "control_request", "request_id": initID, "request": map[string]string{"subtype": "initialize"}}); err != nil {
		_ = r.Close(ctx)
		return errors.New("无法初始化 Claude Code 协议")
	}
	select {
	case err = <-ready:
	case <-r.done:
		err = errors.New("Claude Code 初始化时退出")
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		_ = r.Close(ctx)
	}
	return err
}

const maxClaudeFrame = 24 << 20

func (r *runtime) send(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) >= maxClaudeFrame {
		return errors.New("Claude Code 消息超出协议大小限制")
	}
	data = append(data, '\n')
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	n, err := r.input.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

func (r *runtime) Submit(ctx context.Context, s domain.Session, id string, in harness.TurnInput) (string, error) {
	ref, receipt, err := r.SubmitDeferred(ctx, s, id, in, nil)
	if err != nil {
		return ref, err
	}
	select {
	case err = <-receipt:
		return ref, err
	case <-ctx.Done():
		return ref, deliveryUnknown()
	}
}

func deliveryUnknown() error {
	return &harness.DeliveryError{Uncertain: true, Message: "Claude Code 输入接纳结果待核对"}
}

func (r *runtime) SubmitDeferred(ctx context.Context, s domain.Session, _ string, in harness.TurnInput, claim func() error) (string, <-chan error, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	blocks, err := inputBlocks(in)
	if err != nil {
		return "", nil, err
	}
	if !r.Observes(s) {
		return "", nil, errors.New("Claude Code 会话未连接")
	}
	if claim != nil {
		if err := claim(); err != nil {
			return "", nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turn != nil || r.Stopped() || r.closing {
		return "", nil, &harness.DeliveryError{Message: "Claude Code 运行环境当前无法接纳新输入"}
	}
	now := time.Now().UTC()
	t := &activeTurn{turn: domain.Turn{ID: domain.NewID(), Status: domain.ExecutionRunning, StartedAt: &now},
		uuid: domain.NewID(), receipt: make(chan error, 1), items: map[string]domain.Item{}, tools: map[string]string{}}
	r.turn = t
	event := map[string]any{"type": "user", "uuid": t.uuid, "session_id": s.ThreadID, "parent_tool_use_id": nil,
		"message": map[string]any{"role": "user", "content": blocks}}
	if err := r.send(event); err != nil {
		return t.turn.ID, nil, deliveryUnknown()
	}
	if err := r.emit(domain.Event{Type: "turn", Turn: &t.turn}); err != nil {
		return t.turn.ID, nil, deliveryUnknown()
	}
	return t.turn.ID, t.receipt, nil
}

func (r *runtime) Interrupt(ctx context.Context, s domain.Session, turn string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turn == nil || r.turn.turn.ID != turn || r.session.ThreadID != s.ThreadID {
		return errors.New("回复已结束")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.turn.interrupted = true
	return r.send(map[string]any{"type": "control_request", "request_id": domain.NewID(), "request": map[string]string{"subtype": "interrupt"}})
}

func (r *runtime) Respond(a domain.Answer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.approvals[a.RequestID]
	if !ok || p.request.Revision != a.Revision || r.turn == nil || r.turn.interrupted {
		return errors.New("审批已失效")
	}
	var response map[string]any
	switch a.Decision {
	case "accept":
		response = map[string]any{"behavior": "allow", "updatedInput": p.input}
	case "decline":
		response = map[string]any{"behavior": "deny", "message": "The user declined this tool request."}
	default:
		return errors.New("审批选项不可用")
	}
	if err := r.reply(p.native, response); err != nil {
		return err
	}
	return r.resolve(p)
}

func (r *runtime) reply(id string, response map[string]any) error {
	return r.send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": response}})
}

func (r *runtime) resolve(p approval) error {
	delete(r.approvals, p.request.ID)
	p.request.State = domain.RequestResolved
	return r.emit(domain.Event{Type: "request.resolve", Request: &p.request})
}

func (r *runtime) Inspect(_ context.Context, s domain.Session) (domain.RecoveryHistory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session.ID != s.ID || r.session.ThreadID != s.ThreadID || r.Stopped() {
		return domain.RecoveryHistory{State: domain.SourceUnavailable}, nil
	}
	state := domain.SourceIdle
	if r.turn != nil {
		state = domain.SourceActive
	}
	return domain.RecoveryHistory{Attached: r.process != nil, State: state}, nil
}
func (r *runtime) PrepareHistory(context.Context, domain.Session, harness.HistoryBoundary) (string, error) {
	return "", harness.Unsupported("history-edit")
}
func (r *runtime) ActivateHistory(context.Context, domain.Session, string, func() error) error {
	return harness.Unsupported("history-edit")
}
func (r *runtime) SteerDeferred(context.Context, domain.Session, string, string, harness.TurnInput, func() error) (<-chan error, error) {
	return nil, harness.Unsupported("steer")
}
func (r *runtime) Steer(context.Context, domain.Session, string, string, harness.TurnInput) error {
	return harness.Unsupported("steer")
}

func (r *runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.Stopped() {
		r.mu.Unlock()
		return nil
	}
	r.closing = true
	process, input := r.process, r.input
	r.mu.Unlock()
	if process == nil {
		r.stopOnce.Do(func() { close(r.done) })
		return nil
	}
	_ = input.Close()
	if r.opts.Scope != nil {
		if err := r.opts.Scope.Terminate(ctx, true); err != nil {
			return err
		}
	} else {
		_ = syscall.Kill(-process.Process.Pid, syscall.SIGTERM)
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *runtime) emit(e domain.Event) error {
	e.SessionID = r.session.ID
	if r.opts.OnEvent != nil {
		return r.opts.OnEvent(e)
	}
	return nil
}

func (r *runtime) receive(output io.Reader, initID string, ready chan<- error) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), maxClaudeFrame)
	protocolError := false
	for scanner.Scan() {
		var in frame
		if json.Unmarshal(scanner.Bytes(), &in) != nil {
			protocolError = true
			break
		}
		if initID != "" && in.Type == "control_response" && in.Response.RequestID == initID {
			if in.Response.Subtype == "success" {
				ready <- nil
			} else {
				ready <- errors.New("Claude Code 拒绝了协议初始化")
			}
			initID = ""
			continue
		}
		r.mu.Lock()
		err := r.processFrame(in)
		r.mu.Unlock()
		if err != nil {
			protocolError = true
			break
		}
	}
	if protocolError || scanner.Err() != nil {
		if r.opts.Scope != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.opts.Scope.Terminate(ctx, true)
			cancel()
		}
		_ = r.process.Process.Kill()
	}
	_ = r.input.Close()
	_ = r.process.Wait()
	r.stopProxy()

	r.mu.Lock()
	unexpected, bound := !r.closing, r.session.ID != ""
	if r.turn != nil {
		r.confirm(r.turn, deliveryUnknown())
	}
	if unexpected {
		for id, p := range r.approvals {
			p.request.State = domain.RequestInvalidated
			p.request.Revision++
			_ = r.emit(domain.Event{Type: "request.resolve", Request: &p.request})
			delete(r.approvals, id)
		}
	}
	r.stopOnce.Do(func() { close(r.done) })
	r.mu.Unlock()
	if unexpected {
		if bound && r.opts.OnEvent != nil {
			_ = r.emit(domain.Event{Type: "runtime.lost"})
		} else if r.opts.OnLost != nil {
			_ = r.opts.OnLost()
		}
	}
}

func (r *runtime) confirm(t *activeTurn, err error) {
	if t.receipt != nil {
		t.receipt <- err
		t.receipt = nil
	}
}

func (r *runtime) processFrame(in frame) error {
	t := r.turn
	if in.Type == "control_request" {
		if in.Request.Subtype != "can_use_tool" || t == nil || t.interrupted || in.SessionID != "" && in.SessionID != r.session.ThreadID {
			return r.reply(in.RequestID, map[string]any{"behavior": "deny", "message": "Tool permission is unavailable."})
		}
		r.confirm(t, nil)
		body := strings.TrimSpace(string(in.Request.Input))
		if len(body) > 4000 {
			return r.reply(in.RequestID, map[string]any{"behavior": "deny", "message": "Tool input is too long to display in full for human approval."})
		}
		body = strings.ReplaceAll(body, r.config.Key, "••••")
		p := approval{native: in.RequestID, input: in.Request.Input, request: domain.PendingRequest{
			ID: domain.NewID(), SessionID: r.session.ID, TurnID: t.turn.ID, ItemID: t.tools[in.Request.ToolUseID], Kind: "approval",
			Title: in.Request.ToolName, Text: body, Revision: 1, State: domain.RequestPending,
			Choices: []domain.RequestChoice{{ID: "accept", Label: "批准本次"}, {ID: "decline", Label: "拒绝"}},
		}}
		r.approvals[p.request.ID] = p
		return r.emit(domain.Event{Type: "request.open", Request: &p.request})
	}
	if in.Type == "control_cancel_request" {
		for _, p := range r.approvals {
			if p.native == in.RequestID {
				return r.resolve(p)
			}
		}
		return nil
	}
	if t == nil || in.SessionID != "" && in.SessionID != r.session.ThreadID {
		return nil
	}
	switch in.Type {
	case "user":
		if in.UUID == t.uuid && in.IsReplay {
			r.confirm(t, nil)
			return nil
		}
		for _, block := range in.Message.Content {
			id := t.tools[block.ToolUseID]
			if block.Type != "tool_result" || id == "" {
				continue
			}
			item := t.items[id]
			item.Final, item.Status = true, "completed"
			if block.IsError {
				item.Status = "failed"
			}
			var text string
			if json.Unmarshal(block.Content, &text) != nil {
				var parts []struct{ Text string }
				if json.Unmarshal(block.Content, &parts) == nil {
					for _, part := range parts {
						text += part.Text
					}
				}
			}
			item.Parts = []domain.ContentPart{{Kind: "text", Text: text}}
			if err := r.item(t, item, false); err != nil {
				return err
			}
		}
	case "stream_event":
		r.confirm(t, nil)
		switch in.Event.Type {
		case "message_start":
			t.messageID = in.Event.Message.ID
		case "content_block_start":
			return r.block(t, t.messageID, in.Event.Index, in.Event.ContentBlock, false)
		case "content_block_delta":
			id := blockID(t, t.messageID, in.Event.Index)
			item, ok := t.items[id]
			if !ok {
				return nil
			}
			item.Text += in.Event.Delta.Text + in.Event.Delta.Thinking
			return r.item(t, item, false)
		}
	case "assistant":
		r.confirm(t, nil)
		for index, block := range in.Message.Content {
			if err := r.block(t, in.Message.ID, index, block, true); err != nil {
				return err
			}
		}
	case "result":
		r.confirm(t, nil)
		for _, p := range r.approvals {
			if err := r.resolve(p); err != nil {
				return err
			}
		}
		for _, item := range t.items {
			if !item.Final {
				item.Final = true
				if t.interrupted {
					item.Status = "interrupted"
				} else {
					item.Status = "completed"
				}
				if err := r.item(t, item, false); err != nil {
					return err
				}
			}
		}
		now := time.Now().UTC()
		duration := now.Sub(*t.turn.StartedAt).Milliseconds()
		terminal := t.turn
		terminal.CompletedAt, terminal.DurationMS = &now, &duration
		terminal.InputTokens, terminal.OutputTokens, terminal.CachedInputTokens = &in.Usage.Input, &in.Usage.Output, &in.Usage.Cache
		terminal.Status = domain.ExecutionCompleted
		if t.interrupted {
			terminal.Status = domain.ExecutionInterrupted
		} else if in.IsError || in.Subtype != "success" {
			terminal.Status, terminal.Error = domain.ExecutionFailed, "Claude Code 未能完成本轮，请检查模型服务或设备设置"
		}
		r.turn = nil
		return r.emit(domain.Event{Type: "turn", Turn: &terminal})
	}
	return nil
}

func blockID(t *activeTurn, message string, index int) string {
	return fmt.Sprintf("%s:%s:%d", t.turn.ID, message, index)
}
func (r *runtime) block(t *activeTurn, message string, index int, c content, final bool) error {
	id := blockID(t, message, index)
	item := domain.Item{ID: id, TurnID: t.turn.ID, Status: "running", Final: final}
	switch c.Type {
	case "text":
		item.Kind, item.Text = "assistant", c.Text
	case "thinking":
		item.Kind, item.Text = "reasoning", c.Thinking
	case "tool_use":
		if c.ID != "" {
			t.tools[c.ID] = id
		}
		item.Kind, item.Name, item.Final = "tool", c.Name, false
		if len(c.Input) > 0 {
			item.Text = string(c.Input)
		}
	default:
		return nil
	}
	if item.Final {
		item.Status = "completed"
	}
	_, exists := t.items[id]
	return r.item(t, item, !exists)
}
func (r *runtime) item(t *activeTurn, item domain.Item, start bool) error {
	if r.config.Key != "" {
		item.Text = strings.ReplaceAll(item.Text, r.config.Key, "••••")
		for i := range item.Parts {
			item.Parts[i].Text = strings.ReplaceAll(item.Parts[i].Text, r.config.Key, "••••")
		}
	}
	t.items[item.ID] = item
	return r.emit(domain.Event{Type: "item.upsert", Item: &item, ActivityStart: start})
}

var _ harness.Runtime = (*runtime)(nil)
var _ harness.DeferredInputRuntime = (*runtime)(nil)
