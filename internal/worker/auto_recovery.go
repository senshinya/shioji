package worker

import (
	"context"
	"fmt"
	"os"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"time"
)

type recoveryAttempt struct {
	Revision   int64
	Generation uint64
}

func (s *Server) wakeRecovery() {
	select {
	case s.recoveryWake <- struct{}{}:
	default:
	}
}
func (s *Server) watchRuntime(r harness.Runtime) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case <-s.ctx.Done():
		case <-r.Done():
			s.wakeRecovery()
		}
	}()
}
func (s *Server) recoveryLoop() {
	defer s.wg.Done()
	s.wakeRecovery()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.recoveryWake:
		}
		snapshot, err := s.store.Snapshot()
		if err != nil {
			continue
		}

		// Prepare each harness independently. This also recovers an exited idle runtime,
		// while a launch failure is shared by all affected sessions.
		s.dispatchMu.Lock()
		prepareErrors := map[string]error{}
		for _, id := range s.adapterOrder {
			if !s.agentEnabled(id) {
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
			_, prepareErrors[id] = s.recoveryRuntime(ctx, false, id)
			cancel()
		}
		s.dispatchMu.Unlock()
		// Transcript correction is read-only at the harness boundary and does not
		// hold dispatch admission while waiting for history RPCs.
		if err := s.reconcileReplies(); err != nil {
			fmt.Fprintf(os.Stderr, "shioji: reply history reconciliation: %v\n", err)
		}
		for _, session := range snapshot.Sessions {
			if session.Archived || !session.Status.NeedsRecovery() {
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
			s.dispatchMu.Lock()
			latest, e := s.store.Session(session.ID, 0, 1)
			if e == nil && ctx.Err() == nil && (s.recoveryChecked[session.ID].Revision != latest.Session.Revision || s.recoveryChecked[session.ID].Generation < s.recoveryRefresh.Load()) {
				if own := s.currentRuntime(session.ID); prepareErrors[session.HarnessID] != nil && (own == nil || own.Stopped()) {
					updated, writeErr := s.store.SetRecovery(session.ID, &domain.SessionRecovery{Phase: "attention", Message: "暂时无法核对执行状态", NeedsStop: ownedProcessPresent(s.catalogDirectory(session.HarnessID))})
					if writeErr == nil {
						s.recoveryChecked[session.ID] = recoveryAttempt{Revision: updated.Revision, Generation: s.recoveryRefresh.Load()}
					}
				} else {
					_, e = s.reconcileLocked(ctx, protocol.RecoveryInput{SessionID: session.ID, Revision: latest.Session.Revision})
				}
			}
			s.dispatchMu.Unlock()
			cancel()
		}
	}
}

func (s *Server) reconcileReplies() error {
	entries, err := s.store.PendingReplies()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if s.ctx.Err() != nil {
			return nil
		}
		r := s.currentRuntime(entry.Session.ID)
		release := func() {}
		if r == nil || r.Stopped() {
			r, release, err = s.borrowCatalog(entry.Session.HarnessID)
			if err != nil {
				continue
			}
		}
		if !r.Readiness().Capabilities.Recovery {
			release()
			continue
		}
		ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
		source, inspectErr := r.Inspect(ctx, entry.Session)
		cancel()
		release()
		if inspectErr != nil {
			continue
		} // Preserve provisional output until a later reconnect/event.
		if err = s.store.ReconcileReplies(entry, source); err != nil {
			return err
		}
	}
	return nil
}
