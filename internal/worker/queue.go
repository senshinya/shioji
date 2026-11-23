package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"sync"
	"time"
)

type steerRuntimeKey struct {
	session string
	runtime <-chan struct{}
}

func (s *Server) wakeQueue() {
	select {
	case s.queueWake <- struct{}{}:
	default:
	}
}
func (s *Server) queueLoop() {
	defer s.wg.Done()
	active := make(map[string]bool)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.queueWake:
		}
		s.dispatchManagedGoals()
		ids, err := s.store.ReadyQueueSessions()
		if err != nil {
			continue
		}
		s.dispatchMu.Lock()
		for _, id := range ids {
			if len(active) >= 4 || s.ctx.Err() != nil {
				break
			}
			if active[id] || s.sessionTransitions[id] != nil {
				continue
			}
			active[id] = true
			s.wg.Add(1)
			go func(id string) {
				defer s.wg.Done()
				s.dispatchMu.Lock()
				defer s.dispatchMu.Unlock()
				defer func() { delete(active, id) }()
				q, err := s.store.Queue(id)
				if err != nil || q.Paused || len(q.Items) == 0 || s.updateAdmission() != nil {
					return
				}
				_, err = s.submitLocked(q.Items[0].Input, q.Revision, false)
				if err != nil && !errors.Is(err, errCatalogRefreshing) && s.updateAdmission() == nil {
					if latest, e := s.store.Queue(id); e == nil && latest.Revision == q.Revision {
						_, _ = s.store.PauseQueue(id, err.Error())
					}
				}
				// A successful claim or a paused queue frees capacity for later
				// sessions. Catalog refresh already wakes the queue on completion.
				if !errors.Is(err, errCatalogRefreshing) {
					s.wakeQueue()
				}
			}(id)
		}
		s.dispatchMu.Unlock()
	}
}
func (s *Server) enqueue(in domain.Submission) (domain.Operation, error) {
	defer s.beginInputReceipt(in.OperationID)()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	return s.enqueueLocked(in)
}
func (s *Server) enqueueLocked(in domain.Submission) (domain.Operation, error) {
	if err := s.sessionMaintenanceAdmission(in.SessionID); err != nil {
		return domain.Operation{}, err
	}
	id, err := s.inputHarness(in)
	if err != nil {
		return domain.Operation{}, err
	}
	runtime, release, err := s.borrowCatalog(id)
	if err != nil {
		return domain.Operation{}, err
	}
	defer release()
	if _, err := s.attachmentInputsOutsideDispatch(in); err != nil {
		return domain.Operation{}, err
	}
	if err := validateAvailableConfiguration(runtime, in.Config); err != nil {
		return domain.Operation{}, err
	}
	if in.GoalObjective != "" && !runtime.Readiness().Capabilities.Goals {
		return domain.Operation{}, harness.Unsupported("goal")
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
		validationSkills := in.Skills
		if validationSkills == nil {
			q, e := s.store.Queue(in.SessionID)
			if e != nil {
				return domain.QueueState{}, e
			}
			for _, item := range q.Items {
				if item.ID == in.ID {
					validationSkills = item.Input.Skills
					break
				}
			}
		}
		if _, err = s.attachmentInputsOutsideDispatch(domain.Submission{WorkspaceID: d.Session.WorkspaceID, SessionID: in.SessionID, Text: in.Text, Attachments: in.Attachments, Skills: validationSkills, ReferencePositions: in.ReferencePositions}); err != nil {
			return domain.QueueState{}, err
		}
		return s.store.EditQueueContent(in.SessionID, in.ID, in.Revision, in.Text, in.Attachments, in.Skills, in.ReferencePositions)
	case "queue.remove":
		return s.store.RemoveQueue(in.SessionID, in.ID, in.Revision)
	case "queue.continue":
		if err := s.sessionMaintenanceAdmission(in.SessionID); err != nil {
			return domain.QueueState{}, err
		}
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
	defer s.beginInputReceipt(in.Input.OperationID)()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	return s.steerLocked(in, 0)
}
func (s *Server) steerLocked(in protocol.SteerInput, revision int64) (domain.Operation, error) {
	if err := store.CheckSubmissionJournal(in.Input); err != nil {
		return domain.Operation{}, err
	}
	if in.Input.GoalObjective != "" {
		return domain.Operation{}, errors.New("目标输入请加入队列")
	}
	if err := s.sessionMaintenanceAdmission(in.Input.SessionID); err != nil {
		return domain.Operation{}, err
	}
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
	before, err := s.store.Session(in.Input.SessionID, 0, 1)
	if err != nil {
		return domain.Operation{}, err
	}
	target := s.currentRuntime(in.Input.SessionID)
	if a := before.Session.ContextAction; a != nil && a.SourceRef == before.Session.ThreadID && a.State.Active() {
		return domain.Operation{}, errors.New("上下文压缩期间请将消息加入待发送队列")
	}
	if err := s.updateAdmission(); err != nil {
		return domain.Operation{}, err
	}
	releasePreparation := s.reservePreparation(in.Input.SessionID)
	defer releasePreparation()
	if _, err := s.attachmentInputsOutsideDispatch(in.Input); err != nil {
		return domain.Operation{}, err
	}
	if err := s.updateAdmission(); err != nil {
		return domain.Operation{}, err
	}
	if !s.capturedRuntimeCurrent(in.Input.SessionID, target, before.Session.ThreadID) {
		return domain.Operation{}, workNotSent(errRuntimeRetired)
	}
	var op domain.Operation
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
	runtime := target
	key := steerRuntimeKey{session: op.SessionID, runtime: runtime.Done()}
	if s.steerDispatch == nil {
		s.steerDispatch = make(map[steerRuntimeKey]chan struct{})
	}
	previous := s.steerDispatch[key]
	done := make(chan struct{})
	s.steerDispatch[key] = done
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.wakeQueue()
		releaseOrder := sync.OnceFunc(func() {
			s.dispatchMu.Lock()
			defer s.dispatchMu.Unlock()
			if s.steerDispatch[key] == done {
				delete(s.steerDispatch, key)
			}
			close(done)
		})
		defer releaseOrder()
		if previous != nil {
			select {
			case <-previous:
			case <-runtime.Done():
			case <-s.ctx.Done():
				return
			}
		}
		s.dispatchMu.Lock()
		locked := true
		defer func() {
			if locked {
				s.dispatchMu.Unlock()
			}
		}()
		if !s.dispatchReserved(op, runtime) {
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		defer cancel()
		inputs, err := s.attachmentInputsOutsideDispatch(in.Input)
		current, e := s.store.Operation(op.ID)
		if e != nil || current.State != "accepted" || !s.capturedRuntimeCurrent(op.SessionID, runtime, d.Session.ThreadID) {
			return
		}
		if err == nil {
			err = s.updateAdmission()
		}
		delayed, nativeHandoff := runtime.(harness.InputHandoffRuntime)
		handed := false
		if err == nil && !nativeHandoff {
			if runtime.ManagedCoverage() {
				err = s.coverOperation(op.SessionID, op.ID)
			}
			if err == nil {
				err = s.store.BeginInputHandoff(op.ID)
				handed = err == nil
			}
		}
		if err == nil {
			s.runtimeMu.RLock()
			gate := s.runtimeEvents[op.SessionID]
			s.runtimeMu.RUnlock()
			s.dispatchMu.Unlock()
			locked = false
			input := harness.TurnInput{Text: in.Input.Text, Attachments: inputs, Skills: in.Input.Skills, QuestionReply: in.Input.QuestionReply}
			claim := func() error {
				err := s.claimInput(gate, runtime, op)
				handed = err == nil
				return err
			}
			if deferred, ok := runtime.(harness.DeferredInputRuntime); ok {
				var receipt <-chan error
				receipt, err = deferred.SteerDeferred(ctx, d.Session, in.TurnID, op.ID, input, claim)
				releaseOrder()
				if err == nil {
					err = awaitNativeInput(s.ctx, runtime, receipt)
					if s.ctx.Err() != nil {
						return
					}
				}
			} else if nativeHandoff {
				err = delayed.SteerWithHandoff(ctx, d.Session, in.TurnID, op.ID, input, claim)
			} else {
				err = runtime.Steer(ctx, d.Session, in.TurnID, op.ID, input)
			}
			s.dispatchMu.Lock()
			locked = true
			if current := s.currentRuntime(op.SessionID); current == nil || current.Done() != runtime.Done() {
				return
			}
			if handed && err != nil && runtime.Stopped() && !harness.IsRejected(err) {
				err = &harness.DeliveryError{Uncertain: true, Message: "运行环境已结束，Steer 结果待核对；不会自动重发"}
			}
		}
		if errors.Is(err, errRuntimeRetired) || errors.Is(err, store.ErrInputHandoffRevoked) {
			return
		}
		outcome := domain.DeliveryOutcome(err != nil, handed && harness.IsUncertain(err))
		message := ""
		if err != nil {
			message = err.Error()
		}
		if err := s.store.CompleteInput(op.ID, outcome, message, in.TurnID); err != nil {
			if !errors.Is(err, store.ErrInputHandoffRevoked) {
				s.cancel()
			}
			return
		}
		if outcome.Operation == domain.OperationIndeterminate {
			s.wakeRecovery()
		}
	}()
	return op, nil
}
