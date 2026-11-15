package worker

import (
	"context"
	"database/sql"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/store"
	"strings"
	"time"
)

func (s *Server) changeHistory(in domain.HistoryChange) (domain.HistoryOperation, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	// Completed receipts do not depend on whether a catalog is refreshing.
	if saved, e := s.store.HistoryOperation(in.OperationID); e == nil && saved.State == "complete" && saved.Input.Equal(in) {
		return saved, nil
	}
	if err := s.sessionMaintenanceAdmission(in.SessionID); err != nil {
		return domain.HistoryOperation{}, err
	}
	runtime, release, err := s.borrowInputRuntime(domain.Submission{SessionID: in.SessionID})
	if err != nil {
		return domain.HistoryOperation{}, err
	}
	defer release()
	var op domain.HistoryOperation
	existing, err := s.store.HistoryOperation(in.OperationID)
	if err == nil {
		if !existing.Input.Equal(in) {
			return op, errors.New("操作标识已被其他输入使用")
		}
		if existing.State == "complete" {
			return existing, nil
		}
		if existing.State != "forked" {
			return existing, store.HistoryPending(existing)
		}
		op = existing
	} else {
		if !errors.Is(err, sql.ErrNoRows) {
			return op, err
		}
		e := s.historyPreflight(in)
		if e != nil {
			return op, e
		}
		op, e = s.store.PrepareHistory(in)
		if e != nil {
			return op, e
		}
		if in.Restart {
			if _, e = s.attachmentInputs(op.Submission); e != nil {
				s.store.FailHistory(op.ID, e)
				return op, e
			}
		}
		ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
		thread, e := runtime.PrepareHistory(ctx, op.Source, harness.HistoryBoundary{TurnID: op.TurnID, Restart: in.Restart})
		cancel()
		if thread != "" {
			if saveErr := s.store.SetHistoryThread(op.ID, thread); saveErr != nil {
				return op, saveErr
			}
			op.ThreadID, op.State = thread, "forked"
		}
		if e != nil {
			if thread == "" {
				s.store.FailHistory(op.ID, e)
			}
			return op, e
		}

	}
	if err = s.historyPreflight(in); err != nil {
		return op, err
	}
	if in.Restart {
		if _, err = s.attachmentInputs(op.Submission); err != nil {
			return op, err
		}
	}
	releaseCtx, cancelRelease := context.WithTimeout(s.ctx, 5*time.Second)
	_, releaseErr := runtime.PrepareHistory(releaseCtx, op.Source, harness.HistoryBoundary{TurnID: op.TurnID, Restart: in.Restart, PreparedRef: op.ThreadID})
	cancelRelease()
	if releaseErr != nil {
		return op, releaseErr
	}
	if !in.Restart {
		return s.store.CommitHistory(op.ID)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	runtime, err = s.sessionRuntime(ctx, op.Source.ID)
	cancel()
	if err != nil {
		return op, err
	}
	if !runtime.Readiness().Capabilities.Restart {
		return op, harness.Unsupported("restart")
	}
	if err = runtime.Validate(op.Submission.Config); err != nil {
		return op, err
	}
	activationCtx, cancelActivation := context.WithTimeout(s.ctx, 20*time.Second)
	err = runtime.ActivateHistory(activationCtx, op.Source, op.ThreadID, func() error { var e error; op, e = s.store.CommitHistory(op.ID); return e })
	cancelActivation()
	if err != nil {
		return op, err
	}
	select {
	case s.titleWake <- struct{}{}:
	default:
	}
	s.mu.Lock()
	delete(s.bound, op.Session.ID)
	s.mu.Unlock()
	accepted, err := s.store.Operation(op.ID)
	if err != nil {
		return op, err
	}
	// Commit has already accepted exactly one ordinary input. Existing dispatch
	// owns all uncertainty handling; retrying this history operation never replays.
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.dispatch(op.Submission, accepted, runtime) }()
	return op, nil
}

func (s *Server) historyPreflight(in domain.HistoryChange) error {
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return err
	}
	if d.Session.Revision != in.Revision {
		return errors.New("会话已改变，请重新载入后再试")
	}
	runtime, runtimeErr := s.inputRuntime(domain.Submission{SessionID: in.SessionID})
	if runtimeErr != nil || runtime == nil || s.ctx.Err() != nil {
		return errors.New("执行服务未就绪")
	}
	caps := runtime.Readiness().Capabilities
	if in.Restart && !caps.Restart {
		return harness.Unsupported("restart")
	}
	if !in.Restart && !caps.Fork {
		return harness.Unsupported("fork")
	}
	paths := append([]string{d.Session.Context.CWD}, d.Session.Context.AdditionalDirectories...)
	valid, err := validateFolders(paths)
	if err != nil {
		return err
	}
	if strings.Join(paths, "\x00") != strings.Join(valid, "\x00") {
		return errors.New("执行目录身份已改变")
	}
	if _, err = s.prepareGit(domain.Submission{SessionID: d.Session.ID, WorkspaceID: d.Session.WorkspaceID, Config: d.Session.Config}, d.Session.Context); err != nil {
		return err
	}
	if in.Restart {
		target, release, e := s.borrowCatalog(d.Session.HarnessID)
		if e != nil {
			return e
		}
		defer release()
		if !target.Readiness().Capabilities.Restart {
			return harness.Unsupported("restart")
		}
		return validateAvailableConfiguration(target, in.Config)
	}
	return nil
}
