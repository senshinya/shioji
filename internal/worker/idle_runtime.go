package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"shioji/internal/harness"
	"shioji/internal/store"
)

const sessionRuntimeIdleTimeout = 20 * time.Minute
const sessionRuntimeIdleCheck = 30 * time.Second

type idleRuntimeObservation struct {
	runtime  harness.Runtime
	activity store.RuntimeIdleState
	since    time.Time
}

func (s *Server) idleRuntimeLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(sessionRuntimeIdleCheck)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.reclaimIdleRuntimes(time.Now); err != nil {
				fmt.Fprintf(os.Stderr, "shioji: idle runtime cleanup: %v\n", err)
			}
		}
	}
}

// The worker owns this clock; viewing a session or disconnecting its clients
// does not affect it. dispatchMu serializes final fencing with all submissions.
func (s *Server) reclaimIdleRuntimes(now func() time.Time) error {
	s.runtimeMu.RLock()
	ids := make([]string, 0, len(s.runtimes))
	for id := range s.runtimes {
		ids = append(ids, id)
	}
	s.runtimeMu.RUnlock()
	var result error
	for _, id := range ids {
		s.dispatchMu.Lock()
		err := s.reclaimIdleRuntime(id, now())
		s.dispatchMu.Unlock()
		result = errors.Join(result, err)
	}
	return result
}

// Caller holds dispatchMu, including final state and process ownership checks.
func (s *Server) reclaimIdleRuntime(id string, now time.Time) (result error) {
	if s.ctx.Err() != nil || s.updating.Load() || s.backgroundStopping.Load() {
		return nil
	}
	if s.idleRuntimes == nil {
		s.idleRuntimes = map[string]idleRuntimeObservation{}
	}
	r := s.currentRuntime(id)
	if r == nil {
		delete(s.idleRuntimes, id)
		return nil
	}
	if !r.ManagedCoverage() || !r.Readiness().Capabilities.Continue {
		delete(s.idleRuntimes, id)
		return nil
	}
	if pending, ok := r.(harness.PendingRuntime); ok && pending.PendingOperations() > 0 {
		delete(s.idleRuntimes, id)
		return nil
	}
	activity, err := s.store.RuntimeGCIdle(id)
	if err != nil || !activity.Idle {
		delete(s.idleRuntimes, id)
		return err
	}
	scope, err := s.executions.Existing(id)
	if err != nil || scope == nil {
		delete(s.idleRuntimes, id)
		return err
	}
	// Source callbacks can have arrived while process admission was fenced.
	latest, err := s.store.RuntimeGCIdle(id)
	if err != nil || latest != activity || !latest.Idle {
		delete(s.idleRuntimes, id)
		return err
	}
	previous, seen := s.idleRuntimes[id]
	if !seen || previous.runtime.Done() != r.Done() || previous.activity != activity {
		s.idleRuntimes[id] = idleRuntimeObservation{runtime: r, activity: activity, since: now}
		return nil
	}
	timeout := sessionRuntimeIdleTimeout
	if activity.Background {
		timeout = 24 * time.Hour
	}
	if now.Sub(previous.since) < timeout {
		return nil
	}

	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	err = s.cleanManagedExecution(ctx, id)
	cancel()
	if err != nil {
		err = errors.Join(err, s.detachFailedRuntime(id, err))
		return err
	}
	delete(s.idleRuntimes, id)
	// Keep durable scope/history records. A later accepted input starts a new
	// generation and resumes the same opaque history reference through adapter.
	s.runtimeMu.Lock()
	delete(s.runtimes, id)
	delete(s.runtimeCatalogs, id)
	delete(s.runtimeEvents, id)
	s.runtimeMu.Unlock()
	s.mu.Lock()
	delete(s.bound, id)
	s.mu.Unlock()
	return nil
}
