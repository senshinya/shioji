package worker

import (
	"context"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/supervisor"
	"sync/atomic"
	"testing"
	"time"
)

type handoffCatalog struct {
	memoryHarness
	blocked atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (a *handoffCatalog) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	if o.CatalogOnly && a.blocked.Load() {
		select {
		case a.entered <- struct{}{}:
		default:
		}
		select {
		case <-a.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return a.memoryHarness.Start(ctx, o)
}

func TestCatalogNativeStartDoesNotHoldDrainAdmission(t *testing.T) {
	for _, route := range []string{"recovery", "recheck"} {
		t.Run(route, func(t *testing.T) {
			a := &handoffCatalog{memoryHarness: memoryHarness{id: "handoff-catalog"}, entered: make(chan struct{}, 1), release: make(chan struct{})}
			s, _ := memoryWorker(t, a)
			s.config.ServiceInstance = "handoff-instance"
			s.dispatchMu.Lock()
			s.dispatchMu.Unlock()
			a.blocked.Store(true)
			finished := make(chan struct{}, 1)
			if route == "recovery" {
				if err := s.catalogRuntime("handoff-catalog").Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				s.wakeRecovery()
			} else {
				go func() { s.recheckRuntime("handoff-catalog"); finished <- struct{}{} }()
			}
			select {
			case <-a.entered:
			case <-time.After(time.Second):
				t.Fatal("native directory startup not entered")
			}
			released := false
			defer func() {
				if !released {
					close(a.release)
				}
			}()
			idle, err := s.businessStopReady()
			if err != nil || !idle.Ready {
				t.Fatal("fixture has actual business work", idle, err)
			}
			drained := make(chan error, 1)
			go func() {
				ready, e := s.DrainService(supervisor.Control{Instance: s.config.ServiceInstance, OperationID: "handoff-drain", Replacement: domain.WorkerReplacement{Policy: "when-idle"}})
				if e == nil && !ready.Ready {
					e = context.DeadlineExceeded
				}
				drained <- e
			}()
			blocked := false
			select {
			case err = <-drained:
			case <-time.After(200 * time.Millisecond):
				blocked = true
			}
			close(a.release)
			released = true
			if blocked {
				select {
				case err = <-drained:
				case <-time.After(2 * time.Second):
					t.Fatal("drain did not recover")
				}
			}
			// The same per-harness gate proves startup finished, without polling timing.
			s.catalogGates["handoff-catalog"].Lock()
			s.catalogGates["handoff-catalog"].Unlock()
			if route == "recheck" {
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Fatal("recheck did not finish")
				}
			}
			t.Logf("route=%s businessIdle=%v nativeWaitBlockedDrain=%v drainError=%v", route, idle.Ready, blocked, err)
			if blocked || err != nil {
				t.Error("read-only native directory start held device-wide lifecycle admission")
			}
			if current := s.catalogRuntime("handoff-catalog"); current != nil && !current.Stopped() {
				t.Error("late directory startup published a live runtime after drain was admitted")
			}
		})
	}
}
