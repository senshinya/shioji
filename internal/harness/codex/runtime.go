package codex

import (
	"bufio"
	"context"
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
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
	"shioji/internal/managedexec"
)

type Options struct {
	ImportImage      func(harness.OutputImage) (domain.Attachment, error)
	OnMessage        func(context.Context, harness.IncomingMessage) error
	OnStart          func(int) error
	Executable, Home string
	OnEvent          func(domain.Event) error
	Scope            *managedexec.Scope
	Executor         *Executor
}
type Runtime struct {
	desktop         *desktopBridge
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	writeMu         sync.Mutex
	mu              sync.Mutex
	eventMu         sync.Mutex
	pending         map[string]chan wire.Message
	done            chan struct{}
	normalizer      *Normalizer
	onEvent         func(domain.Event) error
	ready           domain.Readiness
	err             error
	scope           *managedexec.Scope
	executor        *Executor
	managedCoverage bool
}

// CallError distinguishes explicit rejection from an unconfirmed external send.
type CallError = harness.DeliveryError

func IsUncertain(err error) bool { return harness.IsUncertain(err) }
func Start(ctx context.Context, opts Options) (*Runtime, error) {
	binary := opts.Executable
	if binary == "" {
		binary = "codex"
	}
	v, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return nil, errors.New("Codex unavailable; install and authenticate Codex 0.166.0")
	}
	if strings.TrimSpace(string(v)) != "codex-cli 0.166.0" {
		return nil, errors.New("incompatible Codex; tested version is 0.166.0")
	}
	argv := []string{binary, "app-server", "--listen", "stdio://"}
	if opts.Executor != nil {
		argv = append(argv, "-c", "features.shell_snapshot=false", "-c", "features.shell_snapshot_v2=false")
	}
	r := &Runtime{pending: map[string]chan wire.Message{}, done: make(chan struct{}), normalizer: NewNormalizer(), onEvent: opts.OnEvent, scope: opts.Scope, executor: opts.Executor}
	r.normalizer.importImage = opts.ImportImage
	env := os.Environ()
	if opts.Home != "" {
		env = append(env, "CODEX_HOME="+opts.Home)
	}
	if opts.OnMessage != nil {
		node, nodeErr := exec.LookPath("node")
		if nodeErr == nil {
			node, err = filepath.Abs(node)
			if err != nil {
				return nil, err
			}
			r.desktop, err = newDesktopBridge(func(ctx context.Context, thread, callID, text string) error {
				return r.receiveDesktopMessage(ctx, thread, callID, text, opts.OnMessage)
			})
			if err != nil {
				return nil, err
			}
			defer func() {
				if r.cmd == nil {
					r.desktop.close()
				}
			}()
			env = desktopEnvironment(env, r.desktop, node)
		} else {
			env = desktopEnvironment(env, nil, "")
		}
	} else {
		env = desktopEnvironment(env, nil, "")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = io.Discard
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if opts.Scope != nil {
		stdin.Close()
		stdout.Close()
		resolved, resolveErr := exec.LookPath(binary)
		if resolveErr != nil {
			return nil, resolveErr
		}
		argv[0] = resolved
		var ownedOut io.Reader
		cmd, stdin, ownedOut, err = opts.Scope.StartOwned(ctx, "agent-runtime", argv, env, opts.OnStart)
		if err != nil {
			return nil, err
		}
		stdoutReader := ownedOut
		r.cmd, r.stdin = cmd, stdin
		return initializeRuntime(ctx, r, stdoutReader)
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	if opts.OnStart != nil {
		if err = opts.OnStart(cmd.Process.Pid); err != nil {
			stdin.Close()
			cmd.Process.Kill()
			cmd.Wait()
			return nil, err
		}
	}
	r.cmd, r.stdin = cmd, stdin
	return initializeRuntime(ctx, r, stdout)
}
func initializeRuntime(ctx context.Context, r *Runtime, stdout io.Reader) (*Runtime, error) {
	go r.read(stdout)
	var init json.RawMessage
	err := r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "shioji", "version": "0.1.0"}, "capabilities": map[string]bool{"experimentalApi": true}}, &init)
	if err == nil {
		err = r.send(wire.Message{Method: "initialized", Params: json.RawMessage(`{}`)})
	}
	if err == nil {
		if r.executor != nil {
			err = r.call(ctx, "environment/add", map[string]any{"environmentId": r.executionEnvironmentID(), "execServerUrl": r.executor.URL(), "authBearerToken": r.executor.Token()}, &init)
		}
	}
	if err == nil {
		err = r.catalog(ctx)
	}
	if err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}
func (r *Runtime) read(stdout io.Reader) {
	if r.desktop != nil {
		defer r.desktop.close()
	}
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 64<<10), 16<<20)
	for scan.Scan() {
		var m wire.Message
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			r.fail(errors.New("malformed app-server frame"))
			break
		}
		if m.Method == "" {
			r.mu.Lock()
			ch := r.pending[string(m.ID)]
			r.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
			continue
		}
		r.eventMu.Lock()
		events, reply, err := r.normalizer.Feed(scan.Bytes())
		if err == nil && reply != nil {
			err = r.write(reply)
		}
		if err == nil {
			err = r.emit(events)
		}
		r.eventMu.Unlock()
		if err != nil {
			r.fail(err)
			break
		}
	}
	if err := scan.Err(); err != nil {
		r.fail(errors.New("app-server stream failed or exceeded 16 MiB limit"))
	}
	r.cmd.Process.Kill()
	r.cmd.Wait()
	r.eventMu.Lock()
	r.emit(r.normalizer.Invalidate())
	r.eventMu.Unlock()
	r.mu.Lock()
	if r.err == nil {
		r.err = errors.New("app-server exited")
	}
	r.mu.Unlock()
	close(r.done)
}
func (r *Runtime) emit(events []domain.Event) error {
	if r.onEvent == nil {
		return nil
	}
	for _, e := range events {
		if err := r.onEvent(e); err != nil {
			return err
		}
	}
	return nil
}
func (r *Runtime) fail(err error) { r.mu.Lock(); r.err = err; r.mu.Unlock(); r.cmd.Process.Kill() }
func (r *Runtime) write(b []byte) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if len(b) > 16<<20 {
		return errors.New("app-server request too large")
	}
	_, err := r.stdin.Write(append(b, '\n'))
	return err
}
func (r *Runtime) send(m wire.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return r.write(b)
}
func (r *Runtime) call(ctx context.Context, method string, params, out any) error {
	id, _ := json.Marshal("shioji:" + domain.NewID())
	ch := make(chan wire.Message, 1)
	key := string(id)
	r.mu.Lock()
	r.pending[key] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, key); r.mu.Unlock() }()
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err = r.send(wire.Message{ID: id, Method: method, Params: b}); err != nil {
		return &CallError{Uncertain: true, Message: "app-server send failed; external acceptance is unknown"}
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return &CallError{Message: fmt.Sprintf("Codex rejected %s (code %d)", method, m.Error.Code)}
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		return &CallError{Uncertain: true, Message: "app-server response timed out; external acceptance is unknown"}
	case <-r.done:
		return &CallError{Uncertain: true, Message: "app-server exited before confirming the request"}
	}
}
func (r *Runtime) catalog(ctx context.Context) error {
	ready := domain.Readiness{State: "ready"}
	var cursor *string
	for {
		var result struct {
			Data       []wire.Model `json:"data"`
			NextCursor *string      `json:"nextCursor"`
		}
		if err := r.call(ctx, "model/list", map[string]any{"limit": 100, "includeHidden": false, "cursor": cursor}, &result); err != nil {
			return err
		}
		for _, m := range result.Data {
			model := domain.Model{ID: m.Model, Name: m.DisplayName, Default: m.IsDefault, DefaultEffort: m.DefaultReasoningEffort}
			for _, e := range m.SupportedReasoningEfforts {
				model.Efforts = append(model.Efforts, e.ReasoningEffort)
			}
			for _, tier := range m.ServiceTiers {
				if tier.ID == "fast" {
					model.Fast = true
				}
			}
			ready.Models = append(ready.Models, model)
		}
		if result.NextCursor == nil {
			break
		}
		if cursor != nil && *cursor == *result.NextCursor {
			return errors.New("model catalog cursor did not advance")
		}
		cursor = result.NextCursor
	}
	if len(ready.Models) == 0 {
		return errors.New("Codex returned no available models")
	}
	var modes struct {
		Data []struct {
			Mode string `json:"mode"`
		} `json:"data"`
	}
	if err := r.call(ctx, "collaborationMode/list", struct{}{}, &modes); err != nil {
		return err
	}
	for _, m := range modes.Data {
		if m.Mode == "plan" {
			ready.Plan = true
		}
	}
	var requirements struct {
		Requirements *struct {
			AllowedApprovalPolicies   []string        `json:"allowedApprovalPolicies"`
			AllowedSandboxModes       []string        `json:"allowedSandboxModes"`
			AllowedPermissionProfiles map[string]bool `json:"allowedPermissionProfiles"`
		} `json:"requirements"`
	}
	if err := r.call(ctx, "configRequirements/read", struct{}{}, &requirements); err != nil {
		return err
	}
	for _, permission := range []string{"read-only", "default", "auto-approve", "yolo"} {
		sandbox, approval, _ := permissionPolicy(permission)
		allowed := true
		if req := requirements.Requirements; req != nil {
			allowed = (req.AllowedApprovalPolicies == nil || slices.Contains(req.AllowedApprovalPolicies, approval)) && (req.AllowedSandboxModes == nil || slices.Contains(req.AllowedSandboxModes, sandbox)) && req.AllowedPermissionProfiles == nil
		}
		if allowed {
			ready.Permissions = append(ready.Permissions, permission)
		}
	}
	// Discover named profiles without silently mapping them onto the four prototype presets.
	var profiles json.RawMessage
	if err := r.call(ctx, "permissionProfile/list", map[string]any{"limit": 100}, &profiles); err != nil {
		return err
	}
	if len(ready.Permissions) == 0 {
		return errors.New("managed permission profiles require a supported explicit mapping")
	}
	r.ready = ready
	return nil
}
func (r *Runtime) Readiness() domain.Readiness {
	select {
	case <-r.done:
		return domain.Readiness{State: "unavailable", Error: "Codex app-server stopped; reconcile existing sessions before retrying"}
	default:
		return r.ready
	}
}
func permissionPolicy(permission string) (string, string, string) {
	switch permission {
	case "read-only":
		return "read-only", "never", "readOnly"
	case "auto-approve":
		return "workspace-write", "never", "workspaceWrite"
	case "yolo":
		return "danger-full-access", "never", "dangerFullAccess"
	default:
		return "workspace-write", "on-request", "workspaceWrite"
	}
}
func (r *Runtime) Validate(config domain.RunConfig) error {
	if r.Readiness().State != "ready" {
		return errors.New("Codex runtime unavailable")
	}
	if !slices.Contains(r.ready.Permissions, config.Permission) {
		return errors.New("permission is unavailable under current policy")
	}
	for _, m := range r.ready.Models {
		if m.ID != config.Model {
			continue
		}
		if config.Effort != "" && !slices.Contains(m.Efforts, config.Effort) {
			return errors.New("unsupported reasoning effort")
		}
		if config.Fast && !m.Fast {
			return errors.New("Fast unavailable for this model")
		}
		if config.Plan && !r.ready.Plan {
			return errors.New("Plan mode unavailable")
		}
		return nil
	}
	return errors.New("model is not in the runtime catalog")
}
func (r *Runtime) Create(ctx context.Context, s domain.Session) (string, error) {
	if err := r.Validate(s.Config); err != nil {
		return "", err
	}
	sandbox, approval, kind := permissionPolicy(s.Config.Permission)
	var out struct {
		Thread struct {
			ID   string  `json:"id"`
			Name *string `json:"name"`
		} `json:"thread"`
		CWD     string `json:"cwd"`
		Model   string `json:"model"`
		Sandbox struct {
			Type string `json:"type"`
		} `json:"sandbox"`
	}
	params := wire.ThreadParams{CWD: s.Context.CWD, Model: s.Config.Model, ApprovalPolicy: approval, Sandbox: sandbox, RuntimeWorkspaceRoots: append([]string{s.Context.CWD}, s.Context.AdditionalDirectories...)}
	configuration, err := r.executionConfig(ctx, s)
	if err != nil {
		return "", err
	}
	params.Config = configuration
	params.Environments = r.executionEnvironment(s)
	if err := r.call(ctx, "thread/start", params, &out); err != nil {
		return "", err
	}
	if out.Thread.ID == "" || sourceExecutionPath(out.CWD) != s.Context.CWD || out.Model != s.Config.Model || out.Sandbox.Type != kind {
		return "", errors.New("Codex did not accept the requested execution context")
	}
	if err := r.bindThread(out.Thread.ID, s.ID, true, out.Thread.Name); err != nil {
		return "", err
	}
	return out.Thread.ID, nil
}
func (r *Runtime) Resume(ctx context.Context, s domain.Session) error {
	if err := r.Validate(s.Config); err != nil {
		return err
	}
	var out struct {
		Thread struct {
			ID   string  `json:"id"`
			Name *string `json:"name"`
		} `json:"thread"`
		CWD string `json:"cwd"`
	}
	configuration, err := r.executionConfig(ctx, s)
	if err != nil {
		return err
	}
	if err := r.call(ctx, "thread/resume", map[string]any{"threadId": s.ThreadID, "excludeTurns": true, "config": configuration}, &out); err != nil {
		return err
	}
	if sourceExecutionPath(out.CWD) != s.Context.CWD || out.Thread.ID != s.ThreadID {
		return errors.New("restored thread execution context differs")
	}
	return r.bindThread(s.ThreadID, s.ID, false, out.Thread.Name)
}
func (r *Runtime) bindThread(thread, session string, fresh bool, name *string) error {
	// Response metadata precedes notifications handled after this binding.
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	r.normalizer.Bind(thread, session, fresh)
	if name == nil || strings.TrimSpace(*name) == "" {
		return nil
	}
	return r.emit([]domain.Event{{SessionID: session, Type: "session.title", Text: boundedTo(strings.TrimSpace(*name), 4<<10)}})
}
func (r *Runtime) Submit(ctx context.Context, s domain.Session, operationID, text string, attachments ...AttachmentInput) (string, error) {
	return r.submit(ctx, s, operationID, text, attachments, nil)
}
func (r *Runtime) submit(ctx context.Context, s domain.Session, operationID, text string, attachments []AttachmentInput, skills []domain.SkillReference) (string, error) {
	if err := r.Validate(s.Config); err != nil {
		return "", err
	}
	_, approval, kind := permissionPolicy(s.Config.Permission)
	policy := wire.SandboxPolicy{Type: kind}
	if kind == "workspaceWrite" {
		policy.WritableRoots = append([]string{s.Context.CWD}, s.Context.AdditionalDirectories...)
	}
	input, err := attachmentInputs(text, attachments)
	input = append(input, skillInputs(skills)...)
	if err != nil {
		return "", err
	}
	params := wire.TurnParams{ThreadID: s.ThreadID, Input: input, ClientUserMessageID: "input:" + operationID, Model: s.Config.Model, Effort: s.Config.Effort, ApprovalPolicy: approval, SandboxPolicy: policy}
	params.Environments = r.executionEnvironment(s)
	if s.Config.Fast {
		fast := "fast"
		params.ServiceTier = &fast
	}
	{
		mode := "default"
		if s.Config.Plan {
			mode = "plan"
		}
		var effort *string
		if s.Config.Effort != "" {
			effort = &s.Config.Effort
		}
		params.CollaborationMode = &wire.CollaborationMode{Mode: mode, Settings: wire.ModeSettings{Model: s.Config.Model, Effort: effort}}
	}
	var out struct {
		Turn wire.Turn `json:"turn"`
	}
	if err := r.call(ctx, "turn/start", params, &out); err != nil {
		return "", err
	}
	if out.Turn.ID == "" {
		return "", &CallError{Uncertain: true, Message: "turn response omitted its source identity"}
	}
	return out.Turn.ID, nil
}

// Steer uses the pinned app-server schema's active-turn precondition.
func (r *Runtime) Steer(ctx context.Context, s domain.Session, turnID, operationID, text string, attachments ...AttachmentInput) error {
	return r.steer(ctx, s, turnID, operationID, text, attachments, nil)
}
func (r *Runtime) steer(ctx context.Context, s domain.Session, turnID, operationID, text string, attachments []AttachmentInput, skills []domain.SkillReference) error {
	input, err := attachmentInputs(text, attachments)
	input = append(input, skillInputs(skills)...)
	if err != nil {
		return err
	}
	params := struct {
		ThreadID            string       `json:"threadId"`
		ExpectedTurnID      string       `json:"expectedTurnId"`
		ClientUserMessageID string       `json:"clientUserMessageId"`
		Input               []wire.Input `json:"input"`
	}{s.ThreadID, turnID, "input:" + operationID, input}
	var out struct {
		TurnID string `json:"turnId"`
	}
	if err := r.call(ctx, "turn/steer", params, &out); err != nil {
		return err
	}
	if out.TurnID != turnID {
		return &CallError{Uncertain: true, Message: "steer response omitted or changed the active turn identity"}
	}
	return nil
}
func (r *Runtime) Respond(answer domain.Answer) error {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	data, err := r.normalizer.Response(answer)
	if err != nil {
		return err
	}
	request, _ := r.normalizer.Pending(answer.RequestID)
	if err = r.emit([]domain.Event{{Type: "request.open", SessionID: request.SessionID, Request: &request}}); err != nil {
		return err
	}
	if err = r.write(data); err != nil {
		return &CallError{Uncertain: true, Message: "response delivery is unknown; do not resend"}
	}
	return nil
}
func (r *Runtime) Interrupt(ctx context.Context, s domain.Session, turnID string) error {
	return r.call(ctx, "turn/interrupt", map[string]string{"threadId": s.ThreadID, "turnId": turnID}, nil)
}
func (r *Runtime) Close() error {
	if r.desktop != nil {
		r.desktop.close()
	}
	r.stdin.Close()
	select {
	case <-r.done:
		return nil
	case <-time.After(3 * time.Second):
		syscall.Kill(-r.cmd.Process.Pid, syscall.SIGTERM)
	}
	select {
	case <-r.done:
		return nil
	case <-time.After(2 * time.Second):
		syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
		<-r.done
		return nil
	}
}
