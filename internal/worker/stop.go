package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"time"
)

// An interrupt ACK is only request acceptance. The captured turn and native
// requests must settle within the same five-second cancellation budget.
func (s *Server) interruptTurn(id, turn string) error {
	s.dispatchMu.Lock()
	r := s.currentRuntime(id)
	if r == nil {
		s.runtimeMu.RLock()
		pending := s.runtimeStarts[id]
		s.runtimeMu.RUnlock()
		if pending != nil {
			err := s.stopInitializing(id, pending)
			s.dispatchMu.Unlock()
			return err
		}
		s.dispatchMu.Unlock()
		return errors.New("runtime unavailable")
	}
	if !r.Readiness().Capabilities.Interrupt {
		s.dispatchMu.Unlock()
		return harness.Unsupported("interrupt")
	}
	d, err := s.store.Session(id, 0, 1)
	active := turn == "" && (d.Session.Status.Active() || d.Session.Goal.Active())
	for _, t := range d.Turns {
		if t.ID == turn && t.Status.Active() {
			active = true
		}
	}
	if c := d.Session.Completion; c != nil && c.Pending && (turn == "" || turn == c.TurnID) {
		active = true
		if turn == "" {
			turn = c.TurnID
		}
	}
	if err != nil || !active {
		s.dispatchMu.Unlock()
		if err != nil {
			return err
		}
		return errors.New("turn is not active")
	}
	err = s.store.WithdrawCompletion(id)
	if err == nil {
		_, err = s.store.PauseQueue(id, "stopped")
	}
	if err == nil {
		scope, e := s.executions.Existing(id)
		err = e
		if scope != nil && err == nil {
			err = scope.RecordCancellation(turn)
		}
	}
	if err != nil {
		s.dispatchMu.Unlock()
		return err
	}
	s.mu.Lock()
	epoch := s.dispatchEpochs[id]
	s.mu.Unlock()
	s.wg.Add(1)
	s.dispatchMu.Unlock()
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		ack := make(chan error, 1)
		go func() {
			if turn == "" && !d.Session.Goal.Active() {
				return // No native turn yet. Keep the existing timeout/recovery path.
			}
			if turn != "" {
				if e := r.Interrupt(ctx, d.Session, turn); e != nil {
					ack <- e
					return
				}
			}
			ack <- s.pauseGoalAfterStop(ctx, r, d.Session)
		}()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		acknowledged := false
		for {
			select {
			case e := <-ack:
				acknowledged = e == nil
			case <-tick.C:
				if !acknowledged {
					continue
				}
				latest, e := s.store.Session(id, 0, 1)
				if e != nil {
					continue
				}
				finished := true
				for _, t := range latest.Turns {
					if t.ID == turn && t.Status.Active() {
						finished = false
					}
				}
				if pending, ok := r.(harness.PendingRuntime); ok && pending.PendingOperations() != 0 {
					finished = false
				}
				if busy, e := s.store.HasActiveWork(id); e != nil || busy {
					finished = false
				}
				if finished {
					return
				}
			case <-ctx.Done():
				if s.ctx.Err() != nil {
					return
				}
				s.dispatchMu.Lock()
				current := s.currentRuntime(id)
				s.mu.Lock()
				unchanged := s.dispatchEpochs[id] == epoch
				s.mu.Unlock()
				if current != nil && current.Done() == r.Done() && unchanged {
					// Revoke the captured maintenance owner, including its logical
					// slot. A late reply cannot retain or replace the next Agent.
					release, err := s.beginSessionRetirement(s.ctx, id, func() (bool, error) {
						current := s.currentRuntime(id)
						s.mu.Lock()
						unchanged := s.dispatchEpochs[id] == epoch
						s.mu.Unlock()
						return current != nil && current.Done() == r.Done() && unchanged, nil
					})
					if err != nil || release == nil {
						if err != nil {
							s.cancel()
						}
						s.dispatchMu.Unlock()
						return
					}
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					failure := s.retireRuntimeOutsideDispatch(cleanup, id, "stop-timeout", true)
					stop()
					if current = s.currentRuntime(id); current != nil && current.Done() == r.Done() {
						s.mu.Lock()
						unchanged = s.dispatchEpochs[id] == epoch
						s.mu.Unlock()
						if err := s.detachRuntimeInstance(id, failure, unchanged); err != nil {
							s.cancel()
						}
					}
					release()
				}
				s.dispatchMu.Unlock()
				return
			}
		}
	}()
	return nil
}

// Only the captured runtime and objective may be paused by this Stop.
func (s *Server) pauseGoalAfterStop(ctx context.Context, runtime harness.Runtime, before domain.Session) error {
	if !before.Goal.Active() {
		return nil
	}
	goals, ok := runtime.(harness.GoalController)
	if !ok || !runtime.Readiness().Capabilities.Goals {
		return harness.Unsupported("goal")
	}
	s.runtimeMu.RLock()
	gate := s.runtimeEvents[before.ID]
	s.runtimeMu.RUnlock()
	if gate == nil || !gate.Load() || s.currentRuntime(before.ID) != runtime {
		return errRuntimeRetired
	}
	native, err := goals.ReadGoal(ctx, before)
	if err != nil {
		return err
	}
	if !native.SameTarget(before.Goal) {
		return nil // The goal settled or changed while Stop was in flight.
	}
	result, err := goals.ChangeGoal(ctx, before, domain.GoalChange{Action: domain.PauseGoal})
	if err != nil {
		return err
	}
	return gate.apply(func() error {
		if err := s.store.RecordGoalReceipt(before.ID, before.ThreadID, native, result); err != nil {
			return err
		}
		s.wakeQueue()
		return nil
	})
}
