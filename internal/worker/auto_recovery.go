package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"slices"
	"sync"
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
func (s *Server) watchRuntime(r harness.Runtime, sessionIDs ...string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case <-s.ctx.Done():
		case <-r.Done():
			if len(sessionIDs) > 0 {
				s.dispatchMu.Lock()
				current := s.currentRuntime(sessionIDs[0])
				if s.ctx.Err() == nil && !s.backgroundStopping.Load() && current != nil && current.Done() == r.Done() {
					if err := s.store.InterruptCompletion(sessionIDs[0]); err != nil {
						s.cancel()
					}
				}
				s.dispatchMu.Unlock()
			}
			s.wakeRecovery()
		}
	}()
}
func (s *Server) recoveryLoop() {
	defer s.wg.Done()
	s.wakeRecovery()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var mu sync.Mutex
	active := map[string]bool{}
	launch := func(key string, cap int, run func()) bool {
		mu.Lock()
		defer mu.Unlock()
		if active[key] || len(active) >= cap {
			return false
		}
		active[key] = true
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { mu.Lock(); delete(active, key); mu.Unlock() }()
			run()
		}()
		return true
	}
	var cursor string
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.recoveryWake:
		case <-ticker.C:
		}
		if s.updating.Load() || s.backgroundStopping.Load() || s.ctx.Err() != nil {
			continue
		}
		for _, id := range s.adapterOrder {
			if !s.agentEnabled(id) {
				continue
			}
			if catalog := s.catalogRuntime(id); catalog != nil && !catalog.Stopped() {
				continue
			}
			launch("catalog:"+id, 2, func() {
				ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
				defer cancel()
				s.dispatchMu.Lock()
				_, err := s.recoveryRuntime(ctx, false, id)
				s.dispatchMu.Unlock()
				if err == nil {
					s.wakeRecovery()
				}
			})
		}
		ids, err := s.store.RecoverableSessionIDs()
		if err != nil {
			continue
		}
		replies, _ := s.store.PendingReplies()
		replyByID := map[string][]store.ReplyReconciliation{}
		for _, entry := range replies {
			id := entry.Session.ID
			if _, exists := replyByID[id]; !exists && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
			replyByID[id] = append(replyByID[id], entry)
		}
		slices.Sort(ids)
		start, _ := slices.BinarySearch(ids, cursor)
		if start < len(ids) && ids[start] == cursor {
			start++
		}
		ordered := append(slices.Clone(ids[start:]), ids[:start]...)
		for _, id := range ordered {
			entries := replyByID[id]
			if launch("session:"+id, 6, func() {
				s.recoverSession(id)
				for _, entry := range entries {
					if err := s.reconcileReply(entry); err != nil {
						fmt.Fprintf(os.Stderr, "shioji: reply history reconciliation: %v\n", err)
					}
				}
			}) {
				cursor = id
			}
		}
	}
}

func (s *Server) recoverSession(id string) {
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	latest, err := s.store.SessionRecord(id)
	if err != nil || ctx.Err() != nil || latest.Archived {
		return
	}
	if latest.Recovery != nil && latest.Recovery.AutoContinueTurn != "" {
		delete(s.restartRecovery, id)
		s.continueRecoveredSession(latest)
		return
	}
	if !latest.Status.NeedsRecovery() {
		delete(s.restartRecovery, id)
		return
	}
	checked := s.recoveryChecked[id]
	if checked.Revision == latest.Revision && checked.Generation >= s.recoveryRefresh.Load() {
		return
	}
	result, err := s.reconcileLocked(ctx, protocol.RecoveryInput{SessionID: id, Revision: latest.Revision})
	if err == nil && result.CanContinue {
		latest, err = s.store.SessionRecord(id)
		if err == nil && latest.Recovery != nil && latest.Recovery.AutoContinueTurn != "" {
			s.continueRecoveredSession(latest)
		}
	}
}

// The pending turn is committed with recovery. Normal submission consumes it
// atomically on acceptance, so a worker crash cannot duplicate the new input.
// Caller holds dispatchMu. Submission rechecks the candidate and queue at accept.
func (s *Server) continueRecoveredSession(session domain.Session) {
	if s.updateAdmission() != nil || s.ctx.Err() != nil {
		return
	}
	turn := session.Recovery.AutoContinueTurn
	_, err := s.submitLocked(domain.Submission{
		OperationID: domain.RecoveryContinuationID(session.ID, turn),
		SessionID:   session.ID,
		WorkspaceID: session.WorkspaceID,
		HarnessID:   session.HarnessID,
		Config:      session.Config,
		Text:        "continue",
		Continue:    true,
	}, 0, false)
	if err == nil || errors.Is(err, errCatalogRefreshing) || s.updateAdmission() != nil || s.ctx.Err() != nil {
		return
	}
	latest, err := s.store.SessionRecord(session.ID)
	if err != nil || latest.Recovery == nil || latest.Recovery.AutoContinueTurn != turn {
		return
	}
	info := *latest.Recovery
	info.AutoContinueTurn = ""
	if _, err = s.store.SetRecovery(session.ID, &info); err != nil {
		fmt.Fprintf(os.Stderr, "shioji: settle automatic continuation: %v\n", err)
	}
}

func (s *Server) reconcileReply(entry store.ReplyReconciliation) error {
	if s.ctx.Err() != nil {
		return nil
	}
	r := s.currentRuntime(entry.Session.ID)
	release := func() {}
	if r == nil || r.Stopped() {
		var err error
		r, release, err = s.borrowCatalog(entry.Session.HarnessID)
		if err != nil {
			return nil
		}
	}
	defer release()
	if !r.Readiness().Capabilities.Recovery {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	source, err := r.Inspect(ctx, entry.Session)
	if err != nil {
		return nil // Preserve provisional output until a later observation.
	}
	return s.store.ReconcileReplies(entry, source)
}
