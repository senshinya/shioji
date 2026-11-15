package worker

import (
	"context"
	"database/sql"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"slices"
	"time"
)

func workNotSent(err error) error {
	return &protocol.RemoteError{Code: "notSent", Message: err.Error()}
}
func (s *Server) sendWork(in domain.WorkInput) (domain.WorkInputOperation, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	var none domain.WorkInputOperation
	if err := s.updateAdmission(); err != nil {
		return none, err
	}
	previous, err := s.store.WorkInput(in.OperationID)
	if err == nil {
		if previous.Input != in {
			return none, workNotSent(errors.New("消息操作与原请求不一致"))
		}
		return previous, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return none, workNotSent(err)
	}
	if err = s.sessionMaintenanceAdmission(in.SessionID); err != nil {
		return none, err
	}
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return none, workNotSent(err)
	}
	if in.Mode == "continue" {
		paths := append([]string{d.Session.Context.CWD}, d.Session.Context.AdditionalDirectories...)
		validated, err := validateFolders(paths)
		if err != nil {
			return none, workNotSent(err)
		}
		if !slices.Equal(validated, paths) {
			return none, workNotSent(errors.New("execution directory identity changed"))
		}
		if _, err = s.prepareGit(domain.Submission{SessionID: in.SessionID, WorkspaceID: d.Session.WorkspaceID}, d.Session.Context); err != nil {
			return none, workNotSent(err)
		}
		d, err = s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return none, workNotSent(err)
		}
	}
	work, err := s.store.Work(in.SessionID, in.WorkID)
	if err != nil {
		return none, workNotSent(err)
	}
	w := work.Work
	if w.Kind != "agent" || w.OriginRef == "" || w.OriginRef != d.Session.ThreadID || d.Session.Archived || d.Session.ContextChanged || d.Session.ExecutionGuard != "" {
		return none, workNotSent(errors.New("子 Agent 的执行上下文已改变"))
	}
	if in.Mode == "steer" {
		if !w.Active() || !w.CanSteer {
			return none, workNotSent(harness.Unsupported("independent-work-steer"))
		}
	} else if in.Mode != "continue" || !w.CanContinue || (w.State != "completed" && w.State != "stopped") {
		return none, workNotSent(harness.Unsupported("independent-work-continue"))
	}
	r := s.currentRuntime(in.SessionID)
	if r == nil || r.Stopped() || !r.Observes(d.Session) {
		return none, workNotSent(errors.New("没有可确认的子 Agent 运行实例"))
	}
	messenger, ok := r.(harness.WorkMessenger)
	if !ok {
		return none, workNotSent(harness.Unsupported("independent-work-input"))
	}
	op, fresh, err := s.store.AcceptWorkInput(in)
	if err != nil {
		return none, workNotSent(err)
	}
	if !fresh {
		return op, nil
	}
	if err = s.store.SetWorkInput(in.OperationID, "dispatched", ""); err != nil {
		return op, err
	}
	// Reserve execution ownership before starting a child that is currently idle.
	// This also protects shared-checkout changes before its first source event.
	var reservation int64
	if in.Mode == "continue" {
		reservation = op.Order
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	err = messenger.SendWork(ctx, d.Session, in)
	outcome := domain.DeliveryOutcome(err != nil, harness.IsUncertain(err))
	message := ""
	if err != nil {
		message = err.Error()
	}
	if saveErr := s.store.SetWorkInput(in.OperationID, outcome.Operation, message); saveErr != nil {
		return op, saveErr
	}
	if outcome.Operation == "rejected" && reservation != 0 {
		if latest, e := s.store.Work(in.SessionID, in.WorkID); e == nil && latest.Work.Revision == reservation {
			if _, e = s.store.Apply(in.SessionID, domain.Event{Type: "work.upsert", Work: &w}); e != nil {
				return op, e
			}
		}
	}
	return s.store.WorkInput(in.OperationID)
}
func (s *Server) queryWorkInput(in domain.WorkInput) (domain.WorkInputOperation, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	op, err := s.store.WorkInput(in.OperationID)
	if err != nil {
		return op, err
	}
	if op.Input != in {
		return domain.WorkInputOperation{}, errors.New("消息回执与请求不一致")
	}
	if op.State != "indeterminate" {
		return op, nil
	}
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return op, err
	}
	r := s.currentRuntime(in.SessionID)
	if r == nil || r.Stopped() || !r.Observes(d.Session) {
		return op, nil
	}
	messenger, ok := r.(harness.WorkMessenger)
	if !ok {
		return op, nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	found, err := messenger.WorkInputReceived(ctx, d.Session, in)
	if err != nil {
		return op, err
	}
	if found {
		if err = s.store.SetWorkInput(in.OperationID, "confirmed", ""); err != nil {
			return op, err
		}
	}
	return s.store.WorkInput(in.OperationID)
}
