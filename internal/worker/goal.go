package worker

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func (s *Server) goalControl(in domain.GoalRequest) (domain.Operation, error) {
	var zero domain.Operation
	if in.OperationID == "" || len(in.OperationID) > 256 || in.SessionID == "" {
		return zero, errors.New("目标操作需要稳定标识")
	}
	switch in.Action {
	case domain.PauseGoal, domain.ResumeGoal, domain.ClearGoal:
	default:
		return zero, errors.New("无效的目标操作")
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.waitSessionTransition(s.ctx, in.SessionID); err != nil {
		return zero, err
	}
	if prior, err := s.store.GoalOperation(in); err == nil {
		return prior, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	if err := s.updateAdmission(); err != nil {
		return zero, err
	}
	if err := s.cleanRuntimeRetirements(in.SessionID); err != nil {
		return zero, err
	}
	release, err := s.beginSessionTransition(s.ctx, in.SessionID)
	if err != nil {
		return zero, err
	}
	defer release()
	owner := s.sessionTransitions[in.SessionID]
	preparation := s.reservePreparation(in.SessionID)
	defer preparation()
	ctx, cancel := context.WithTimeout(owner.ctx, 20*time.Second)
	defer cancel()

	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return zero, err
	}
	session := d.Session
	w, err := s.store.Workspace(session.WorkspaceID)
	if err != nil {
		return zero, err
	}
	if w.Archived || session.Archived || session.ThreadID == "" || !session.Goal.SameTarget(&in.Target) {
		return zero, errors.New("目标已改变或会话不可操作，请重新核对")
	}
	if in.Action == domain.PauseGoal && !session.Goal.Active() || in.Action == domain.ResumeGoal && !session.Goal.CanResume() {
		return zero, errors.New("当前目标状态不支持此操作")
	}
	if err = s.sessionMaintenanceAdmission(session.ID); err != nil {
		return zero, err
	}
	if in.Action == domain.ResumeGoal {
		if err := s.goalResumeAdmission(d); err != nil {
			return zero, err
		}
	}
	r := s.currentRuntime(session.ID)
	if r == nil || r.Stopped() || in.Action == domain.ResumeGoal && !r.ExecutionReady() {
		r, err = s.sessionRuntime(ctx, session.ID)
		if err != nil {
			return zero, err
		}
	}
	goals, supported := r.(harness.GoalController)
	if !supported || !r.Readiness().Capabilities.Goals {
		return zero, harness.Unsupported("goal")
	}

	// Status-only actions can use the native durable thread while it is cold.
	// Loading an active thread here could otherwise schedule new execution.
	var native *domain.Goal
	_, err = s.runtimeCallOutsideDispatch(ctx, session.ID, r, func() (bool, error) {
		var e error
		native, e = goals.ReadGoal(ctx, session)
		return e == nil, e
	})
	if err != nil {
		return zero, err
	}
	if !owner.active() || s.currentRuntime(session.ID) != r {
		return zero, errRuntimeRetired
	}
	if err = s.store.RecordGoalReceipt(session.ID, session.ThreadID, session.Goal, native); err != nil {
		return zero, err
	}
	if !native.SameTarget(&in.Target) {
		s.wakeQueue()
		return zero, errors.New("目标已在执行环境中改变")
	}
	if in.Action == domain.ResumeGoal {
		if err = validateAvailableConfiguration(r, session.Config); err != nil {
			return zero, err
		}
		if !r.Observes(session) {
			_, err = s.runtimeCallOutsideDispatch(ctx, session.ID, r, func() (bool, error) {
				e := r.Resume(ctx, session)
				return e == nil, e
			})
			if err != nil {
				return zero, err
			}
		}
		d, err = s.store.Session(session.ID, 0, 1)
		if err != nil {
			return zero, err
		}
		if err = s.goalResumeReady(d); err != nil {
			return zero, err
		}
	}
	if !owner.active() || s.currentRuntime(session.ID) != r {
		return zero, errSessionTransitionRetired
	}
	if err = s.updateAdmission(); err != nil {
		return zero, err
	}
	op, err := s.store.AcceptGoal(in, session.ThreadID)
	if err != nil {
		return zero, err
	}
	owner.abandon = func() error {
		return s.store.SetOperation(op.ID, domain.OperationAbandoned, "运行环境已结束，目标操作未获确认")
	}
	defer s.wakeQueue()
	defer s.wakeRecovery()
	if in.Action == domain.ResumeGoal && r.ManagedCoverage() {
		if err = s.coverOperation(session.ID, op.ID); err != nil {
			if writeErr := s.store.SetOperation(op.ID, domain.OperationRejected, err.Error()); writeErr != nil {
				return zero, writeErr
			}
			return s.store.GoalOperation(in)
		}
	}
	var result *domain.Goal
	_, err = s.runtimeCallOutsideDispatch(ctx, session.ID, r, func() (bool, error) {
		var e error
		result, e = goals.ChangeGoal(ctx, session, domain.GoalChange{Action: in.Action})
		return e == nil, e
	})
	if !owner.active() {
		return s.store.GoalOperation(in)
	}
	if err == nil {
		if writeErr := s.store.RecordGoalReceipt(session.ID, session.ThreadID, native, result); writeErr != nil {
			return zero, writeErr
		}
	}
	state := domain.DeliveryOutcome(err != nil, harness.IsUncertain(err)).Operation
	message := ""
	if err != nil {
		message = err.Error()
	}
	if err = s.store.SetOperation(op.ID, state, message); err != nil {
		return zero, err
	}
	return s.store.GoalOperation(in)
}

func (s *Server) goalResumeReady(d domain.SessionDetail) error {
	if d.Session.Archived || d.Session.ContextChanged || d.Session.ExecutionGuard != "" ||
		!d.Session.Status.CanSubmit() || d.Session.Config.Plan || !d.Session.Goal.CanResume() {
		return errors.New("请核对分支与执行状态后恢复目标")
	}
	for _, req := range d.Requests {
		if req.State.AwaitingResolution() {
			return errors.New("请先处理问题和授权")
		}
	}
	busy, err := s.store.HasUnsettledOperation(d.Session.ID)
	if err != nil {
		return err
	}
	work, err := s.store.HasActiveWork(d.Session.ID)
	if err != nil {
		return err
	}
	if busy || work {
		return errors.New("请等待未确认输入和后台任务结束")
	}
	return nil
}

func (s *Server) goalResumeAdmission(d domain.SessionDetail) error {
	if err := s.goalResumeReady(d); err != nil {
		return err
	}
	if err := s.admitMemoryPressure(d.Session.ID); err != nil {
		return err
	}
	paths := append([]string{d.Session.Context.CWD}, d.Session.Context.AdditionalDirectories...)
	if err := s.restoreOwnedWorktrees(paths); err != nil {
		return err
	}
	resolved, err := validateFolders(paths)
	if err != nil {
		return err
	}
	if !slices.Equal(paths, resolved) {
		return errors.New("执行目录已改变")
	}
	_, err = s.prepareGitLocked(domain.Submission{SessionID: d.Session.ID, WorkspaceID: d.Session.WorkspaceID}, d.Session.Context)
	return err
}

func (s *Server) startGoal(ctx context.Context, r harness.Runtime, session domain.Session, objective string, gate *runtimeEventGate) error {
	goals, ok := r.(harness.GoalController)
	if !ok || !r.Readiness().Capabilities.Goals {
		return harness.Unsupported("goal")
	}
	goal, err := goals.ChangeGoal(ctx, session, domain.GoalChange{Action: domain.SetGoal, Objective: objective})
	if err != nil {
		return err
	}
	return gate.apply(func() error {
		return s.store.RecordGoalReceipt(session.ID, session.ThreadID, session.Goal, goal)
	})
}
