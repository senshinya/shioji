package codex

import (
	"context"
	"errors"
	"sync"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

// A catalog owns cached discovery, never a resident Agent. Native service
// operations get separate probes, retained here until physical cleanup succeeds.
type catalogRuntime struct {
	adapter  Adapter
	options  harness.StartOptions
	ready    domain.Readiness
	version  string
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	active   map[*adapterRuntime]*catalogProbe
	starting int
	closing  bool
	err      error
	done     chan struct{}
	closed   bool
}
type catalogProbe struct {
	runtime        *adapterRuntime
	ctx            context.Context
	cancel         context.CancelFunc
	stop           func() bool
	inUse, reaping bool
}

func (a Adapter) startCatalog(ctx context.Context, options harness.StartOptions) (harness.Runtime, error) {
	options.CatalogOnly = false
	r, err := a.startNative(ctx, options)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c := &catalogRuntime{adapter: a, options: options, ready: r.describeReadiness(r.nativeReadiness()), version: r.version, ctx: life, cancel: cancel, active: map[*adapterRuntime]*catalogProbe{}, done: make(chan struct{})}
	p := &catalogProbe{runtime: r, ctx: ctx, cancel: func() {}, stop: func() bool { return true }, inUse: true}
	c.active[r] = p
	err = c.release(p)
	c.err = err
	go c.reaper()
	return c, err
}
func (c *catalogRuntime) Readiness() domain.Readiness {
	c.mu.Lock()
	defer c.mu.Unlock()
	ready := c.ready
	if c.closing || c.err != nil {
		ready.State = domain.RuntimeUnavailable
		ready.Error = "目录探测暂不可用"
		if c.err != nil {
			ready.Error = c.err.Error()
		}
	}
	return harness.ApplyProfileEfforts(c.adapter.effortHome(), ready)
}
func (c *catalogRuntime) Validate(config domain.RunConfig) error {
	return validateCodexConfig(c.Readiness(), config)
}
func (c *catalogRuntime) CatalogVersion() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version, c.err
}
func (c *catalogRuntime) Done() <-chan struct{} { return c.done }
func (c *catalogRuntime) Stopped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}
func (c *catalogRuntime) ExecutionReady() bool         { return false }
func (c *catalogRuntime) ManagedCoverage() bool        { return false }
func (c *catalogRuntime) Observes(domain.Session) bool { return false }
func (c *catalogRuntime) finishLocked() {
	if c.closing && c.starting == 0 && len(c.active) == 0 && !c.closed {
		c.closed = true
		close(c.done)
	}
}
func (c *catalogRuntime) acquire(ctx context.Context) (*catalogProbe, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return nil, errors.New("目录已关闭")
	}
	c.starting++
	c.mu.Unlock()
	query, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	r, err := c.adapter.startNative(query, c.options)
	c.mu.Lock()
	c.starting--
	if err != nil {
		if query.Err() == nil {
			c.err = err
		}
		c.finishLocked()
		c.mu.Unlock()
		stop()
		cancel()
		return nil, err
	}
	p := &catalogProbe{runtime: r, ctx: query, cancel: cancel, stop: stop, inUse: true}
	c.active[r] = p
	closing := c.closing
	c.mu.Unlock()
	if closing || query.Err() != nil || r.version != c.version {
		cause := query.Err()
		if cause == nil {
			cause = errors.New("Agent 安装已变化，请重新检测")
		}
		return nil, errors.Join(cause, c.release(p))
	}
	return p, nil
}
func (c *catalogRuntime) release(p *catalogProbe) error {
	p.stop()
	p.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := p.runtime.Close(ctx)
	cancel()
	c.mu.Lock()
	p.inUse = false
	if err == nil {
		delete(c.active, p.runtime)
	}
	c.finishLocked()
	c.mu.Unlock()
	return err
}
func (c *catalogRuntime) reap(ctx context.Context, p *catalogProbe) {
	err := p.runtime.Close(ctx)
	c.mu.Lock()
	p.reaping = false
	if err == nil && !p.inUse {
		delete(c.active, p.runtime)
	}
	c.finishLocked()
	c.mu.Unlock()
}
func (c *catalogRuntime) reaper() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
		c.mu.Lock()
		var pending []*catalogProbe
		for _, p := range c.active {
			if !p.inUse && !p.reaping {
				p.reaping = true
				pending = append(pending, p)
			}
		}
		c.mu.Unlock()
		for _, p := range pending {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				c.reap(ctx, p)
			}()
		}
	}
}
func (c *catalogRuntime) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closing = true
	c.cancel()
	c.mu.Unlock()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		c.mu.Lock()
		var pending []*catalogProbe
		for _, p := range c.active {
			if !p.reaping {
				p.reaping = true
				pending = append(pending, p)
			}
		}
		c.finishLocked()
		c.mu.Unlock()
		for _, p := range pending {
			go c.reap(ctx, p)
		}
		select {
		case <-c.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func catalogQuery[T any](ctx context.Context, c *catalogRuntime, run func(context.Context, *adapterRuntime) (T, error)) (T, error) {
	var zero T
	p, err := c.acquire(ctx)
	if err != nil {
		return zero, err
	}
	value, err := run(p.ctx, p.runtime)
	cleanup := c.release(p)
	return value, errors.Join(err, cleanup)
}
func catalogAction(ctx context.Context, c *catalogRuntime, run func(context.Context, *adapterRuntime) error) error {
	_, err := catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (struct{}, error) { return struct{}{}, run(ctx, r) })
	return err
}

func (c *catalogRuntime) Create(context.Context, domain.Session) (string, error) {
	return "", harness.Unsupported("catalog-execution")
}
func (c *catalogRuntime) Resume(context.Context, domain.Session) error {
	return harness.Unsupported("catalog-execution")
}
func (c *catalogRuntime) Submit(context.Context, domain.Session, string, harness.TurnInput) (string, error) {
	return "", harness.Unsupported("catalog-execution")
}
func (c *catalogRuntime) Steer(context.Context, domain.Session, string, string, harness.TurnInput) error {
	return harness.Unsupported("catalog-execution")
}
func (c *catalogRuntime) Respond(domain.Answer) error {
	return harness.Unsupported("catalog-execution")
}
func (c *catalogRuntime) ActivateHistory(context.Context, domain.Session, string, func() error) error {
	return harness.Unsupported("catalog-execution")
}
func (c *catalogRuntime) Inspect(ctx context.Context, s domain.Session) (domain.RecoveryHistory, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.RecoveryHistory, error) { return r.Inspect(ctx, s) })
}
func (c *catalogRuntime) Interrupt(ctx context.Context, s domain.Session, turn string) error {
	return catalogAction(ctx, c, func(ctx context.Context, r *adapterRuntime) error { return r.Interrupt(ctx, s, turn) })
}
func (c *catalogRuntime) PrepareHistory(ctx context.Context, s domain.Session, b harness.HistoryBoundary) (string, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (string, error) { return r.PrepareHistory(ctx, s, b) })
}
func (c *catalogRuntime) GenerateTitle(ctx context.Context, in harness.TitleInput) (string, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (string, error) { return r.GenerateTitle(ctx, in) })
}
func (c *catalogRuntime) Skills(ctx context.Context, in domain.LaunchContext) (domain.SkillCatalog, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.SkillCatalog, error) { return r.Skills(ctx, in) })
}
func (c *catalogRuntime) SkillInventory(ctx context.Context, in domain.LaunchContext) (domain.SkillInventory, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.SkillInventory, error) {
		return r.SkillInventory(ctx, in)
	})
}
func (c *catalogRuntime) ReadSkill(ctx context.Context, in domain.LaunchContext, id string) (domain.SkillDocument, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.SkillDocument, error) {
		return r.ReadSkill(ctx, in, id)
	})
}
func (c *catalogRuntime) ChangeSkill(ctx context.Context, in domain.LaunchContext, change domain.SkillChange) (domain.SkillChangeResult, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.SkillChangeResult, error) {
		return r.ChangeSkill(ctx, in, change)
	})
}
func (c *catalogRuntime) Hooks(ctx context.Context, in domain.LaunchContext, source string) (domain.HookCatalog, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.HookCatalog, error) {
		return r.Hooks(ctx, in, source)
	})
}
func (c *catalogRuntime) ChangeHook(ctx context.Context, in domain.LaunchContext, change domain.HookChange) (domain.HookChangeResult, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.HookChangeResult, error) {
		return r.ChangeHook(ctx, in, change)
	})
}
func (c *catalogRuntime) RefreshHooks(ctx context.Context) error {
	return catalogAction(ctx, c, func(ctx context.Context, r *adapterRuntime) error { return r.RefreshHooks(ctx) })
}
func (c *catalogRuntime) ToolServices(ctx context.Context) (domain.ToolServiceCatalog, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.ToolServiceCatalog, error) {
		return r.ToolServices(ctx)
	})
}
func (c *catalogRuntime) RefreshToolServices(ctx context.Context) error {
	return catalogAction(ctx, c, func(ctx context.Context, r *adapterRuntime) error { return r.RefreshToolServices(ctx) })
}
func (c *catalogRuntime) InspectToolServices(ctx context.Context, in domain.LaunchContext, source string) (domain.ToolServiceInspection, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.ToolServiceInspection, error) {
		return r.InspectToolServices(ctx, in, source)
	})
}
func (c *catalogRuntime) ChangeToolService(ctx context.Context, in domain.ToolServiceChange) (domain.ToolServiceChangeResult, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.ToolServiceChangeResult, error) {
		return r.ChangeToolService(ctx, in)
	})
}
func (c *catalogRuntime) Conversations(ctx context.Context, roots []string, cursor string) (domain.ConversationCatalog, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.ConversationCatalog, error) {
		return r.Conversations(ctx, roots, cursor)
	})
}
func (c *catalogRuntime) ReadConversation(ctx context.Context, in domain.LaunchContext, source string) (domain.ConversationPreview, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.ConversationPreview, error) {
		return r.ReadConversation(ctx, in, source)
	})
}
func (c *catalogRuntime) ImportConversation(ctx context.Context, in domain.LaunchContext, source string) (domain.ConversationPreview, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.ConversationPreview, error) {
		return r.ImportConversation(ctx, in, source)
	})
}
func (c *catalogRuntime) InspectCompaction(ctx context.Context, s domain.Session, a domain.ContextAction) (domain.ContextObservation, error) {
	return catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (domain.ContextObservation, error) {
		return r.InspectCompaction(ctx, s, a)
	})
}
func (c *catalogRuntime) Compact(context.Context, domain.Session) error {
	return harness.Unsupported("catalog-execution")
}

type catalogAuthorization struct {
	c    *catalogRuntime
	p    *catalogProbe
	auth harness.ToolServiceAuthorization
	once sync.Once
	err  error
}

func (c *catalogRuntime) BeginToolServiceAuthorization(ctx context.Context, id string) (harness.ToolServiceAuthorization, domain.SignInChallenge, error) {
	p, err := c.acquire(ctx)
	if err != nil {
		return nil, domain.SignInChallenge{}, err
	}
	auth, challenge, err := p.runtime.BeginToolServiceAuthorization(p.ctx, id)
	if err != nil {
		return nil, challenge, errors.Join(err, c.release(p))
	}
	owner := &catalogAuthorization{c: c, p: p, auth: auth}
	go func() { <-p.ctx.Done(); owner.finish() }()
	return owner, challenge, nil
}
func (a *catalogAuthorization) Wait(ctx context.Context) error {
	wait, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.p.ctx, cancel)
	defer func() { stop(); cancel() }()
	err := a.auth.Wait(wait)
	a.finish()
	return errors.Join(err, a.err)
}

func (a *catalogAuthorization) finish() { a.once.Do(func() { a.err = a.c.release(a.p) }) }
