package worker

import (
	"context"
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
	latest, err := s.store.Session(id, 0, 1)
	if err != nil || ctx.Err() != nil || latest.Session.Archived || !latest.Session.Status.NeedsRecovery() {
		return
	}
	checked := s.recoveryChecked[id]
	if checked.Revision == latest.Session.Revision && checked.Generation >= s.recoveryRefresh.Load() {
		return
	}
	if own := s.currentRuntime(id); own == nil || own.Stopped() {
		catalog := s.catalogRuntime(latest.Session.HarnessID)
		if catalog == nil || catalog.Stopped() {
			updated, err := s.store.SetRecovery(id, &domain.SessionRecovery{Phase: "attention", Message: "暂时无法核对执行状态", NeedsStop: ownedProcessPresent(s.catalogDirectory(latest.Session.HarnessID))})
			if err == nil {
				s.recoveryChecked[id] = recoveryAttempt{Revision: updated.Revision, Generation: s.recoveryRefresh.Load()}
			}
			return
		}
	}
	_, _ = s.reconcileLocked(ctx, protocol.RecoveryInput{SessionID: id, Revision: latest.Session.Revision})
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
