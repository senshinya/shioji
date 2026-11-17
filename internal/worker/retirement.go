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
	current := s.currentRuntime(id)
	s.retirementMu.Lock()
	defer s.retirementMu.Unlock()
	op := s.retirements[id]
	return op != nil && op.runtime != nil && current != nil && op.runtime.Done() == current.Done()
}

// Callers serialize capture with dispatchMu. A late completion may release only
// the instance captured here. Errors are retryable, never cached permanently.
func (s *Server) retireRuntime(ctx context.Context, id, reason string, force bool) error {
	op, err := s.beginRuntimeRetirement(ctx, id, reason, force)
	if err != nil || op == nil {
		return err
	}
	return waitRuntimeRetirement(ctx, op)
}

// Admission and instance capture stay synchronous; physical cleanup never owns
// the device-wide dispatch lock. Callers recheck captured identities on return.
func (s *Server) retireRuntimeOutsideDispatch(ctx context.Context, id, reason string, force bool) error {
	op, err := s.beginRuntimeRetirement(ctx, id, reason, force)
	if err != nil || op == nil {
		return err
	}
	s.dispatchMu.Unlock()
	defer s.dispatchMu.Lock()
	return waitRuntimeRetirement(ctx, op)
}

func waitRuntimeRetirement(ctx context.Context, op *runtimeRetirement) error {
	select {
	case <-op.done:
		return op.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) beginRuntimeRetirement(ctx context.Context, id, reason string, force bool) (*runtimeRetirement, error) {
	s.retirementMu.Lock()
	if prior := s.retirements[id]; prior != nil {
		s.retirementMu.Unlock()
		return prior, nil
	}
	scope, err := s.executions.Existing(id)
	if err != nil {
		s.retirementMu.Unlock()
		return nil, err
	}
	r := s.currentRuntime(id)
	if scope == nil && r == nil {
		s.retirementMu.Unlock()
		return nil, nil
	}
	if scope != nil {
		if err = scope.Fence(); err != nil {
			s.retirementMu.Unlock()
			return nil, err
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
	collect:
		for range n {
			select {
			case err := <-results:
				op.err = errors.Join(op.err, err)
			case <-work.Done():
				op.err = errors.Join(op.err, work.Err())
				break collect
			}
		}
		// Producers and their synchronous event callbacks have settled. The old
		// gate is closed now, never before the last output has been persisted.
		if op.err == nil {
			var gate *runtimeEventGate
			s.runtimeMu.RLock()
			if current := s.runtimes[id]; current != nil && r != nil && current.Done() == r.Done() {
				gate = s.runtimeEvents[id]
			}
			s.runtimeMu.RUnlock()
			if gate != nil {
				gate.deactivate()
			}
		}
		s.retirementMu.Lock()
		if s.retirements[id] == op {
			delete(s.retirements, id)
		}
		close(op.done)
		s.retirementMu.Unlock()
	}()
	return op, nil
}
