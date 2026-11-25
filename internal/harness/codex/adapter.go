package codex

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"sync"
	"time"
)

// Adapter is configured at composition time. Codex settings never enter the
// worker contract or the generic run configuration.
type Adapter struct {
	Executable, Home       string
	defaultExecutable      string
	override               string
	defaultHome            string
	hasDefaultHome         bool
	runtimeOptions         domain.RuntimeOptions
	launchEnv              []string
	profileID, profileName string
	profileMode            domain.AgentProfileMode
	apiPreset              *apiPreset
	responses              bool
	managedState           string
}

func (a Adapter) Descriptor() domain.HarnessDescriptor {
	if a.responses {
		if a.profileID != "" {
			return domain.HarnessDescriptor{ID: a.profileID, Name: a.profileName, Icon: "openai"}
		}
		return domain.HarnessDescriptor{ID: domain.AgentResponses, Name: "Responses API", Icon: "openai", ProfileOnly: true}
	}
	if a.apiPreset != nil {
		if a.profileID != "" {
			return domain.HarnessDescriptor{ID: a.profileID, Name: a.profileName, Icon: a.apiPreset.id}
		}
		return domain.HarnessDescriptor{ID: a.apiPreset.id, Name: a.apiPreset.id, Icon: a.apiPreset.id, ProfileOnly: true}
	}
	if a.profileID != "" {
		return domain.HarnessDescriptor{ID: a.profileID, Name: a.profileName, Icon: "openai"}
	}
	return domain.HarnessDescriptor{ID: "codex", Name: "Codex", Icon: "openai"}
}
func (a Adapter) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	if a.responses && a.profileID != "" {
		var err error
		a.Executable, err = a.responsesExecutable(ctx, false)
		if err != nil {
			return nil, err
		}
	}

	if a.Descriptor().ProfileOnly {
		return nil, harness.Unsupported("execution-without-profile")
	}
	if opts.CatalogOnly {
		return a.startCatalog(ctx, opts)
	}
	r, err := a.startNative(ctx, opts)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (a Adapter) startNative(ctx context.Context, opts harness.StartOptions) (*adapterRuntime, error) {
	env, _ := a.environment(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	a.launchEnv = env
	titleAdapter := a
	image, err := installedExecutable(a.Executable, env)
	if err != nil {
		return nil, err
	}
	titleAdapter.Executable = image.path
	resolved, _, err := compatibleExecutable(ctx, image.path, env)
	if err != nil {
		var setup *harness.SetupError
		if errors.As(err, &setup) {
			return nil, err
		}
		return nil, &harness.SetupError{Issue: domain.RuntimeIncompatible, Message: "该可执行文件未提供兼容的 Agent 协议，请检查路径或更新安装"}
	}
	a.Executable = resolved
	var executor *Executor
	if opts.Scope != nil {
		executor, err = StartExecutor(ctx, ExecutorOptions{launchEnv: env, Executable: a.Executable, Home: a.Home, Environment: a.runtimeOptions.Environment, Scope: opts.Scope, OnLost: opts.OnLost})
		if err != nil {
			return nil, errors.Join(err, opts.Scope.Stop(ctx))
		}
	}
	r, err := Start(ctx, Options{launchEnv: env, apiPreset: a.apiPreset, Executable: a.Executable, Home: a.Home, Environment: a.runtimeOptions.Environment, Arguments: a.launchArguments(), Instructions: a.runtimeOptions.Instructions, Scope: opts.Scope, Executor: executor, OnStart: opts.OnStart, OnEvent: opts.OnEvent, OnMessage: opts.OnMessage, ImportResource: opts.ImportResource})
	if err != nil {
		if executor != nil {
			executor.Close(ctx)
		}
		if opts.Scope != nil {
			err = errors.Join(err, opts.Scope.Stop(ctx))
		}
		return nil, err
	}
	r.profileID = a.profileID
	after, identityErr := installedExecutable(image.path, env)
	if identityErr != nil || after.version != image.version {
		r.Close()
		if executor != nil {
			executor.Close(ctx)
		}
		return nil, errors.New("Codex 安装正在变化，请稍后重试")
	}
	return &adapterRuntime{version: image.version, Runtime: r, executor: executor, descriptor: a.Descriptor(), titleAdapter: titleAdapter}, nil
}

func (a Adapter) CatalogVersion() (string, error) {
	if a.responses {
		ctx, cancel := context.WithTimeout(context.Background(), shellEnvironmentTimeout+2*time.Second)
		defer cancel()
		if executable, err := a.responsesExecutable(ctx, false); err == nil {
			a.Executable = executable
		} else {
			return "", err
		}
	}

	// Allow the shared probe to finish process cleanup and publish its result.
	ctx, cancel := context.WithTimeout(context.Background(), shellEnvironmentTimeout+time.Second)
	defer cancel()
	env, _ := a.environment(ctx)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	image, err := installedExecutable(a.Executable, env)
	return image.version, err
}
func (r *adapterRuntime) CatalogVersion() (string, error) { return r.version, nil }

type adapterRuntime struct {
	version string
	*Runtime
	executor     *Executor
	descriptor   domain.HarnessDescriptor
	titleAdapter Adapter
	closeMu      sync.Mutex
	closing      *adapterClose
}

type adapterClose struct {
	done chan struct{}
	err  error
}

func (r *adapterRuntime) Readiness() domain.Readiness {
	ready := r.Runtime.Readiness()
	ready.HarnessID = r.descriptor.ID
	goals := ready.Capabilities.Goals
	history := r.supports("thread/read") && r.supports("thread/turns/list")
	fork := history && r.supports("thread/fork") && r.supports("thread/unsubscribe") && r.supports("thread/loaded/list")
	ready.Capabilities = domain.HarnessCapabilities{ToolServices: r.supports("mcpServerStatus/list"), SkillManagement: r.supports("skills/list"), Skills: r.supports("skills/list"), Steer: r.supports("turn/steer"), Interrupt: r.supports("turn/interrupt"), Recovery: history, Continue: history, Reload: history && r.supports("thread/unsubscribe") && r.supports("thread/loaded/list"), Fork: fork, Restart: fork}
	ready.Capabilities.Goals = goals
	ready.Capabilities.ToolServiceInspection = r.supports("config/read")
	ready.Capabilities.PathAttachments = r.supports("turn/start")
	ready.Capabilities.HookManagement = r.supports("hooks/list")
	ready.Capabilities.HookInspection = r.protocol != nil && r.protocol.scopedHooks
	ready.Capabilities.ConversationImport = history && r.supports("thread/list")
	ready.Capabilities.Compact = history && r.supports("thread/compact/start")
	ready.PermissionChoices = []domain.OptionChoice{{ID: "read-only", Name: "只读", Description: "仅查看"}, {ID: "default", Name: "默认权限", Description: "按需请求批准"}, {ID: "auto-approve", Name: "自动批准", Description: "沙盒内自动批准"}, {ID: "yolo", Name: "YOLO", Description: "不限制沙盒或批准"}}
	ready.Harnesses = []domain.HarnessInfo{{Account: ready.Account, ID: r.descriptor.ID, Name: r.descriptor.Name, Icon: r.descriptor.Icon, State: ready.State, Error: ready.Error, Models: ready.Models, Permissions: ready.Permissions, PermissionChoices: ready.PermissionChoices, Plan: ready.Plan, Capabilities: ready.Capabilities}}
	return ready
}
func (r *adapterRuntime) ExecutionReady() bool {
	return !r.Stopped() && (r.scope == nil || r.scope.Admitting() && r.executor != nil && !r.executor.Stopped())
}
func (r *adapterRuntime) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.closeMu.Lock()
	op := r.closing
	if op == nil {
		op = &adapterClose{done: make(chan struct{})}
		r.closing = op
		go func() {
			op.err = r.close(ctx)
			r.closeMu.Lock()
			if r.closing == op {
				r.closing = nil
			}
			close(op.done)
			r.closeMu.Unlock()
		}()
	}
	r.closeMu.Unlock()
	select {
	case <-op.done:
		return op.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *adapterRuntime) close(ctx context.Context) error {
	var result error
	if r.executor != nil {
		result = r.executor.Fence()
	}
	result = errors.Join(result, r.Runtime.close(ctx))
	if r.executor != nil {
		result = errors.Join(result, r.executor.Close(ctx))
	}
	return result
}

func (r *adapterRuntime) ManualShutdownSafe() bool { return r.scope != nil && r.executor != nil }

func (r *adapterRuntime) PrepareHistory(ctx context.Context, source domain.Session, boundary harness.HistoryBoundary) (string, error) {
	if boundary.PreparedRef != "" {
		return boundary.PreparedRef, r.ReleaseFork(ctx, boundary.PreparedRef)
	}
	return r.Fork(ctx, source, boundary.TurnID, boundary.Restart)
}
func (r *adapterRuntime) ActivateHistory(ctx context.Context, source domain.Session, prepared string, commit func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.SwitchHistory(source.ThreadID, prepared, source.ID, commit)
}

func (r *adapterRuntime) Submit(ctx context.Context, s domain.Session, operation string, input harness.TurnInput) (string, error) {
	return r.SubmitWithHandoff(ctx, s, operation, input, nil)
}
func (r *adapterRuntime) Steer(ctx context.Context, s domain.Session, turn, operation string, input harness.TurnInput) error {
	return r.SteerWithHandoff(ctx, s, turn, operation, input, nil)
}
func (r *adapterRuntime) SubmitWithHandoff(ctx context.Context, s domain.Session, operation string, input harness.TurnInput, beforeWrite func() error) (string, error) {
	return r.submit(ctx, s, operation, asyncQuestionInput(input.Text, input.QuestionReply), input.Attachments, input.Skills, beforeWrite)
}
func (r *adapterRuntime) SteerWithHandoff(ctx context.Context, s domain.Session, turn, operation string, input harness.TurnInput, beforeWrite func() error) error {
	return r.steer(ctx, s, turn, operation, asyncQuestionInput(input.Text, input.QuestionReply), input.Attachments, input.Skills, beforeWrite)
}

func (a Adapter) environment(ctx context.Context) ([]string, error) {
	if a.launchEnv != nil {
		return append([]string(nil), a.launchEnv...), nil
	}
	env, err := codexEnvironment(ctx, a.Home, a.runtimeOptions.Environment)
	if a.profileMode != "" {
		env = append(stripProfileEnvironment(env), "CODEX_HOME="+a.Home)
	}
	return env, err
}

func (r *adapterRuntime) PendingOperations() int {
	r.Runtime.mu.Lock()
	n := len(r.Runtime.pendingActivity)
	r.Runtime.mu.Unlock()
	if r.executor != nil {
		r.executor.mu.Lock()
		n += len(r.executor.requests)
		r.executor.mu.Unlock()
	}
	return n
}
