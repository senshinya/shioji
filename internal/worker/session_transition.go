package worker

import "context"

// These gates protect a session's transition while slow native work runs.
// They never hold the device-wide dispatch mutex across an await.
func (s *Server) waitSessionTransition(ctx context.Context, id string) error {
	for {
		done := s.sessionTransitions[id]
		if done == nil {
			return nil
		}
		s.dispatchMu.Unlock()
		var err error
		select {
		case <-done:
		case <-ctx.Done():
			err = ctx.Err()
		}
		s.dispatchMu.Lock()
		if err != nil {
			return err
		}
	}
}
func (s *Server) beginSessionTransition(ctx context.Context, id string) (func(), error) {
	if id == "" {
		return func() {}, nil
	}
	if err := s.waitSessionTransition(ctx, id); err != nil {
		return nil, err
	}
	if s.sessionTransitions == nil {
		s.sessionTransitions = map[string]chan struct{}{}
	}
	done := make(chan struct{})
	s.sessionTransitions[id] = done
	return func() {
		if s.sessionTransitions[id] == done {
			delete(s.sessionTransitions, id)
			close(done)
		}
	}, nil
}
