package worker

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/managedexec"
)

func (s *Server) reapExecutions() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
			// Failure preserves ownership records; reconciliation remains strict.
			if err := s.executions.Reap(ctx); err != nil {
				var ownership *managedexec.OwnershipError
				if errors.As(err, &ownership) {
					if e := s.store.GuardExecution(ownership.Session, ownership.Generation); e != nil {
						s.cancel()
					}
					s.wakeRecovery()
				}
			}
			cancel()
		}
	}
}

func (s *Server) guardManagedExecution(id string) error {
	d, err := s.store.Session(id, 0, 1)
	if err != nil {
		return err
	}
	scope, err := s.executions.Existing(id)
	if err != nil || scope == nil {
		return err
	}
	if !d.Session.Status.BlocksCheckout() && !scope.Admitting() {
		return nil
	}
	return s.store.GuardExecution(id, scope.Generation)
}

// A completed conversation can still own MCP servers and background commands.
// Reopen every durable scope, independent of the selected or visible session.
func (s *Server) recoverManagedScopes(ctx context.Context) error {
	entries, err := os.ReadDir(s.executions.Directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		scope, scopeErr := s.executions.Existing(id)
		if scopeErr == nil && scope != nil {
			scopeErr = scope.Stop(ctx)
		}
		if scopeErr != nil {
			generation := "unverified"
			if scope != nil {
				generation = scope.Generation
			}
			if err := s.store.GuardExecution(id, generation); err != nil {
				return err
			}
			if _, err := s.store.SetRecovery(id, &domain.SessionRecovery{Phase: "attention", Message: "旧工具尚未确认停止，请重新核对"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Caller holds dispatchMu. Admission is fenced before either protocol is closed.
func (s *Server) cleanManagedExecution(ctx context.Context, id string) error {
	scope, err := s.executions.Existing(id)
	if err != nil || scope == nil {
		return err
	}
	if err = scope.Fence(); err != nil {
		return err
	}
	if runtime := s.currentRuntime(id); runtime != nil {
		// This instance is being deliberately retired after admission is fenced.
		// Its final invalidation must not guard the next accepted generation.
		s.runtimeMu.RLock()
		events := s.runtimeEvents[id]
		s.runtimeMu.RUnlock()
		if events != nil {
			events.Store(false)
		}
		runtime.Close(ctx)
	}
	return scope.Stop(ctx)
}

func (s *Server) coverOperation(id, operation string) error {
	scope, err := s.executions.Existing(id)
	if err != nil {
		return err
	}
	if scope == nil {
		return nil
	}
	return scope.Cover(operation)
}

func (s *Server) managedStopProof(id string, source *domain.RecoveryHistory) error {
	scope, err := s.executions.Existing(id)
	if err != nil || scope == nil {
		return err
	}
	if err = scope.VerifyStopped(); err != nil {
		return err
	}
	source.StoppedGeneration = scope.Generation
	source.ManagedStoppedTurns = map[string]bool{}
	uncovered := map[string]bool{}
	for marker, turn := range source.Inputs {
		if op, ok := strings.CutPrefix(marker, "input:"); ok && scope.Covered(op) {
			source.ManagedStoppedTurns[turn] = true
		} else {
			uncovered[turn] = true
		}
	}
	for turn := range uncovered {
		delete(source.ManagedStoppedTurns, turn)
	}
	return nil
}
