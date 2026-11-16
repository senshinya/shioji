package worker

import (
	"context"
	"errors"
	"shioji/internal/managedexec"
	"sync/atomic"
	"time"
)

var errRuntimeStartCancelled = errors.New("运行环境初始化已停止，输入未发送")

type runtimeStart struct {
	cancel    context.CancelFunc
	cancelled atomic.Bool
	gate      *atomic.Bool
	scope     *managedexec.Scope
}

func (s *Server) stopInitializing(id string, start *runtimeStart) error {
	if _, err := s.store.PauseQueue(id, "stopped"); err != nil {
		return err
	}
	if err := start.scope.RecordCancellation("initializing"); err != nil {
		return err
	}
	if err := start.scope.Fence(); err != nil {
		return err
	}
	if err := s.store.StopInitializing(id); err != nil {
		return err
	}
	start.cancelled.Store(true)
	start.gate.Store(false)
	start.cancel()
	s.runtimeMu.Lock()
	if s.runtimeStarts[id] == start {
		delete(s.runtimeStarts, id)
	}
	s.runtimeMu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := start.scope.Terminate(ctx, true); err != nil {
			// Never guard a newly started generation with a late old result.
			s.dispatchMu.Lock()
			if current, e := s.executions.Existing(id); e == nil && current != nil && current.Generation == start.scope.Generation {
				_ = s.store.GuardExecution(id, current.Generation)
				s.wakeRecovery()
			}
			s.dispatchMu.Unlock()
		}
	}()
	return nil
}
