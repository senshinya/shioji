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
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
)

type Options struct {
	OnStart          func(int) error
	Executable, Home string
	OnEvent          func(domain.Event) error
}
type Runtime struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	writeMu    sync.Mutex
	mu         sync.Mutex
	eventMu    sync.Mutex
	pending    map[string]chan wire.Message
	done       chan struct{}
	normalizer *Normalizer
	onEvent    func(domain.Event) error
	ready      domain.Readiness
	err        error
}

// CallError distinguishes explicit rejection from an unconfirmed external send.
type CallError struct {
	Uncertain bool
	Message   string
}

func (e *CallError) Error() string { return e.Message }
func IsUncertain(err error) bool   { var e *CallError; return errors.As(err, &e) && e.Uncertain }
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
	cmd := exec.Command(binary, "app-server", "--listen", "stdio://")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = io.Discard
	if opts.Home != "" {
		cmd.Env = append(os.Environ(), "CODEX_HOME="+opts.Home)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	r := &Runtime{cmd: cmd, stdin: stdin, pending: map[string]chan wire.Message{}, done: make(chan struct{}), normalizer: NewNormalizer(), onEvent: opts.OnEvent}
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
	go r.read(stdout)
	var init json.RawMessage
	err = r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "shioji", "version": "0.1.0"}, "capabilities": map[string]bool{"experimentalApi": true}}, &init)
	if err == nil {
		err = r.send(wire.Message{Method: "initialized", Params: json.RawMessage(`{}`)})
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
			ID string `json:"id"`
		} `json:"thread"`
		CWD     string `json:"cwd"`
		Model   string `json:"model"`
		Sandbox struct {
			Type string `json:"type"`
		} `json:"sandbox"`
	}
	params := wire.ThreadParams{CWD: s.Context.CWD, Model: s.Config.Model, ApprovalPolicy: approval, Sandbox: sandbox, RuntimeWorkspaceRoots: append([]string{s.Context.CWD}, s.Context.AdditionalDirectories...)}
	if err := r.call(ctx, "thread/start", params, &out); err != nil {
		return "", err
	}
	if out.Thread.ID == "" || out.CWD != s.Context.CWD || out.Model != s.Config.Model || out.Sandbox.Type != kind {
		return "", errors.New("Codex did not accept the requested execution context")
	}
	r.normalizer.Bind(out.Thread.ID, s.ID, true)
	return out.Thread.ID, nil
}
func (r *Runtime) Resume(ctx context.Context, s domain.Session) error {
	if err := r.Validate(s.Config); err != nil {
		return err
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		CWD string `json:"cwd"`
	}
	if err := r.call(ctx, "thread/resume", map[string]any{"threadId": s.ThreadID}, &out); err != nil {
		return err
	}
	if out.CWD != s.Context.CWD || out.Thread.ID != s.ThreadID {
		return errors.New("restored thread execution context differs")
	}
	r.normalizer.Bind(s.ThreadID, s.ID, false)
	return nil
}
func (r *Runtime) Submit(ctx context.Context, s domain.Session, operationID, text string) (string, error) {
	if err := r.Validate(s.Config); err != nil {
		return "", err
	}
	_, approval, kind := permissionPolicy(s.Config.Permission)
	policy := wire.SandboxPolicy{Type: kind}
	if kind == "workspaceWrite" {
		policy.WritableRoots = append([]string{s.Context.CWD}, s.Context.AdditionalDirectories...)
	}
	params := wire.TurnParams{ThreadID: s.ThreadID, Input: []wire.Input{{Type: "text", Text: text}}, ClientUserMessageID: "input:" + operationID, Model: s.Config.Model, Effort: s.Config.Effort, ApprovalPolicy: approval, SandboxPolicy: policy}
	if s.Config.Fast {
		fast := "fast"
		params.ServiceTier = &fast
	}
	if s.Config.Plan {
		var effort *string
		if s.Config.Effort != "" {
			effort = &s.Config.Effort
		}
		params.CollaborationMode = &wire.CollaborationMode{Mode: "plan", Settings: wire.ModeSettings{Model: s.Config.Model, Effort: effort}}
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
