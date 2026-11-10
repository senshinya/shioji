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
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	var none domain.Operation
	if old, err := s.store.ContextOperation(in); err == nil {
		return old, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return none, workNotSent(err)
	}
	if err := s.updateAdmission(); err != nil {
		return none, workNotSent(err)
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
	validated, err := validateFolders(paths)
	if err != nil {
		return none, workNotSent(err)
	}
	if !slices.Equal(paths, validated) {
		return none, workNotSent(errors.New("execution directory identity changed"))
	}
	if _, err = s.prepareGit(domain.Submission{SessionID: in.SessionID, WorkspaceID: d.Session.WorkspaceID}, d.Session.Context); err != nil {
		return none, workNotSent(err)
	}
	r, err := s.sessionRuntime(s.ctx, in.SessionID)
	if err != nil {
		return none, workNotSent(err)
	}
	compactor, ok := r.(harness.ContextCompactor)
	if !ok || !r.Readiness().Capabilities.Compact {
		return none, workNotSent(harness.Unsupported("context-compaction"))
	}
	if !r.Observes(d.Session) {
		if err = r.Resume(s.ctx, d.Session); err != nil {
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
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	err = compactor.Compact(ctx, d.Session)
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
	observation, err := compactor.InspectCompaction(ctx, d.Session, *a)
	if err != nil {
		return op, err
	}
	if err = s.store.ResolveContextAction(in.SessionID, *a, observation); err != nil {
		return op, err
	}
	return s.store.Operation(op.ID)
}
