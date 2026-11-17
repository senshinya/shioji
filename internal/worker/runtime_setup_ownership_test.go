package worker

import (
	"context"
	"testing"
	"time"

	"shioji/internal/harness"
)

type cleanupBeforeSetupRuntime struct{ harness.Runtime }

func (*cleanupBeforeSetupRuntime) ExecutionReady() bool { return false }

// Revoke setup while it waits in the real retirement helper, then install a
// successor before giving that old waiter dispatchMu back. This exercises the
// nested helper, before its outer maintenance caller can inspect active().
func TestRuntimeSetupCannotMutateSuccessorAfterCleanupWait(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		for _, starting := range []bool{false, true} {
			t.Run(map[bool]string{false: "ordinary", true: "maintenance"}[maintenance]+"/"+map[bool]string{false: "running", true: "starting"}[starting], func(t *testing.T) {
				s, w := memoryWorker(t, idleHarness{})
				d := submitIdleFixture(t, s, w, "before-setup", "")
				original := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
				entered, release := make(chan struct{}, 1), make(chan struct{})
				original.closing, original.release = entered, release
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				s.runtimeMu.Lock()
				s.runtimes[d.Session.ID] = &cleanupBeforeSetupRuntime{original}
				s.runtimeMu.Unlock()
				done := make(chan error, 1)
				go func() {
					s.dispatchMu.Lock()
					defer s.dispatchMu.Unlock()
					ctx := s.ctx
					if maintenance {
						end, err := s.beginSessionTransition(ctx, d.Session.ID)
						if err != nil {
							done <- err
							return
						}
						defer end()
						ctx = s.sessionTransitions[d.Session.ID].ctx
					}
					_, err := s.sessionRuntime(ctx, d.Session.ID)
					done <- err
				}()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("nested cleanup not entered")
				}
				s.dispatchMu.Lock()
				end, err := s.beginSessionRetirement(s.ctx, d.Session.ID, func() (bool, error) { return true, nil })
				if err == nil {
					err = s.detachRuntimeInstance(d.Session.ID, context.Canceled, false)
				}
				if err != nil {
					s.dispatchMu.Unlock()
					t.Fatal(err)
				}
				scope, err := s.executions.Begin(d.Session.ID)
				if err != nil {
					end()
					s.dispatchMu.Unlock()
					t.Fatal(err)
				}
				gate := &runtimeEventGate{done: make(chan struct{})}
				gate.Store(true)
				successor := &memoryRuntime{adapter: memoryHarness{id: "idle"}, done: make(chan struct{})}
				s.runtimeMu.Lock()
				s.runtimeEvents[d.Session.ID] = gate
				if starting {
					delete(s.runtimes, d.Session.ID)
				} else {
					s.runtimes[d.Session.ID] = successor
				}
				s.runtimeMu.Unlock()
				end()
				close(release)
				s.dispatchMu.Unlock()
				select {
				case err = <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("old setup did not return")
				}
				current, readErr := s.executions.Existing(d.Session.ID)
				detached, detachErr := scope.IsDetached()
				if err == nil || readErr != nil || detachErr != nil || current == nil || current.Generation != scope.Generation || detached || !gate.Load() {
					t.Fatal("old nested setup mutated successor ownership", err, readErr, detachErr, detached, gate.Load())
				}
				if starting {
					if s.currentRuntime(d.Session.ID) != nil {
						t.Fatal("old setup published a runtime into successor initialization")
					}
				} else if s.currentRuntime(d.Session.ID) != successor || successor.Stopped() {
					t.Fatal("old setup replaced the successor")
				}
			})
		}
	}
}
