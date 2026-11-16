package worker

import (
	"context"
	"errors"
	"shioji/internal/harness"
	"time"
)

type runtimeRetirement struct {
	runtime            harness.Runtime
	generation, reason string
	done               chan struct{}
	err                error
}

func (s *Server) runtimeRetiring(id string) bool {
	s.retirementMu.Lock()
	defer s.retirementMu.Unlock()
	return s.retirements[id] != nil
}

// Callers serialize capture with dispatchMu. A late completion may release only
// the instance captured here. Errors are retryable, never cached permanently.
func (s *Server) retireRuntime(ctx context.Context, id, reason string, force bool) error {
	s.retirementMu.Lock()
	if prior := s.retirements[id]; prior != nil {
		s.retirementMu.Unlock()
		select {
		case <-prior.done:
			return prior.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	scope, err := s.executions.Existing(id)
	if err != nil {
		s.retirementMu.Unlock()
		return err
	}
	r := s.currentRuntime(id)
	if scope == nil && r == nil {
		s.retirementMu.Unlock()
		return nil
	}
	if scope != nil {
		if err = scope.Fence(); err != nil {
			s.retirementMu.Unlock()
			return err
		}
	}
	op := &runtimeRetirement{runtime: r, reason: reason, done: make(chan struct{})}
	if scope != nil {
		op.generation = scope.Generation
	}
	if s.retirements == nil {
		s.retirements = map[string]*runtimeRetirement{}
	}
	s.retirements[id] = op
	s.retirementMu.Unlock()
	// The operation owns its bounded lifetime, independent of RPC disconnection.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(time.Now().Add(5*time.Second)) {
		cancel()
		work, cancel = context.WithDeadline(context.WithoutCancel(ctx), deadline)
	}
	go func() {
		defer cancel()
		results := make(chan error, 2)
		n := 0
		if scope != nil {
			n++
			go func() { results <- scope.Terminate(work, force) }()
		}
		if r != nil {
			n++
			go func() { results <- r.Close(work) }()
		}
		for range n {
			op.err = errors.Join(op.err, <-results)
		}
		// Producers and their synchronous event callbacks have settled. The old
		// gate is closed now, never before the last output has been persisted.
		if op.err == nil {
			s.runtimeMu.Lock()
			if current := s.runtimes[id]; current != nil && r != nil && current.Done() == r.Done() {
				if gate := s.runtimeEvents[id]; gate != nil {
					gate.Store(false)
				}
			}
			s.runtimeMu.Unlock()
			s.mu.Lock()
			delete(s.bound, id)
			s.mu.Unlock()
		}
		s.retirementMu.Lock()
		delete(s.retirements, id)
		close(op.done)
		s.retirementMu.Unlock()
	}()
	select {
	case <-op.done:
		return op.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
