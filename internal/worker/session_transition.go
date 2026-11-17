package worker

import (
	"context"
	"errors"
)

var errSessionTransitionRetired = errors.New("会话运行环境已退休，原维护操作未继续")

type sessionTransition struct {
	done     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	retiring bool
	abandon  func() error // Settle this accepted maintenance receipt, without native I/O.
}

func (t *sessionTransition) active() bool {
	select {
	case <-t.done:
		return false
	default:
		return true
	}
}

// These gates protect a session's transition while slow native work runs.
// They never hold the device-wide dispatch mutex across an await.
func (s *Server) waitSessionTransition(ctx context.Context, id string) error {
	for {
		pending := s.sessionTransitions[id]
		if pending == nil {
			return nil
		}
		s.dispatchMu.Unlock()
		var err error
		select {
		case <-pending.done:
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
		return nil, errors.New("缺少会话标识")
	}
	if err := s.waitSessionTransition(ctx, id); err != nil {
		return nil, err
	}
	if s.sessionTransitions == nil {
		s.sessionTransitions = map[string]*sessionTransition{}
	}
	work, cancel := context.WithCancel(ctx)
	transition := &sessionTransition{done: make(chan struct{}), ctx: work, cancel: cancel}
	s.sessionTransitions[id] = transition
	return func() {
		if s.sessionTransitions[id] == transition {
			delete(s.sessionTransitions, id)
			transition.cancel()
			close(transition.done)
		}
	}, nil
}

// Forced retirement revokes native maintenance rather than waiting for its
// reply. Other retirements retain serialization of the physical cleanup.
// Caller holds dispatchMu; abandoning a receipt is local persistence only.
func (s *Server) beginSessionRetirement(ctx context.Context, id string, valid func() (bool, error)) (func(), error) {
	if id == "" {
		return nil, errors.New("缺少会话标识")
	}
	for {
		// Another collector may finish while this one waits. Recheck its
		// durable target before revoking maintenance owned by a successor.
		current, err := valid()
		if err != nil || !current {
			return nil, err
		}
		pending := s.sessionTransitions[id]
		if pending == nil {
			break
		}
		if !pending.retiring {
			if pending.abandon != nil {
				deleted, err := s.store.SessionDeleted(id)
				if err != nil {
					return nil, err
				}
				if !deleted {
					if err = pending.abandon(); err != nil {
						return nil, err
					}
				}
			}
			delete(s.sessionTransitions, id)
			pending.cancel()
			close(pending.done)
			break
		}
		s.dispatchMu.Unlock()
		select {
		case <-pending.done:
		case <-ctx.Done():
			err = ctx.Err()
		}
		s.dispatchMu.Lock()
		if err != nil {
			return nil, err
		}
	}
	release, err := s.beginSessionTransition(ctx, id)
	if err == nil {
		s.sessionTransitions[id].retiring = true
	}
	return release, err
}
