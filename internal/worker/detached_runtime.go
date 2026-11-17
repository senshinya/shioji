package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"sync"
	"sync/atomic"
	"time"
)

type runtimeEventGate struct {
	atomic.Bool
	activity atomic.Uint64
	mu       sync.RWMutex
	done     chan struct{}
	doneOnce sync.Once
}

func (g *runtimeEventGate) deactivate() {
	g.mu.Lock()
	g.Store(false)
	g.doneOnce.Do(func() { close(g.done) })
	g.mu.Unlock()
}

var errRuntimeRetired = errors.New("运行环境已退休")

// Only short worker-owned writes hold this gate. Native calls may emit gated
// events or ignore cancellation, so they never run under its read lock.
func (g *runtimeEventGate) apply(fn func() error) error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.Load() {
		return errRuntimeRetired
	}
	return fn()
}

type detachedRuntime struct {
	runtime harness.Runtime
	scope   *managedexec.Scope
	prior   *runtimeRetirement
	running bool
}

// Caller holds dispatchMu. Logical release never reuses the failed Agent and
// never rewrites its registered process identities as if physical stop succeeded.
func (s *Server) detachFailedRuntime(id string, cause error) error {
	return s.detachRuntimeInstance(id, cause, true)
}

func (s *Server) detachRuntimeInstance(id string, cause error, releaseExecution bool) error {
	r := s.currentRuntime(id)
	scope, err := s.executions.Existing(id)
	if err != nil || scope == nil {
		return errors.Join(cause, err)
	}
	s.retirementMu.Lock()
	if s.detachedRuntimes == nil {
		s.detachedRuntimes = map[string]*detachedRuntime{}
	}
	retired := &detachedRuntime{runtime: r, scope: scope, prior: s.retirements[id], running: true}
	s.detachedRuntimes[scope.Generation] = retired
	s.retirementMu.Unlock()
	defer func() {
		s.retirementMu.Lock()
		retired.running = false
		s.retirementMu.Unlock()
	}()
	if err = scope.Detach(); err != nil {
		return err
	}
	s.runtimeMu.RLock()
	gate := s.runtimeEvents[id]
	s.runtimeMu.RUnlock()
	if gate != nil {
		gate.deactivate()
	}
	message := "旧 Agent 已退休，原执行结果未确认，不会自动重发"
	if cause != nil {
		message += "：" + cause.Error()
	} else {
		message = "回复已停止，未确认的输入不会自动重发"
	}
	if err = s.store.InterruptCompletion(id); err != nil {
		return err
	}
	if err = s.store.RetireWork(id, cause == nil); err != nil {
		return err
	}
	if releaseExecution {
		if err = s.store.ReleaseRuntime(id, scope.Generation, message, cause == nil); err != nil {
			return err
		}
	}
	s.retirementMu.Lock()
	if s.retirements[id] == retired.prior {
		delete(s.retirements, id)
	}
	s.retirementMu.Unlock()
	s.runtimeMu.Lock()
	delete(s.runtimes, id)
	delete(s.runtimeCatalogs, id)
	delete(s.runtimeEvents, id)
	delete(s.idleRuntimes, id)
	s.runtimeMu.Unlock()
	s.mu.Lock()
	delete(s.bound, id)
	s.mu.Unlock()
	if releaseExecution {
		s.releaseDispatches(id)
	}
	return nil
}

func (s *Server) releaseDispatches(id string) {
	s.mu.Lock()
	for op, session := range s.dispatchReservations {
		if session == id {
			delete(s.dispatchReservations, op)
		}
	}
	s.mu.Unlock()
}

// Cleanup retries run independently of dispatch admission, one attempt at a
// time per retired generation. The generation files survive worker restarts.
func (s *Server) detachedRuntimeLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		pending, err := s.executions.Detached()
		if err != nil {
			fmt.Fprintf(os.Stderr, "shioji: detached execution records: %v\n", err)
			continue
		}
		for _, scope := range pending {
			s.retirementMu.Lock()
			if s.detachedRuntimes == nil {
				s.detachedRuntimes = map[string]*detachedRuntime{}
			}
			r := s.detachedRuntimes[scope.Generation]
			if r == nil {
				r = &detachedRuntime{scope: scope}
				s.detachedRuntimes[scope.Generation] = r
			}
			ready := !r.running
			if ready {
				r.running = true
				attempt := *r
				s.wg.Add(1)
				go func() {
					defer s.wg.Done()
					ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
					err := closeDetachedRuntime(ctx, &attempt)
					cancel()
					if err == nil {
						err = r.scope.ResolveDetached()
					}
					s.retirementMu.Lock()
					r.running = false
					if err == nil {
						delete(s.detachedRuntimes, r.scope.Generation)
					}
					s.retirementMu.Unlock()
				}()
			}
			s.retirementMu.Unlock()
		}
	}
}

func closeDetachedRuntime(ctx context.Context, r *detachedRuntime) error {
	results := make(chan error, 2)
	go func() { results <- r.scope.Terminate(ctx, true) }()
	n := 1
	if r.runtime != nil {
		n++
		go func() {
			if r.prior != nil {
				select {
				case <-r.prior.done:
					if r.prior.err == nil {
						results <- nil
						return
					}
				case <-ctx.Done():
					results <- ctx.Err()
					return
				}
			}
			results <- r.runtime.Close(ctx)
		}()
	}
	var result error
	for range n {
		select {
		case err := <-results:
			result = errors.Join(result, err)
		case <-ctx.Done():
			return errors.Join(result, ctx.Err())
		}
	}
	return result
}
