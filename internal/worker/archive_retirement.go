package worker

import (
	"context"
	"errors"
	"shioji/internal/store"
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
		failure := s.cleanRuntimeRetirement(r)

		result = errors.Join(result, failure)
	}
	return result
}

func (s *Server) cleanRuntimeRetirement(r store.RuntimeRetirement) error {
	release, err := s.beginSessionTransition(s.ctx, r.SessionID)
	if err != nil {
		return err
	}
	defer release()
	// The list may have been selected before another collector acquired the
	// transition. A completed row has no authority over a restored successor.
	if pending, err := s.store.RuntimeRetirementPending(r); err != nil || !pending {
		return err
	}
	scope, err := s.executions.Existing(r.SessionID)
	if err != nil {
		return errors.Join(err, s.store.CompleteRuntimeRetirement(r, err))
	}
	d, err := s.store.Session(r.SessionID, 0, 1)
	if err != nil {
		return err
	}
	needsRelease := d.Session.Status.BlocksCheckout() || d.Session.ExecutionGuard != ""
	for _, request := range d.Requests {
		needsRelease = needsRelease || request.State.AwaitingResolution()
	}
	for _, work := range d.Works {
		needsRelease = needsRelease || work.State == "starting" || work.State == "running" || work.State == "waiting"
	}
	if scope == nil || r.Generation == "" {
		// Archive can arrive after durable acceptance but before Begin creates
		// any environment. Retire that reservation without ever launching it.
		if needsRelease && scope == nil {
			if err = s.store.ReleaseRuntime(r.SessionID, r.Generation, "会话已归档；未确认的输入不会自动重发", r.Generation == ""); err != nil {
				return err
			}
			s.releaseDispatches(r.SessionID)
		}
		return s.store.CompleteRuntimeRetirement(r, nil)
	}
	if scope.Generation != r.Generation {
		return s.store.CompleteRuntimeRetirement(r, nil)
	}
	s.runtimeMu.RLock()
	pending := s.runtimeStarts[r.SessionID]
	s.runtimeMu.RUnlock()
	if pending != nil && pending.scope.Generation == r.Generation {
		if err = s.stopInitializing(r.SessionID, pending); err != nil {
			return err
		}
	}
	old := s.currentRuntime(r.SessionID)
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	failure := s.retireRuntimeOutsideDispatch(ctx, r.SessionID, r.Reason, true)
	cancel()
	deleted, err := s.store.SessionDeleted(r.SessionID)
	if err != nil {
		return err
	}
	needsRelease = needsRelease && !deleted
	if current := s.currentRuntime(r.SessionID); old != nil && current != nil && current.Done() != old.Done() {
		return s.store.CompleteRuntimeRetirement(r, nil)
	}
	if failure != nil {
		if err = s.detachRuntimeInstance(r.SessionID, failure, needsRelease); err != nil {
			return errors.Join(failure, err)
		}
	} else {
		if needsRelease {
			if err = s.store.ReleaseRuntime(r.SessionID, r.Generation, "会话已归档，回复已停止；未确认的输入不会自动重发", true); err != nil {
				return err
			}
			s.releaseDispatches(r.SessionID)
		}
		s.runtimeMu.Lock()
		if old == nil || old.Stopped() {
			delete(s.runtimes, r.SessionID)
			delete(s.runtimeCatalogs, r.SessionID)
			delete(s.runtimeEvents, r.SessionID)
		}
		s.runtimeMu.Unlock()
		delete(s.idleRuntimes, r.SessionID)
		s.mu.Lock()
		delete(s.bound, r.SessionID)
		s.mu.Unlock()
	}
	return s.store.CompleteRuntimeRetirement(r, nil)
}
