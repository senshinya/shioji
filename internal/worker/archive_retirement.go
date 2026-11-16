package worker

import (
	"context"
	"errors"
	"time"
)

func (s *Server) archiveGeneration(id string) (string, error) {
	scope, err := s.executions.Existing(id)
	if err != nil || scope == nil {
		return "", err
	}
	return scope.Generation, nil
}
func (s *Server) wakeArchiveRetirement() {
	select {
	case s.archiveWake <- struct{}{}:
	default:
	}
}

// Caller holds dispatchMu. Archive/restoration never launches a runtime. Empty
// generation means no environment existed at acceptance, never "whatever is live".
func (s *Server) cleanRuntimeRetirements(only string) error {
	pending, err := s.store.PendingRuntimeRetirements()
	if err != nil {
		return err
	}
	var result error
	for _, r := range pending {
		if only != "" && r.SessionID != only {
			continue
		}
		var failure error
		scope, e := s.executions.Existing(r.SessionID)
		if e != nil {
			failure = e
		} else if scope != nil && r.Generation != "" && scope.Generation == r.Generation {
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			failure = s.retireRuntime(ctx, r.SessionID, r.Reason, true)
			cancel()
			if failure == nil {
				s.runtimeMu.Lock()
				if old := s.runtimes[r.SessionID]; old != nil && old.Stopped() {
					delete(s.runtimes, r.SessionID)
					delete(s.runtimeCatalogs, r.SessionID)
					delete(s.runtimeEvents, r.SessionID)
				}
				s.runtimeMu.Unlock()
			}
		}
		failure = errors.Join(failure, s.store.CompleteRuntimeRetirement(r, failure))
		result = errors.Join(result, failure)
	}
	return result
}
