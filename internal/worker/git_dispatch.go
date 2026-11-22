package worker

import (
	"context"
	"shioji/internal/domain"
)

// These helpers require dispatchMu. The caller keeps checkout ownership while
// native Git waits, then revalidates its captured admission before committing.
func (s *Server) queryGitOutsideDispatch(ctx context.Context, dir string) (out domain.GitStatus, err error) {
	return s.readGitOutsideDispatch(ctx, dir, true)
}
func (s *Server) readGitOutsideDispatch(ctx context.Context, dir string, detailed bool) (out domain.GitStatus, err error) {
	s.dispatchMu.Unlock()
	defer s.dispatchMu.Lock()
	return readGitStatus(ctx, dir, detailed)
}
func (s *Server) gitRunOutsideDispatch(ctx context.Context, dir string, args ...string) (string, error) {
	s.dispatchMu.Unlock()
	defer s.dispatchMu.Lock()
	return gitRun(ctx, dir, args...)
}

// All keys are acquired together under dispatchMu. Never wait while retaining
// one key: a worktree and its shared Git directory must not deadlock each other.
func (s *Server) beginGitTransition(checkout, common string) (func(), error) {
	keys := []string{checkout}
	if common != "" && common != checkout {
		keys = append(keys, common)
	}
	for {
		busy := ""
		for _, key := range keys {
			if s.worktreeTransitions[key] != nil {
				busy = key
				break
			}
		}
		if busy == "" {
			break
		}
		if err := s.waitWorktreeTransition(busy); err != nil {
			return nil, err
		}
	}
	var releases []func()
	for _, key := range keys {
		releases = append(releases, s.beginWorktreeTransition(key))
	}
	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}, nil
}

func (s *Server) sameCheckoutOutsideDispatch(cwd, checkout string) bool {
	s.dispatchMu.Unlock()
	defer s.dispatchMu.Lock()
	return sameCheckout(s.ctx, cwd, checkout)
}
