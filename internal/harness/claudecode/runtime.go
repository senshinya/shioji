package claudecode

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

//go:embed agent-sdk.mjs
var agentSDKBridge string

type runtime struct {
	opts            harness.StartOptions
	ready           domain.Readiness
	config          configuration
	binary, home    string
	externalHome    string
	privateProfiles string
	version         string
	catalogVersion  string
	node, sdk       string
	env             []string
	title           bool
	mu              sync.Mutex
	writeMu         sync.Mutex
	process         *exec.Cmd
	input           io.WriteCloser
	stopProxy       func()
	done            chan struct{}
	stopOnce        sync.Once
	session         domain.Session
	turn            *activeTurn
	lastResult      completedResult
	canCompact      atomic.Bool
	approvals       map[string]approval
	closing         bool
	mcp             map[string]mcpObservation
	mcpRevision     string
	goalServer      *goalServer
	work            map[string]domain.WorkActivity
	workCalls       map[string]string
	workPlan        map[string]bool
	workReplies     map[string]chan bool
}

type activeTurn struct {
	turn          domain.Turn
	uuid          string
	operation     string
	plan          bool
	stopping      bool
	interrupted   bool
	receipt       chan error
	messageID     string
	items         map[string]domain.Item
	tools         map[string]string
	errorCode     string
	limit         *domain.UsageLimit
	compacted     bool
	pendingSteer  map[string]steerInput
	acceptedSteer map[string]string
	heldResult    *frame
	segmentUsage  inputUsage
	goal          *domain.GoalProgress
	goalKey       string
	planEmitted   map[string]bool
}

type completedResult struct {
	nativeID, turnID string
}

type inputUsage struct {
	Input  int64 `json:"input_tokens"`
	Output int64 `json:"output_tokens"`
	Cache  int64 `json:"cache_read_input_tokens"`
}

type approval struct {
	request   domain.PendingRequest
	native    string
	agent     string
	questions []nativeQuestion
	receipt   chan error
}
type content struct {
	Type, ID, Name, Text, Thinking string
	ToolUseID                      string `json:"tool_use_id"`
	Input                          json.RawMessage
	Content                        json.RawMessage
	IsError                        bool `json:"is_error"`
}

// Native command records can carry a plain string instead of content blocks.
type contentBlocks []content

func (blocks *contentBlocks) UnmarshalJSON(data []byte) error {
	if len(data) != 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*blocks = contentBlocks{{Type: "text", Text: text}}
		return nil
	}
	return json.Unmarshal(data, (*[]content)(blocks))
}

type frame struct {
	Type, Subtype, UUID          string
	OK                           bool
	SessionID                    string   `json:"session_id"`
	RequestID                    string   `json:"request_id"`
	IsReplay                     bool     `json:"isReplay"`
	UserMessageUUID              string   `json:"user_message_uuid"`
	UserMessageUUIDs             []string `json:"user_message_uuids"`
	CommandUUID                  string   `json:"command_uuid"`
	State                        string
	TaskID                       string `json:"task_id"`
	AgentID                      string `json:"agent_id"`
	ParentToolUseID              string `json:"parent_tool_use_id"`
	TaskType                     string `json:"task_type"`
	Background                   bool   `json:"is_backgrounded"`
	Status, Description, Summary string
	Origin                       struct{ Kind string }
	IsError                      bool            `json:"is_error"`
	ErrorCode                    json.RawMessage `json:"error"`
	UsedTokens                   *int64          `json:"used_tokens"`
	LimitTokens                  *int64          `json:"limit_tokens"`
	RateLimit                    struct {
		Status   string
		ResetsAt int64 `json:"resetsAt"`
	} `json:"rate_limit_info"`
	ToolName      string          `json:"tool_name"`
	ToolUseID     string          `json:"tool_use_id"`
	Input         json.RawMessage `json:"input"`
	ReadyCommands []string        `json:"ready_commands"`
	Servers       []mcpObservation
	MCPRevision   string `json:"mcp_revision"`
	Message       struct {
		ID      string
		Content contentBlocks
	}
	Event struct {
		Type         string
		Index        int
		Message      struct{ ID string }
		ContentBlock content `json:"content_block"`
		Delta        struct{ Type, Text, Thinking string }
	}
	Usage inputUsage
}

func (r *runtime) Readiness() domain.Readiness {
	ready := r.ready
	ready.Capabilities.Compact = r.canCompact.Load()
	return ready
}
func (r *runtime) Done() <-chan struct{} { return r.done }
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
	invalid := errors.New("此 Claude Code Profile 不支持所选运行配置")
	if c.Model != r.config.Model || c.Permission != "" && c.Permission != "default" {
		return invalid
	}
	if c.Fast || c.Effort != "" {
		if len(r.ready.Models) != 1 || c.Fast && !r.ready.Models[0].Fast ||
			c.Effort != "" && !slices.Contains(r.ready.Models[0].Efforts, c.Effort) {
			return invalid
		}
	}
	return nil
}
func (r *runtime) Create(ctx context.Context, s domain.Session) (string, error) {
	s.ThreadID = domain.NewID()
	return s.ThreadID, r.start(ctx, s, false)
}
func (r *runtime) Resume(ctx context.Context, s domain.Session) error {
	if !nativeUUID.MatchString(s.ThreadID) {
		return errors.New("Claude Code 会话标识无效")
	}
	if saved, err := r.forkRecord(s.ThreadID); err == nil && saved.Empty {
		// A rewritten first message has no history until native delivery.
		// The SDK can only resume after that transcript exists.
		var history []historyRecord
		if err := r.runHistorySDK(ctx, historyRequest{Action: "boundaries", Thread: s.ThreadID, Directory: s.Context.CWD}, &history); err != nil {
			return err
		}
		if len(history) == 0 {
			return r.start(ctx, s, false)
		}
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
	tools, revision, err := r.executionMCP()
	if err != nil {
		return err
	}
	hooks, err := r.executionHooks()
	if err != nil {
		return err
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
			strings.HasPrefix(name, "NODE_") || name == "DISABLE_AUTOUPDATER" || name == "CI"
	})
	export = append(export, "CLAUDE_CONFIG_DIR="+r.home, "ANTHROPIC_BASE_URL="+endpoint, "ANTHROPIC_API_KEY="+token,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0")
	allowed := append([]string{s.Context.CWD}, s.Context.AdditionalDirectories...)
	readable := slices.Clone(allowed)
	additional := slices.Clone(s.Context.AdditionalDirectories)
	denyRead := []string{"/Users"}
	var protected, rules []string
	privateProfiles := r.privateProfiles
	if privateProfiles == "" {
		privateProfiles = r.home
	}
	if filepath.IsAbs(privateProfiles) {
		denyRead = append(denyRead, privateProfiles)
		protected = append(protected, privateProfiles)
		glob := "(//" + strings.TrimPrefix(privateProfiles, "/") + "/**)"
		rules = append(rules, "Read"+glob, "Edit"+glob)
	}
	if !r.title {
		attachments := r.attachmentDirectory(s)
		if err := os.MkdirAll(attachments, 0700); err != nil {
			stopProxy()
			r.mu.Unlock()
			return errors.New("无法准备 Claude Code 附件目录")
		}
		readable, additional = append(readable, attachments), append(additional, attachments)
		protected = append(protected, attachments)
		glob := "(//" + strings.TrimPrefix(attachments, "/") + "/**)"
		rules = append(rules, "Edit"+glob)
	}
	settings := map[string]any{"sandbox": map[string]any{
		"enabled": true, "failIfUnavailable": true, "allowUnsandboxedCommands": false, "autoAllowBashIfSandboxed": false,
		"filesystem": map[string]any{"denyRead": denyRead, "allowRead": readable, "allowWrite": allowed, "denyWrite": protected},
	}}
	if len(rules) > 0 {
		settings["permissions"] = map[string]any{"deny": rules}
	}
	if len(hooks) > 0 {
		settings["hooks"] = hooks
	}
	args := []string{r.node, "--input-type=module", "--eval", agentSDKBridge, "--", r.sdk}
	var process *exec.Cmd
	var input io.WriteCloser
	var output io.Reader
	if r.opts.Scope != nil {
		argv := append([]string{"/bin/sh", "-c", `cd "$1" && shift && exec "$@"`, "shioji-claude-sdk", s.Context.CWD}, args...)
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
	go r.receive(output, ready)
	if err = r.send(map[string]any{
		"type": "start", "binary": r.binary, "thread": s.ThreadID, "resume": resume,
		"title": r.title, "configuration": claudeTurnConfig(s.Config),
		"model": r.config.Model, "directories": additional, "settings": settings,
		"mcp": tools, "mcp_revision": revision,
	}); err != nil {
		_ = r.Close(ctx)
		return errors.New("无法初始化 Claude Agent SDK")
	}
	select {
	case err = <-ready:
	case <-r.done:
		err = errors.New("Claude Agent SDK 初始化时退出")
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
	if r.input == nil {
		return io.ErrClosedPipe
	}
	if pipe, ok := r.input.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if pipe.SetWriteDeadline(time.Now().Add(5*time.Second)) == nil {
			defer pipe.SetWriteDeadline(time.Time{})
		}
	}
	n, err := r.input.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

func (r *runtime) Submit(ctx context.Context, s domain.Session, id string, in harness.TurnInput) (string, error) {
	return r.SubmitWithHandoff(ctx, s, id, in, nil)
}

func (r *runtime) SubmitWithHandoff(ctx context.Context, s domain.Session, id string, in harness.TurnInput, claim func() error) (string, error) {
	ref, receipt, err := r.SubmitDeferred(ctx, s, id, in, claim)
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

func (r *runtime) SubmitDeferred(ctx context.Context, s domain.Session, operation string, in harness.TurnInput, claim func() error) (string, <-chan error, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	input, err := r.prepareInput(ctx, s, in)
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
	if r.session.ID != s.ID || r.session.ThreadID != s.ThreadID || r.process == nil {
		return "", nil, &harness.DeliveryError{Message: "Claude Code 会话已变化，输入未发送"}
	}
	return r.beginPrompt(ctx, operation, input, "", s.Config)
}

// The caller holds mu through local admission and the transport write.
func (r *runtime) beginPrompt(ctx context.Context, operation string, input preparedInput, presentation string, config domain.RunConfig) (string, <-chan error, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if r.turn != nil || r.Stopped() || r.closing {
		return "", nil, &harness.DeliveryError{Message: "Claude Code 运行环境当前无法接纳新输入"}
	}
	if err := r.Validate(config); err != nil {
		return "", nil, &harness.DeliveryError{Message: err.Error()}
	}
	if r.version != "" {
		version, err := profileVersion(r.home)
		if err != nil || version != r.version {
			return "", nil, &harness.DeliveryError{Message: "Claude Code Profile 已变化，需在空闲后重新加载"}
		}
	}
	tools, revision, err := r.executionMCP()
	if err != nil {
		return "", nil, &harness.DeliveryError{Message: err.Error()}
	}
	if input.Goal != nil && r.goalServer == nil {
		r.goalServer, err = r.openGoalServer()
		if err != nil {
			return "", nil, &harness.DeliveryError{Message: "无法准备 Claude Code 目标进度工具"}
		}
	}
	now := time.Now().UTC()
	inputID := nativeInputID(r.session.ThreadID, operation)
	t := &activeTurn{turn: domain.Turn{ID: inputID, Status: domain.ExecutionRunning, StartedAt: &now, Presentation: presentation},
		uuid: inputID, operation: operation, plan: config.Plan, receipt: make(chan error, 1), items: map[string]domain.Item{}, tools: map[string]string{}}
	t.goal, t.goalKey = input.Goal, input.GoalKey
	if t.goal != nil && strings.HasPrefix(operation, domain.GoalContinuationPrefix) {
		t.turn.Presentation = domain.GoalContinuationPresentation
	}
	r.turn = t
	event := map[string]any{"type": "prompt", "thread": r.session.ThreadID, "uuid": t.uuid, "content": input.Blocks, "configuration": claudeTurnConfig(config)}
	addSkillCommand(event, input)
	if revision != "" {
		event["mcp"], event["mcp_revision"] = tools, revision
	}
	if input.GoalKey != "" {
		event["goal_key"], event["goal_server"] = input.GoalKey, r.goalServer.config
	}
	if err := r.send(event); err != nil {
		return t.turn.ID, nil, deliveryUnknown()
	}
	if err := r.emit(domain.Event{Type: "turn", Turn: &t.turn}); err != nil {
		return t.turn.ID, nil, deliveryUnknown()
	}
	if presentation == "compaction" {
		if err := r.compactionActivity(t, domain.ExecutionRunning); err != nil {
			return t.turn.ID, nil, deliveryUnknown()
		}
	}
	return t.turn.ID, t.receipt, nil
}

func claudeTurnConfig(config domain.RunConfig) map[string]any {
	return map[string]any{"plan": config.Plan, "effort": config.Effort, "fast": config.Fast}
}

func nativeInputID(thread, operation string) string {
	if operation == "" {
		return domain.NewID()
	}
	// The same durable worker operation addresses the same native prompt even
	// after restarting the adapter. Distinct native threads stay isolated.
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("shioji.claude.input\x00"+thread+"\x00"+operation)).String()
}

func (r *runtime) Interrupt(ctx context.Context, s domain.Session, turn string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turn == nil || r.turn.turn.ID != turn || r.session.ID != s.ID || r.session.ThreadID != s.ThreadID {
		return errors.New("回复已结束")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.turn.stopping {
		return nil
	}
	if err := r.send(map[string]string{"type": "interrupt", "thread": s.ThreadID, "uuid": r.turn.uuid}); err != nil {
		return &harness.DeliveryError{Uncertain: true, Message: "Claude Code 停止请求待核对"}
	}
	r.turn.stopping = true
	return nil
}

func (r *runtime) Respond(a domain.Answer) error {
	receipt, err := r.submitApproval(a)
	if err != nil {
		return err
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case err := <-receipt:
		return err
	case <-r.done:
	case <-timer.C:
	}
	select {
	case err := <-receipt:
		return err
	default:
		return &harness.DeliveryError{Uncertain: true, Message: "Claude Code 审批结果待核对"}
	}
}

func (r *runtime) submitApproval(a domain.Answer) (<-chan error, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.approvals[a.RequestID]
	if !ok || p.request.Revision != a.Revision || !r.canAnswer(p) {
		return nil, errors.New("审批已失效")
	}
	if p.receipt != nil {
		return nil, &harness.DeliveryError{Uncertain: true, Message: "Claude Code 审批仍待确认"}
	}
	if a.Decision != "accept" && a.Decision != "decline" && !(len(p.questions) > 0 && a.Decision == "cancel") {
		return nil, errors.New("审批选项不可用")
	}
	var answers map[string]string
	if a.Decision == "accept" && len(p.questions) > 0 {
		var err error
		answers, err = questionAnswers(p, a.Answers)
		if err != nil {
			return nil, err
		}
	}
	p.receipt = make(chan error, 1)
	r.approvals[a.RequestID] = p
	if err := r.reply(p.native, a.Decision == "accept", answers); err != nil {
		return nil, &harness.DeliveryError{Uncertain: true, Message: "Claude Code 审批结果待核对"}
	}
	return p.receipt, nil
}

func (r *runtime) canAnswer(p approval) bool {
	if r.closing || r.Stopped() {
		return false
	}
	if p.agent == "" {
		return r.turn != nil && r.turn.turn.ID == p.request.TurnID && !r.turn.stopping
	}
	work, ok := r.work[p.agent]
	return ok && work.ID == p.request.WorkID && work.Active() &&
		(r.turn == nil || r.turn.turn.ID != work.TurnID || !r.turn.stopping)
}

func (r *runtime) reply(id string, allow bool, answers map[string]string) error {
	return r.send(map[string]any{"type": "approval", "request_id": id, "allow": allow, "answers": answers})
}

func (r *runtime) resolve(p approval) error {
	delete(r.approvals, p.request.ID)
	p.request.State = domain.RequestResolved
	err := r.emit(domain.Event{Type: "request.resolve", Request: &p.request})
	p.receipt <- err
	return err
}

func (r *runtime) invalidateApproval(p approval) error {
	delete(r.approvals, p.request.ID)
	p.request.State = domain.RequestInvalidated
	p.request.Revision++
	if p.receipt != nil {
		p.receipt <- errors.New("Claude Code 已撤回此审批")
	}
	return r.emit(domain.Event{Type: "request.resolve", Request: &p.request})
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
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		if r.opts.Scope == nil {
			// EOF lets the SDK close and reap its CLI before we return.
			// Force the private process group down only if it fails to exit.
			_ = syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
		}
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

func (r *runtime) receive(output io.Reader, ready chan<- error) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), maxClaudeFrame)
	protocolError := false
	for scanner.Scan() {
		var in frame
		if json.Unmarshal(scanner.Bytes(), &in) != nil {
			protocolError = true
			break
		}
		if in.Type == "ready" && ready != nil {
			r.canCompact.Store(slices.Contains(in.ReadyCommands, "compact") || slices.Contains(in.ReadyCommands, "/compact"))
			ready <- nil
			ready = nil
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
		} else {
			_ = syscall.Kill(-r.process.Process.Pid, syscall.SIGKILL)
		}
		_ = r.process.Process.Kill()
	}
	_ = r.input.Close()
	_ = r.process.Wait()
	r.stopProxy()

	r.mu.Lock()
	if r.goalServer != nil {
		_ = r.goalServer.server.Close()
	}

	unexpected, bound := !r.closing, r.session.ID != ""
	if r.turn != nil {
		r.confirm(r.turn, deliveryUnknown())
		r.finishSteers(r.turn)
	}
	if unexpected {
		for id, work := range r.work {
			work.State, work.CanStop = "unknown", false
			_ = r.emit(domain.Event{Type: "work.upsert", Work: &work})
			delete(r.work, id)
		}
		for _, p := range r.approvals {
			_ = r.invalidateApproval(p)
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
		if err == nil {
			observed := t.turn
			observed.Status = domain.ExecutionUnknown
			r.recordReceipt(t.operation, observed)
		}
		t.receipt <- err
		t.receipt = nil
	}
}

func (r *runtime) processFrame(in frame) error {
	t := r.turn
	if !r.title && in.SessionID != "" && in.SessionID != r.session.ThreadID {
		return nil
	}
	if in.Type == "work_stopped" {
		if reply := r.workReplies[in.RequestID]; reply != nil {
			select {
			case reply <- in.OK:
			default:
			}
		}
		return nil
	}
	if in.Type == "system" && in.TaskID != "" {
		return r.observeWork(t, in)
	}
	if in.ParentToolUseID != "" {
		return r.observeWorkItem(in)
	}
	if in.Origin.Kind == "task-notification" {
		return nil
	}
	if in.Type == "mcp_status" {
		r.mcp = make(map[string]mcpObservation)
		r.mcpRevision = in.MCPRevision
		for _, status := range in.Servers {
			r.mcp[status.Name] = status
		}
		return nil
	}
	if in.Type == "approval" {
		// This callback can also belong to queued or delegated work. It
		// does not identify the user input that started the current turn.
		return r.openApproval(t, in)
	}
	if in.Type == "approval_cancelled" || in.Type == "approval_applied" {
		for _, p := range r.approvals {
			if p.native == in.RequestID {
				if in.Type == "approval_cancelled" {
					return r.invalidateApproval(p)
				}
				if p.receipt != nil {
					return r.resolve(p)
				}
				return nil
			}
		}
		return nil
	}
	if in.Type == "context_usage" {
		// The SDK can answer after the result was streamed. Ignore older
		// results after another turn starts so the meter can't move backward.
		if t == nil && in.UUID != "" && in.UUID == r.lastResult.nativeID {
			if in.UsedTokens != nil && *in.UsedTokens < 0 {
				in.UsedTokens = nil
			}
			if in.LimitTokens != nil && *in.LimitTokens <= 0 {
				in.LimitTokens = nil
			}
			if in.UsedTokens != nil || in.LimitTokens != nil {
				return r.emit(domain.Event{Type: "context.usage", ContextUsage: &domain.ContextUsage{
					TurnID: r.lastResult.turnID, UsedTokens: in.UsedTokens, LimitTokens: in.LimitTokens, UpdatedAt: time.Now().UTC(),
				}})
			}
		}
		return nil
	}
	if t == nil || !t.ownsFrame(in) {
		return nil
	}
	if err := r.observeSteers(t, in); err != nil {
		return err
	}
	if r.turn != t {
		return nil
	}
	switch in.Type {
	case "interrupt_applied":
		if t.stopping && in.UUID == t.uuid {
			t.interrupted = true
		}
	case "command_lifecycle":
		if in.CommandUUID == t.uuid && in.State == "started" {
			r.confirm(t, nil)
		}
	case "input_refused":
		if in.UUID == t.uuid {
			t.operation = ""
			t.errorCode = "skill_not_loaded"
			if t.goal != nil {
				t.errorCode = "goal_tool_unavailable"
			}
			r.confirm(t, &harness.DeliveryError{Message: claudeFailure(t.errorCode)})
			return r.processFrame(frame{Type: "result", IsError: true})
		}
	case "system":
		if in.Subtype == "compact_boundary" && t.turn.Presentation == "compaction" {
			t.compacted = true
		}
	case "rate_limit_event":
		if in.RateLimit.Status == "rejected" {
			t.limit = &domain.UsageLimit{}
			if in.RateLimit.ResetsAt > 0 {
				reset := time.Unix(in.RateLimit.ResetsAt, 0).UTC()
				t.limit.ResetAt = &reset
			}
		}
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
			item.Parts = r.toolResultParts(id, block.Content)
			if err := r.item(t, item, false); err != nil {
				return err
			}
		}
	case "stream_event":
		r.confirm(t, nil)
		if t.turn.Presentation == "compaction" {
			return nil
		}
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
		var code string
		if json.Unmarshal(in.ErrorCode, &code) == nil && code != "" {
			t.errorCode = code
			return nil // Provider error content may include private configuration.
		}
		t.errorCode = ""
		if t.turn.Presentation == "compaction" {
			return nil
		}
		for index, block := range in.Message.Content {
			if err := r.block(t, in.Message.ID, index, block, true); err != nil {
				return err
			}
		}
	case "result":
		r.confirm(t, nil)
		if len(t.pendingSteer) > 0 && !t.interrupted {
			held := in
			t.heldResult = &held
			r.watchSteerDisposition(t, &held)
			return nil
		}
		r.finishSteers(t)
		for _, p := range r.approvals {
			if p.agent == "" && p.request.TurnID == t.turn.ID {
				if err := r.invalidateApproval(p); err != nil {
					return err
				}
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
		accumulateUsage(&in.Usage, t.segmentUsage)
		terminal.InputTokens, terminal.OutputTokens, terminal.CachedInputTokens = &in.Usage.Input, &in.Usage.Output, &in.Usage.Cache
		terminal.Status = domain.ExecutionCompleted
		if t.interrupted {
			terminal.Status = domain.ExecutionInterrupted
		} else if in.IsError || in.Subtype != "success" || t.errorCode != "" {
			terminal.Status, terminal.Error = domain.ExecutionFailed, claudeFailure(t.errorCode)
			if t.errorCode == "rate_limit" || t.errorCode == "" && t.limit != nil {
				terminal.Error = claudeFailure("rate_limit")
				terminal.UsageLimit = t.limit
				if terminal.UsageLimit == nil {
					terminal.UsageLimit = &domain.UsageLimit{}
				}
			}
		}
		if terminal.Presentation == "compaction" {
			if terminal.Status == domain.ExecutionCompleted && !t.compacted {
				terminal.Status, terminal.Error = domain.ExecutionFailed, "Claude Code 未确认上下文已压缩"
			}
			if err := r.compactionActivity(t, terminal.Status); err != nil {
				return err
			}
		}
		r.lastResult = completedResult{nativeID: in.UUID, turnID: t.turn.ID}
		r.recordReceipt(t.operation, terminal)
		for _, operation := range t.acceptedSteer {
			r.recordReceipt(operation, terminal)
		}
		r.turn = nil
		return r.emit(domain.Event{Type: "turn", Turn: &terminal, GoalProgress: t.goal})
	}
	return nil
}

func claudeFailure(code string) string {
	switch code {
	case "authentication_failed", "oauth_org_not_allowed", "cloud_credential_error":
		return "Claude Code 模型服务认证失败，请检查设备上的 Profile"
	case "account_on_hold", "billing_error":
		return "Claude Code 模型服务账号或账单不可用"
	case "rate_limit":
		return "Claude Code 已达到模型服务的用量限制"
	case "overloaded", "server_error":
		return "Claude Code 模型服务暂时不可用"
	case "model_not_found":
		return "Claude Code 找不到该 Profile 选择的模型"
	case "max_output_tokens":
		return "Claude Code 回复达到了模型的输出长度限制"
	case "skill_not_loaded":
		return "Claude Code 未加载所选 Skill"
	case "goal_tool_unavailable":
		return "Claude Code 未加载目标进度工具"
	default:
		return "Claude Code 未能完成本轮，请检查模型服务或设备设置"
	}
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
		if c.Name == "ExitPlanMode" && final {
			if err := r.publishPlan(t, c.ID, c.Input); err != nil {
				return err
			}
		}
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
