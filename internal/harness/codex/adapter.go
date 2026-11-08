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
type Adapter struct{ Executable, Home string }

func (a Adapter) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "codex", Name: "Codex", Icon: "openai"}
}
func (a Adapter) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	var executor *Executor
	var err error
	if opts.Scope != nil {
		executor, err = StartExecutor(ctx, ExecutorOptions{Executable: a.Executable, Home: a.Home, Scope: opts.Scope, OnLost: opts.OnLost})
		if err != nil {
			return nil, errors.Join(err, opts.Scope.Stop(ctx))
		}
	}
	r, err := Start(ctx, Options{Executable: a.Executable, Home: a.Home, Scope: opts.Scope, Executor: executor, OnStart: opts.OnStart, OnEvent: opts.OnEvent, OnMessage: opts.OnMessage, ImportResource: opts.ImportResource})
	if err != nil {
		if executor != nil {
			executor.Close(ctx)
		}
		if opts.Scope != nil {
			err = errors.Join(err, opts.Scope.Stop(ctx))
		}
		return nil, err
	}
	return &adapterRuntime{Runtime: r, executor: executor, descriptor: a.Descriptor(), titleAdapter: a}, nil
}

type adapterRuntime struct {
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
	ready.Capabilities = domain.HarnessCapabilities{Skills: true, Steer: true, Interrupt: true, Recovery: true, Continue: true, Fork: true, Restart: true}
	ready.PermissionChoices = []domain.OptionChoice{{ID: "read-only", Name: "只读", Description: "仅查看"}, {ID: "default", Name: "默认权限", Description: "按需请求批准"}, {ID: "auto-approve", Name: "自动批准", Description: "沙盒内自动批准"}, {ID: "yolo", Name: "YOLO", Description: "不限制沙盒或批准"}}
	ready.Harnesses = []domain.HarnessInfo{{ID: r.descriptor.ID, Name: r.descriptor.Name, Icon: r.descriptor.Icon, State: ready.State, Error: ready.Error, Models: ready.Models, Permissions: ready.Permissions, PermissionChoices: ready.PermissionChoices, Plan: ready.Plan, Capabilities: ready.Capabilities}}
	return ready
}
func (r *adapterRuntime) ExecutionReady() bool {
	return !r.Stopped() && (r.scope == nil || r.scope.Admitting() && r.executor != nil && !r.executor.Stopped())
}
func (r *adapterRuntime) Close(ctx context.Context) error {
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
