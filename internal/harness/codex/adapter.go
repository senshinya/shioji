package codex

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"sync"
)

// Adapter is configured at composition time. Codex settings never enter the
// worker contract or the generic run configuration.
type Adapter struct {
	Executable, Home  string
	defaultExecutable string
	override          string
	defaultHome       string
	hasDefaultHome    bool
	runtimeOptions    domain.RuntimeOptions
	launchEnv         []string
}

func (a Adapter) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "codex", Name: "Codex", Icon: "openai"}
}
func (a Adapter) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
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
	r, err := Start(ctx, Options{launchEnv: env, Executable: a.Executable, Home: a.Home, Environment: a.runtimeOptions.Environment, Arguments: a.runtimeOptions.Arguments, Instructions: a.runtimeOptions.Instructions, Scope: opts.Scope, Executor: executor, OnStart: opts.OnStart, OnEvent: opts.OnEvent, OnMessage: opts.OnMessage, ImportResource: opts.ImportResource})
	if err != nil {
		if executor != nil {
			executor.Close(ctx)
		}
		if opts.Scope != nil {
			err = errors.Join(err, opts.Scope.Stop(ctx))
		}
		return nil, err
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), shellEnvironmentTimeout)
	defer cancel()
	env, _ := a.environment(ctx)
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
	closeOnce    sync.Once
	closeErr     error
}

func (r *adapterRuntime) Readiness() domain.Readiness {
	ready := r.Runtime.Readiness()
	ready.HarnessID = r.descriptor.ID
	history := r.supports("thread/read") && r.supports("thread/turns/list")
	fork := history && r.supports("thread/fork") && r.supports("thread/unsubscribe") && r.supports("thread/loaded/list")
	ready.Capabilities = domain.HarnessCapabilities{ToolServices: r.supports("mcpServerStatus/list"), SkillManagement: r.supports("skills/list"), Skills: r.supports("skills/list"), Steer: r.supports("turn/steer"), Interrupt: r.supports("turn/interrupt"), Recovery: history, Continue: history, Reload: history && r.supports("thread/unsubscribe") && r.supports("thread/loaded/list"), Fork: fork, Restart: fork}
	ready.Capabilities.ToolServiceInspection = r.supports("config/read")
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
	if r.scope != nil && !r.scope.ForceAllowed() {
		var err error
		if r.executor != nil {
			err = r.executor.Fence()
		}
		err = errors.Join(err, r.Runtime.Close())
		if r.executor != nil {
			err = errors.Join(err, r.executor.Close(ctx))
		}
		return err
	}
	r.closeOnce.Do(func() {
		if r.executor != nil {
			r.closeErr = r.executor.Fence()
		}
		r.closeErr = errors.Join(r.closeErr, r.Runtime.Close())
		if r.executor != nil {
			r.closeErr = errors.Join(r.closeErr, r.executor.Close(ctx))
		}
	})
	return r.closeErr
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
	return r.submit(ctx, s, operation, input.Text, input.Attachments, input.Skills)
}
func (r *adapterRuntime) Steer(ctx context.Context, s domain.Session, turn, operation string, input harness.TurnInput) error {
	return r.steer(ctx, s, turn, operation, input.Text, input.Attachments, input.Skills)
}

func (a Adapter) environment(ctx context.Context) ([]string, error) {
	if a.launchEnv != nil {
		return append([]string(nil), a.launchEnv...), nil
	}
	return codexEnvironment(ctx, a.Home, a.runtimeOptions.Environment)
}
