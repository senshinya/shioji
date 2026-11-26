package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/systemexec"
)

type initialization struct {
	ProtocolVersion int `json:"protocolVersion"`
	AgentInfo       struct {
		Name, Version string
	} `json:"agentInfo"`
	AgentCapabilities struct {
		LoadSession        bool
		MCPMethods         struct{ HTTP bool } `json:"mcpCapabilities"`
		PromptCapabilities struct {
			Image           bool
			EmbeddedContext bool
		}
		SessionCapabilities struct {
			List                  *struct{}
			Resume                *struct{}
			Close                 *struct{}
			AdditionalDirectories *struct{}
		}
	} `json:"agentCapabilities"`
}

type choice struct {
	Value, Name, Description string
}

type configOption struct {
	ID, Type, Category, CurrentValue string
	Options                          []choice
}

func (o *configOption) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID, Type, Category string
		CurrentValue       json.RawMessage
		Options            []struct {
			choice
			Options []choice
		}
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	o.ID, o.Type, o.Category = raw.ID, raw.Type, raw.Category
	if o.Type != "select" {
		return nil
	}
	if err := json.Unmarshal(raw.CurrentValue, &o.CurrentValue); err != nil {
		return err
	}
	for _, opt := range raw.Options {
		if opt.Value != "" {
			o.Options = append(o.Options, opt.choice)
		}
		o.Options = append(o.Options, opt.Options...)
	}
	return nil
}

func (o configOption) has(value string) bool {
	return slices.ContainsFunc(o.Options, func(c choice) bool { return c.Value == value })
}

type sessionResponse struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []configOption `json:"configOptions"`
	Models        struct {
		AvailableModels []struct {
			ModelID, Name string
		}
	} `json:"models"`
	Meta struct {
		Antigravity struct {
			Tier struct {
				Name string `json:"name"`
				ID   string `json:"id"`
			} `json:"tier"`
		} `json:"antigravity"`
	} `json:"_meta"`
}

func subscriptionTier(result sessionResponse) string {
	name := strings.TrimSpace(result.Meta.Antigravity.Tier.Name)
	if name == "" || strings.ContainsAny(name, "\r\n\x00") {
		return ""
	}
	return name
}

func selectOption(options []configOption, category string) (configOption, bool) {
	for _, opt := range options {
		if opt.Type == "select" && (opt.Category == category || opt.ID == category) {
			return opt, true
		}
	}
	return configOption{}, false
}

type sessionRequest struct {
	SessionID             string          `json:"sessionId,omitempty"`
	CWD                   string          `json:"cwd"`
	MCPServers            []mcpHTTPServer `json:"mcpServers"`
	AdditionalDirectories []string        `json:"additionalDirectories,omitempty"`
}

type runtime struct {
	descriptor   domain.HarnessDescriptor
	options      harness.StartOptions
	version      string
	profile      string
	skillProfile string
	skillHome    string
	historyBin   string
	historyEnv   []string
	native       initialization
	process      *rpc
	bridge       *goalBridge
	callback     *callbackBridge
	nativeYOLO   bool
	direct       sync.Mutex // Serialize native configuration and binding.

	mu             sync.Mutex
	ready          domain.Readiness
	cfg            sessionResponse
	session        domain.Session
	active         *logicalTurn
	settledTurn    string
	lateTools      map[string]lateTool
	lateToolIDs    []string
	nativeTitle    string
	commandOffers  map[string]bool
	commandChanged chan struct{}
	requests       map[string]nativeRequest
	done           chan struct{} // Catalog lifetime only.
	close          sync.Once
}

func (a Adapter) Start(ctx context.Context, options harness.StartOptions) (harness.Runtime, error) {
	env, _ := a.environment(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := a.executable(env)
	if err != nil {
		return nil, err
	}
	if a.profileID != "" && !a.profileAuthReady() {
		return nil, &harness.SetupError{Issue: domain.RuntimeSignInRequired, Message: "请登录此 Profile 的 Google 账号"}
	}
	version, err := executableVersion(path)
	if err != nil {
		return nil, err
	}
	profile := skillProfileDirectory(env)
	if resolved, err := filepath.EvalSymlinks(profile); err == nil && filepath.IsAbs(resolved) {
		profile = resolved
	}
	r := &runtime{
		descriptor: a.Descriptor(), options: options, version: version, profile: profileKey(env), skillProfile: profile, done: make(chan struct{}), requests: make(map[string]nativeRequest),
		skillHome: systemexec.EnvironmentValue(env, "HOME"),
		ready:     domain.Readiness{HarnessID: a.Descriptor().ID, State: domain.RuntimeUnavailable, Error: "请在设备 Agent 设置重新检测以获取模型"},
	}
	argv := []string{path, "acp", "--process-per-session", "--disable-telemetry"}
	if cfg := options.InitialConfig; !options.CatalogOnly && cfg != nil && cfg.Permission == yoloPermission && !cfg.Plan {
		argv = append(argv, "--dangerously-skip-permissions")
		r.nativeYOLO = true
	}
	if profile := systemexec.EnvironmentValue(env, "GEMINI_HOME"); filepath.IsAbs(profile) {
		argv = append(argv, "--gemini-dir", profile)
	}
	env = append(env, "AGY_CLI_DISABLE_AUTO_UPDATE=1")
	if !options.CatalogOnly && options.OnMessage != nil {
		r.callback, err = r.startCallbackBridge()
		if err != nil {
			return nil, errors.New("无法启动会话回调入口")
		}
		env = append(env, "SHIOJI_CALLBACK_URL="+r.callback.url, "SHIOJI_CALLBACK_TOKEN="+r.callback.token)
	}
	hooks := options
	hooks.OnLost = r.transportLost
	c, err := startRPC(ctx, argv, env, hooks, r.handle)
	if err != nil {
		_ = r.callback.close()
		return nil, &harness.SetupError{Issue: domain.RuntimeIncompatible, Message: "无法启动该设备上的 agy ACP"}
	}
	r.process = c
	go c.read()
	initialized := false
	defer func() {
		if !initialized {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = r.Close(cleanup)
		}
	}()
	params := struct {
		ProtocolVersion    int               `json:"protocolVersion"`
		ClientInfo         map[string]string `json:"clientInfo"`
		ClientCapabilities struct {
			Elicitation struct {
				Form struct{} `json:"form"`
			} `json:"elicitation"`
		} `json:"clientCapabilities"`
	}{ProtocolVersion: 1, ClientInfo: map[string]string{"name": "Shioji", "version": "1"}}
	if err := c.call(ctx, "initialize", params, &r.native); err != nil || r.native.ProtocolVersion != 1 || r.native.AgentInfo.Name != "Antigravity ACP Server" {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &harness.SetupError{Issue: domain.RuntimeIncompatible, Message: "该可执行文件未提供兼容的 Antigravity ACP"}
	}
	if current, err := executableVersion(path); err != nil || current != version {
		return nil, errors.New("agy 安装正在变化，请重新检测")
	}
	if options.CatalogOnly {
		r.historyBin, r.historyEnv = path, slices.Clone(env)
		r.ready.Capabilities = r.capabilities()
		if err := r.discover(ctx); err != nil {
			return nil, err
		}
		if err := r.closeProcess(ctx); err != nil {
			return nil, err
		}
		r.process = nil
	} else {
		if r.native.AgentCapabilities.MCPMethods.HTTP {
			r.bridge, err = r.startGoalBridge()
			if err != nil {
				return nil, err
			}
		}
		r.ready.Capabilities = r.capabilities()
		c.ready.Store(true)
	}
	initialized = true
	return r, nil
}

func (r *runtime) capabilities() domain.HarnessCapabilities {
	// Concurrent prompt and cancellation semantics are verified on modern agy.
	resume := r.native.AgentCapabilities.SessionCapabilities.Resume != nil
	portable := r.native.AgentCapabilities.PromptCapabilities.EmbeddedContext
	goals := r.native.AgentCapabilities.MCPMethods.HTTP && (r.bridge != nil || r.options.CatalogOnly)
	return domain.HarnessCapabilities{
		Goals: goals, WorkerGoals: goals, ExclusivePlan: true, PathAttachments: true, Skills: true, SkillManagement: true,
		Interrupt: true, Steer: verifiedConcurrentPrompt(r.native.AgentInfo.Version),
		Resume: resume, CompactNeedsCheck: resume, Reload: resume && r.native.AgentCapabilities.SessionCapabilities.Close != nil,
		Fork: portable, PortableFork: portable, ConversationImport: r.historyAvailable(),
		ToolServices: r.skillProfile != "", ToolServiceInspection: r.skillProfile != "",
		HookManagement: r.skillProfile != "", HookInspection: r.skillProfile != "",
	}
}

func verifiedConcurrentPrompt(version string) bool {
	var major, minor, patch int
	if count, _ := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); count != 3 {
		return false
	}
	return major > 1 || major == 1 && (minor > 5 || minor == 5 && patch >= 15)
}

func (r *runtime) bind(ctx context.Context, s domain.Session, source string) (sessionResponse, error) {
	var result sessionResponse
	if r.process == nil || r.Stopped() {
		return result, os.ErrClosed
	}
	if !filepath.IsAbs(s.Context.CWD) {
		return result, errors.New("Antigravity 会话需要执行设备上的绝对目录")
	}
	if len(s.Context.AdditionalDirectories) > 0 && r.native.AgentCapabilities.SessionCapabilities.AdditionalDirectories == nil {
		return result, harness.Unsupported("additional-directories")
	}
	method := "session/new"
	if source != "" {
		if r.native.AgentCapabilities.SessionCapabilities.Resume == nil {
			return result, harness.Unsupported("resume")
		}
		method = "session/resume"
	}
	request := sessionRequest{SessionID: source, CWD: s.Context.CWD, MCPServers: []mcpHTTPServer{}, AdditionalDirectories: s.Context.AdditionalDirectories}
	if r.bridge != nil {
		request.MCPServers = append(request.MCPServers, r.bridge.server)
	}
	if err := r.process.call(ctx, method, request, &result); err != nil {
		return result, setupError(err)
	}
	if result.SessionID == "" || source != "" && result.SessionID != source {
		return result, errors.New("Antigravity 返回的会话标识无效")
	}
	return result, nil
}

func setupError(err error) error {
	var native *rpcError
	if !errors.As(err, &native) {
		return err
	}
	if native.Code == -32000 {
		return &harness.SetupError{Issue: domain.RuntimeSignInRequired, Message: "请登录所属设备上的 Antigravity 账号",
			Account: &domain.RuntimeAccount{Mode: "account", Label: "Antigravity", State: "required"}}
	}
	return fmt.Errorf("Antigravity ACP 请求失败（%d）", native.Code)
}

func (r *runtime) Create(ctx context.Context, s domain.Session) (string, error) {
	r.direct.Lock()
	defer r.direct.Unlock()
	if r.session.ID != "" {
		return "", errors.New("Antigravity 运行环境已绑定会话")
	}
	response, err := r.bind(ctx, s, "")
	if err != nil {
		return "", err
	}
	r.setBinding(s, response)
	return response.SessionID, nil
}

func (r *runtime) Resume(ctx context.Context, s domain.Session) error {
	r.direct.Lock()
	defer r.direct.Unlock()
	if r.Observes(s) {
		return r.configure(ctx, s.Config)
	}
	if s.ThreadID == "" || r.session.ID != "" {
		return errors.New("无法绑定这条 Antigravity 历史")
	}
	response, err := r.bind(ctx, s, s.ThreadID)
	if err != nil {
		return err
	}
	r.setBinding(s, response)
	return r.configure(ctx, s.Config)
}

func (r *runtime) setBinding(s domain.Session, response sessionResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.ThreadID = response.SessionID
	r.session, r.cfg = s, response
	for id := range r.commandOffers {
		if id != s.ThreadID {
			delete(r.commandOffers, id)
		}
	}
	r.ready = makeReadiness(r.ready.HarnessID, response, r.capabilities())
}

func (r *runtime) configure(ctx context.Context, cfg domain.RunConfig) error {
	if r.PermissionRequiresRestart(cfg) {
		return errPermissionRestart
	}
	if err := r.Validate(cfg); err != nil {
		return err
	}
	if err := r.setOption(ctx, "model", cfg.Model); err != nil {
		return err
	}
	if cfg.Effort != "" {
		if err := r.setOption(ctx, "thought_level", cfg.Effort); err != nil {
			return err
		}
	}
	mode := cfg.Permission
	if mode == "" || mode == yoloPermission {
		mode = "default"
	}
	if cfg.Plan {
		mode = "plan"
	}
	if err := r.setOption(ctx, "mode", mode); err != nil {
		return err
	}
	r.mu.Lock()
	r.session.Config.Permission = cfg.Permission
	r.mu.Unlock()
	return nil
}

func (r *runtime) setOption(ctx context.Context, category, value string) error {
	option, ok := selectOption(r.cfg.ConfigOptions, category)
	if !ok || !option.has(value) {
		return errors.New("所选 Antigravity 配置在当前会话不可用")
	}
	if option.CurrentValue == value {
		return nil
	}
	args := struct {
		SessionID string `json:"sessionId"`
		ConfigID  string `json:"configId"`
		Value     string `json:"value"`
	}{r.session.ThreadID, option.ID, value}
	var result sessionResponse
	if err := r.process.call(ctx, "session/set_config_option", args, &result); err != nil {
		return setupError(err)
	}
	r.cfg.ConfigOptions = result.ConfigOptions
	applied, ok := selectOption(result.ConfigOptions, category)
	if !ok || applied.CurrentValue != value {
		return errors.New("Antigravity 未确认所选配置")
	}
	return nil
}

func (r *runtime) Readiness() domain.Readiness {
	r.mu.Lock()
	defer r.mu.Unlock()
	ready := r.ready
	if r.session.ThreadID != "" {
		compact, advertised := r.commandOffers[r.session.ThreadID]
		ready.Capabilities.Compact = compact
		ready.Capabilities.CompactNeedsCheck = !advertised && ready.Capabilities.CompactNeedsCheck
	}
	d := r.descriptor
	if d.ID == "" {
		d = (Adapter{}).Descriptor()
	}
	ready.Harnesses = []domain.HarnessInfo{{
		ID: d.ID, Name: d.Name, Icon: d.Icon, State: ready.State, Error: ready.Error, Account: ready.Account,
		Models: ready.Models, Permissions: ready.Permissions, PermissionChoices: ready.PermissionChoices,
		Plan: ready.Plan, Capabilities: ready.Capabilities,
	}}
	return ready
}

func (r *runtime) Validate(cfg domain.RunConfig) error {
	ready := r.Readiness()
	if ready.State != domain.RuntimeReady {
		return errors.New(ready.Error)
	}
	index := slices.IndexFunc(ready.Models, func(m domain.Model) bool { return m.ID == cfg.Model })
	if index < 0 {
		return errors.New(domain.ModelUnavailableMessage)
	}
	if cfg.Effort != "" && !slices.Contains(ready.Models[index].Efforts, cfg.Effort) {
		return harness.Unsupported("thinking-level")
	}
	if cfg.Fast {
		return harness.Unsupported("fast")
	}
	permission := cfg.Permission
	if permission == "" {
		permission = "default"
	}
	if !slices.Contains(ready.Permissions, permission) || cfg.Plan && (!ready.Plan || permission != "default") {
		return harness.Unsupported("execution-mode")
	}
	return nil
}

func makeReadiness(id string, result sessionResponse, capabilities domain.HarnessCapabilities) domain.Readiness {
	ready := domain.Readiness{HarnessID: id, Capabilities: capabilities, State: domain.RuntimeUnavailable, Error: "Antigravity 未返回可用模型或执行模式"}
	model, modelOK := selectOption(result.ConfigOptions, "model")
	mode, modeOK := selectOption(result.ConfigOptions, "mode")
	if !modelOK || !modeOK || !mode.has("default") {
		return ready
	}
	thinking, _ := selectOption(result.ConfigOptions, "thought_level")
	for _, m := range model.Options {
		entry := domain.Model{ID: m.Value, Name: m.Name, Default: m.Value == model.CurrentValue}
		if entry.Default {
			entry.DefaultEffort = thinking.CurrentValue
			for _, effort := range thinking.Options {
				entry.Efforts = append(entry.Efforts, effort.Value)
			}
		} else {
			for _, variant := range result.Models.AvailableModels {
				if effort, ok := strings.CutPrefix(variant.ModelID, m.Value+"-"); ok && !slices.Contains(entry.Efforts, effort) {
					entry.Efforts = append(entry.Efforts, effort)
				}
			}
		}
		ready.Models = append(ready.Models, entry)
	}
	for _, option := range mode.Options {
		switch option.Value {
		case "plan":
			ready.Plan = true
		case "default", "accept-edits":
			ready.Permissions = append(ready.Permissions, option.Value)
			ready.PermissionChoices = append(ready.PermissionChoices, domain.OptionChoice{ID: option.Value, Name: option.Name, Description: option.Description})
		}
	}
	if len(ready.Models) > 0 {
		ready.State, ready.Error = domain.RuntimeReady, ""
		addYOLOPermission(&ready)
	}
	return ready
}

func (r *runtime) CatalogVersion() (string, error) { return r.version, nil }
func (r *runtime) ManagedCoverage() bool           { return r.options.Scope != nil }
func (r *runtime) ExecutionReady() bool {
	return r.process != nil && !r.process.unavailable() && (r.options.Scope == nil || r.options.Scope.Admitting())
}
func (r *runtime) Stopped() bool {
	select {
	case <-r.Done():
		return true
	default:
		return false
	}
}

func (r *runtime) Done() <-chan struct{} {
	if r.process != nil {
		return r.process.done
	}
	return r.done
}

func (r *runtime) Close(ctx context.Context) error {
	if r.process == nil {
		r.close.Do(func() { close(r.done) })
		return nil
	}
	return errors.Join(r.callback.close(), r.closeProcess(ctx), r.bridge.close(ctx))
}

func (r *runtime) closeProcess(ctx context.Context) error {
	c := r.process
	c.closing.Store(true)
	_ = c.in.Close()
	if r.options.Scope != nil {
		if err := r.options.Scope.Stop(ctx); err != nil {
			return err
		}
	} else {
		select {
		case <-c.waitDone:
			return nil
		default:
			// The probe owns this entire process group. A child can keep stdout
			// open even when the root already exited.
			_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
		}
	}
	select {
	case <-c.waitDone:
		return nil
	case <-ctx.Done():
		if r.options.Scope == nil {
			_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		}
		return ctx.Err()
	}
}

func (r *runtime) Unload(ctx context.Context, s domain.Session) error {
	if !r.Observes(s) || r.native.AgentCapabilities.SessionCapabilities.Close == nil {
		return nil
	}
	return setupError(r.process.call(ctx, "session/close", sessionID(s.ThreadID), nil))
}

func sessionID(value string) map[string]string { return map[string]string{"sessionId": value} }

func (r *runtime) Observes(s domain.Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.process != nil && !r.Stopped() && s.ThreadID != "" && r.session.ID == s.ID && r.session.ThreadID == s.ThreadID
}

func (r *runtime) Inspect(context.Context, domain.Session) (domain.RecoveryHistory, error) {
	return domain.RecoveryHistory{}, harness.Unsupported("recover-in-flight")
}
func (r *runtime) PrepareHistory(context.Context, domain.Session, harness.HistoryBoundary) (string, error) {
	return "", harness.Unsupported("native-history-edit")
}
func (r *runtime) ActivateHistory(context.Context, domain.Session, string, func() error) error {
	return harness.Unsupported("native-history-edit")
}

func (r *runtime) transportLost() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	bridgeErr := errors.Join(r.callback.close(), r.bridge.close(ctx))
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, permission := range r.requests {
		permission.Request.State = domain.RequestInvalidated
		permission.Request.Revision++
		_ = r.emit(domain.Event{Type: "request.resolve", Request: &permission.Request})
		delete(r.requests, key)
	}
	if r.session.ID != "" && r.options.OnEvent != nil {
		return errors.Join(bridgeErr, r.emit(domain.Event{Type: "runtime.lost"}))
	}
	if r.options.OnLost != nil {
		return errors.Join(bridgeErr, r.options.OnLost())
	}
	return bridgeErr
}
