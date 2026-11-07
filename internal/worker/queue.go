package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"time"
)

func (s *Server) wakeQueue() {
	select {
	case s.queueWake <- struct{}{}:
	default:
	}
}
func (s *Server) queueLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.queueWake:
		}
		s.dispatchMu.Lock()
		snapshot, err := s.store.Snapshot()
		if err == nil && s.ctx.Err() == nil {
			for _, session := range snapshot.Sessions {
				if !session.Status.CanSubmit() {
					continue
				}
				q, e := s.store.Queue(session.ID)
				if e != nil || q.Paused || len(q.Items) == 0 {
					continue
				}
				d, e := s.store.Session(session.ID, 0, 1)
				if e != nil {
					continue
				}
				blocked := false
				for _, req := range d.Requests {
					if req.State.AwaitingResolution() {
						blocked = true
					}
				}
				unsettled, e := s.store.HasUnsettledOperation(session.ID)
				if e != nil || unsettled || blocked {
					continue
				}
				if _, e = s.submitLocked(q.Items[0].Input, q.Revision, false); e != nil {
					s.store.PauseQueue(session.ID, e.Error())
				}
			}
		}
		s.dispatchMu.Unlock()
	}
}
func (s *Server) enqueue(in domain.Submission) (domain.Operation, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	runtime, err := s.inputRuntime(in)
	if err != nil {
		return domain.Operation{}, err
	}
	if _, err := s.attachmentInputs(in); err != nil {
		return domain.Operation{}, err
	}
	if err := runtime.Validate(in.Config); err != nil {
		return domain.Operation{}, err
	}
	op, err := s.store.Enqueue(in)
	if err == nil {
		s.wakeQueue()
	}
	return op, err
}
func (s *Server) changeQueue(method string, in protocol.QueueInput) (domain.QueueState, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	switch method {
	case "queue.send":
		q, err := s.store.Queue(in.SessionID)
		if err != nil {
			return q, err
		}
		if q.Revision != in.Revision {
			return q, errors.New("queue changed")
		}
		for _, item := range q.Items {
			if item.ID == in.ID {
				if in.TurnID == "" {
					if _, err = s.submitLocked(item.Input, q.Revision, true); err != nil {
						return q, err
					}
					return s.store.Queue(in.SessionID)
				}
				if _, err = s.steerLocked(protocol.SteerInput{Input: item.Input, TurnID: in.TurnID}, q.Revision); err != nil {
					return q, err
				}
				return s.store.Queue(in.SessionID)
			}
		}
		return q, errors.New("queued message no longer exists")
	case "queue.reorder":
		return s.store.ReorderQueue(in.SessionID, in.ID, in.BeforeID, in.Revision)
	case "queue.edit":
		d, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return domain.QueueState{}, err
		}
		if _, err = s.attachmentInputs(domain.Submission{WorkspaceID: d.Session.WorkspaceID, Text: in.Text, Attachments: in.Attachments}); err != nil {
			return domain.QueueState{}, err
		}
		return s.store.EditQueue(in.SessionID, in.ID, in.Revision, in.Text, in.Attachments)
	case "queue.remove":
		return s.store.RemoveQueue(in.SessionID, in.ID, in.Revision)
	case "queue.continue":
		d, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return domain.QueueState{}, err
		}
		if s.catalogRuntime(d.Session.HarnessID) == nil || s.ctx.Err() != nil || d.Session.ContextChanged || d.Session.Status == "unknown" {
			return domain.QueueState{}, errors.New("reconcile execution context before continuing")
		}
		q, err := s.store.ContinueQueue(in.SessionID, in.Revision)
		if err == nil {
			s.wakeQueue()
		}
		return q, err
	}
	return domain.QueueState{}, errors.New("unknown queue command")
}
func (s *Server) steer(in protocol.SteerInput) (domain.Operation, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	return s.steerLocked(in, 0)
}
func (s *Server) steerLocked(in protocol.SteerInput, revision int64) (domain.Operation, error) {
	if s.currentRuntime(in.Input.SessionID) == nil || s.ctx.Err() != nil {
		return domain.Operation{}, errors.New("runtime unavailable")
	}
	if _, err := s.inputHarness(in.Input); err != nil {
		return domain.Operation{}, err
	}
	if !s.currentRuntime(in.Input.SessionID).Readiness().Capabilities.Steer {
		return domain.Operation{}, harness.Unsupported("steer")
	}
	if _, err := s.store.Operation(in.Input.OperationID); err == nil {
		return s.store.AcceptSteer(in.Input, in.TurnID)
	}
	if _, err := s.attachmentInputs(in.Input); err != nil {
		return domain.Operation{}, err
	}
	var op domain.Operation
	var err error
	if revision > 0 {
		op, err = s.store.AcceptQueuedSteer(in.Input, in.TurnID, revision)
	} else {
		op, err = s.store.AcceptSteer(in.Input, in.TurnID)
	}
	if err != nil {
		return op, err
	}
	d, err := s.store.Session(op.SessionID, 0, 1)
	if err != nil {
		return op, err
	}
	runtime := s.currentRuntime(in.Input.SessionID)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.wakeQueue()
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		if !s.dispatchReserved(op, runtime) {
			return
		}
		if s.store.SetOperation(op.ID, "dispatched", "") != nil {
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		defer cancel()
		inputs, err := s.attachmentInputs(in.Input)
		if err == nil && runtime.ManagedCoverage() {
			err = s.coverOperation(op.SessionID, op.ID)
		}
		if err == nil {
			err = runtime.Steer(ctx, d.Session, in.TurnID, op.ID, in.Input.Text, inputs...)
		}
		outcome := domain.DeliveryOutcome(err != nil, harness.IsUncertain(err))
		state, status, message := outcome.Operation, outcome.Input, ""
		if err != nil {
			message = err.Error()
			if state == domain.OperationIndeterminate {
				s.store.PauseQueue(op.SessionID, "steer delivery is unknown")
			}
		}
		if s.store.SetOperation(op.ID, state, message) != nil {
			return
		}
		if state == "indeterminate" {
			defer s.wakeRecovery()
		}

		if state != "confirmed" && revision > 0 {
			return
		}
		s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + op.ID, TurnID: in.TurnID, Kind: "user", Attachments: in.Input.Attachments, Text: in.Input.Text, Final: true, Status: status}})
	}()
	return op, nil
}
