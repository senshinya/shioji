package worker

import (
	"context"
	"database/sql"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"slices"
	"time"
)

func (s *Server) compactContext(in domain.ContextActionRequest) (domain.Operation, error) {
	if in.SessionID == "" {
		return domain.Operation{}, errors.New("缺少会话标识")
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	var none domain.Operation
	if old, err := s.store.ContextOperation(in); err == nil {
		return old, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return none, workNotSent(err)
	}
	if err := s.cleanRuntimeRetirements(in.SessionID); err != nil {
		return none, workNotSent(err)
	}
	release, err := s.beginSessionTransition(s.ctx, in.SessionID)
	if err != nil {
		return none, workNotSent(err)
	}
	defer release()
	transition := s.sessionTransitions[in.SessionID]
	releasePreparation := s.reservePreparation(in.SessionID)
	defer releasePreparation()
	if err := s.admitMemoryPressure(in.SessionID); err != nil {
		return none, workNotSent(err)
	}
	if err := s.updateAdmission(); err != nil {
		return none, workNotSent(err)
	}
	if err := s.sessionMaintenanceAdmission(in.SessionID); err != nil {
		return none, err
	}
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return none, workNotSent(err)
	}
	if d.Session.Revision != in.Revision || !d.Session.Status.CanSubmit() || d.Session.ExecutionGuard != "" || d.Session.ContextChanged || d.Session.Archived || d.Session.ThreadID == "" {
		return none, workNotSent(errors.New("当前会话暂不可压缩上下文"))
	}
	if err = s.store.ValidateContextAction(in); err != nil {
		return none, workNotSent(err)
	}
	paths := append([]string{d.Session.Context.CWD}, d.Session.Context.AdditionalDirectories...)
	if err := s.restoreOwnedWorktrees(paths); err != nil {
		return none, workNotSent(err)
	}
	validated, err := validateFolders(paths)
	if err != nil {
		return none, workNotSent(err)
	}
	if !slices.Equal(paths, validated) {
		return none, workNotSent(errors.New("execution directory identity changed"))
	}
	if _, err = s.prepareGitLocked(domain.Submission{SessionID: in.SessionID, WorkspaceID: d.Session.WorkspaceID}, d.Session.Context); err != nil {
		return none, workNotSent(err)
	}
	if !transition.active() {
		return none, errSessionTransitionRetired
	}
	if err := s.updateAdmission(); err != nil {
		return none, err
	}
	setup, cancelSetup := context.WithTimeout(transition.ctx, 20*time.Second)
	defer cancelSetup()
	r, err := s.sessionRuntime(setup, in.SessionID)
	if !transition.active() {
		return none, errSessionTransitionRetired
	}
	if err != nil {
		return none, workNotSent(err)
	}
	compactor, ok := r.(harness.ContextCompactor)
	if !ok || !r.Readiness().Capabilities.Compact {
		return none, workNotSent(harness.Unsupported("context-compaction"))
	}
	if !r.Observes(d.Session) {
		s.dispatchMu.Unlock()
		err = r.Resume(setup, d.Session)
		s.dispatchMu.Lock()
		if !transition.active() {
			return none, errSessionTransitionRetired
		}
		if err != nil {
			return none, workNotSent(err)
		}
	}
	// The caller revision was checked before setup. Resume can publish a title;
	// admission now rechecks the complete state against that refreshed revision.
	latest, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return none, workNotSent(err)
	}
	if latest.Session.ThreadID != d.Session.ThreadID {
		return none, workNotSent(errors.New("压缩目标已改变"))
	}
	op, err := s.store.AcceptContextAction(in, latest.Session.Revision)
	if err != nil {
		return none, workNotSent(err)
	}
	if err = s.store.SetOperation(op.ID, "dispatched", ""); err != nil {
		return op, err
	}
	ctx, cancel := context.WithTimeout(transition.ctx, 30*time.Second)
	defer cancel()
	s.dispatchMu.Unlock()
	err = compactor.Compact(ctx, d.Session)
	s.dispatchMu.Lock()
	if !transition.active() {
		return op, errSessionTransitionRetired
	}
	if current, e := s.store.Operation(op.ID); e != nil || current.State != "dispatched" {
		return current, e
	}
	outcome := domain.DeliveryOutcome(err != nil, harness.IsUncertain(err))
	message := ""
	if err != nil {
		message = err.Error()
	}
	if err = s.store.SetOperation(op.ID, outcome.Operation, message); err != nil {
		return op, err
	}
	s.wakeQueue()
	s.wakeRecovery()
	return s.store.Operation(op.ID)
}
func (s *Server) queryContextOperation(in domain.ContextActionRequest) (domain.Operation, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	op, err := s.store.ContextOperation(in)
	if err != nil {
		return op, err
	}
	if op.State != "indeterminate" && op.State != "confirmed" {
		return op, nil
	}
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return op, err
	}
	a := d.Session.ContextAction
	if a == nil || a.OperationID != op.ID || a.SourceRef != d.Session.ThreadID {
		return op, errors.New("压缩目标已改变")
	}
	if op.State == "confirmed" && a.State != "unknown" {
		return op, nil
	}
	r := s.currentRuntime(in.SessionID)
	if r == nil || r.Stopped() {
		r = s.catalogRuntime(d.Session.HarnessID)
	}
	compactor, ok := r.(harness.ContextCompactor)
	if !ok {
		return op, nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	s.dispatchMu.Unlock()
	observation, err := compactor.InspectCompaction(ctx, d.Session, *a)
	s.dispatchMu.Lock()
	if err != nil {
		return op, err
	}
	if err = s.store.ResolveContextAction(in.SessionID, *a, observation); err != nil {
		return op, err
	}
	return s.store.Operation(op.ID)
}
