package worker

import (
	"fmt"
	"os"
	"time"
)

type worktreeCollection struct {
	done chan struct{}
	err  error
}

func (s *Server) wakeWorktreeGC() {
	select {
	case s.worktreeWake <- struct{}{}:
	default:
	}
}

// One consumer plus a one-element wake channel coalesces requests received
// during a sweep. It owns retries independently of session retirement.
func (s *Server) worktreeLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.worktreeWake:
		}
		if err := s.collectOwnedWorktrees(time.Now()); err != nil && s.ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "shioji: worktree cleanup: %v\n", err)
		}
	}
}
func (s *Server) collectOwnedWorktrees(now time.Time) error {
	s.dispatchMu.Lock()
	if s.ctx.Err() != nil || s.updating.Load() || s.backgroundStopping.Load() {
		s.dispatchMu.Unlock()
		return nil
	}
	if active := s.worktreeCollection; active != nil {
		s.dispatchMu.Unlock()
		select {
		case <-active.done:
			return active.err
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	active := &worktreeCollection{done: make(chan struct{})}
	s.worktreeCollection = active
	defer s.dispatchMu.Unlock()
	defer func() { s.worktreeCollection = nil; close(active.done) }()
	active.err = s.cleanOwnedWorktrees(now)
	return active.err
}
