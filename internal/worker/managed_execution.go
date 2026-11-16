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
					deleted, e := s.store.SessionDeleted(ownership.Session)
					if e != nil {
						s.cancel()
					}
					if !deleted && e == nil {
						if e := s.store.GuardExecution(ownership.Session, ownership.Generation); e != nil {
							s.cancel()
						}
						s.wakeRecovery()
					}
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
		deleted, e := s.store.SessionDeleted(id)
		if e != nil {
			return e
		}
		// Deleted generations belong to the durable archive cleanup outbox;
		// they must never restore history or prevent worker startup.
		if deleted {
			continue
		}
		scope, scopeErr := s.executions.Existing(id)
		if scopeErr == nil && scope != nil {
			manual, e := scope.ManualStopPending()
			if e != nil {
				return e
			}
			if manual {
				continue
			}
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

// Caller holds dispatchMu while capturing and fencing this generation.
func (s *Server) cleanManagedExecution(ctx context.Context, id string) error {
	return s.retireRuntime(ctx, id, "cleanup", true)
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
	source.ExplicitlyStopped, err = scope.ExplicitStopProven()
	if err != nil {
		return err
	}
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
