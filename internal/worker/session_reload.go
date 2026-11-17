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

func (s *Server) reloadSession(in domain.SessionReloadRequest, query bool) (domain.SessionReloadResult, error) {
	if in.SessionID == "" {
		return domain.SessionReloadResult{}, errors.New("缺少会话标识")
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	var result domain.SessionReloadResult
	op, err := s.store.SessionReload(in)
	if err == nil || query {
		if err != nil {
			return result, err
		}
		d, err := s.store.Session(in.SessionID, 0, 1)
		return domain.SessionReloadResult{Operation: op, Session: d.Session}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, workNotSent(err)
	}
	if err := s.cleanRuntimeRetirements(in.SessionID); err != nil {
		return result, workNotSent(err)
	}
	release, err := s.beginSessionTransition(s.ctx, in.SessionID)
	if err != nil {
		return result, workNotSent(err)
	}
	defer release()
	transition := s.sessionTransitions[in.SessionID]
	if err := s.admitMemoryPressure(in.SessionID); err != nil {
		return result, workNotSent(err)
	}
	if err := s.updateAdmission(); err != nil {
		return result, workNotSent(err)
	}
	if err := s.sessionMaintenanceAdmission(in.SessionID); err != nil {
		return result, workNotSent(err)
	}
	d, err := s.store.Session(in.SessionID, 0, 1)
	if err != nil {
		return result, workNotSent(err)
	}
	catalog := s.catalogRuntime(d.Session.HarnessID)
	if catalog == nil || !catalog.Readiness().Capabilities.Reload {
		return result, workNotSent(harness.Unsupported("session-reload"))
	}
	if previous := s.currentRuntime(in.SessionID); previous != nil && !previous.Stopped() && !previous.Readiness().Capabilities.Reload {
		return result, workNotSent(harness.Unsupported("session-reload"))
	}
	if err := catalog.Validate(d.Session.Config); err != nil {
		return result, workNotSent(err)
	}
	paths := append([]string{d.Session.Context.CWD}, d.Session.Context.AdditionalDirectories...)
	if err := s.restoreOwnedWorktrees(paths); err != nil {
		return result, workNotSent(err)
	}
	validated, err := validateFolders(paths)
	if err != nil {
		return result, workNotSent(err)
	}
	if !slices.Equal(paths, validated) {
		return result, workNotSent(errors.New("会话执行目录的实际位置已改变"))
	}
	op, err = s.store.AcceptSessionReload(in)
	if err != nil {
		return result, workNotSent(err)
	}
	transition.abandon = func() error {
		_, err := s.store.FinishSessionReload(in, errSessionTransitionRetired)
		return err
	}
	// The worker owns accepted maintenance even if the client disconnects.
	ctx, cancel := context.WithTimeout(transition.ctx, 30*time.Second)
	defer cancel()
	err = func() error {
		if previous := s.currentRuntime(in.SessionID); previous != nil && !previous.Stopped() {
			if unloader, ok := previous.(harness.SessionUnloader); ok {
				s.dispatchMu.Unlock()
				err = unloader.Unload(ctx, d.Session)
				s.dispatchMu.Lock()
			}
		}
		if !transition.active() {
			return errSessionTransitionRetired
		}
		// A failed unsubscribe cannot keep the captured instance reusable. Physical
		// cleanup owns the result; a survivor is detached and retried independently.
		err = s.cleanSessionRuntime(ctx, in.SessionID)
		if !transition.active() {
			return errSessionTransitionRetired
		}
		if err != nil {
			err = s.detachRuntimeInstance(in.SessionID, err, false)
		}
		if err == nil {
			var runtime harness.Runtime
			runtime, err = s.sessionRuntime(ctx, in.SessionID)
			if !transition.active() {
				return errSessionTransitionRetired
			}
			if err == nil {
				s.dispatchMu.Unlock()
				err = runtime.Resume(ctx, d.Session)
				s.dispatchMu.Lock()
				if !transition.active() {
					return errSessionTransitionRetired
				}
				if err == nil {
					if restorer, ok := runtime.(harness.WorkRestorer); ok {
						restorer.RestoreWork(d.Session, d.Works)
					}
					s.mu.Lock()
					s.bound[in.SessionID] = true
					s.mu.Unlock()
				}
			}
		}
		return err
	}()
	if err != nil && transition.active() {
		// Keep all durable input and history. Unproven teardown is handled by
		// the existing generation guard instead of starting another process.
		if scope, e := s.executions.Existing(in.SessionID); e == nil && scope != nil && scope.VerifyStopped() != nil {
			if e = s.store.GuardExecution(in.SessionID, scope.Generation); e != nil {
				s.cancel()
			}
			s.wakeRecovery()
		}
	}
	op, finishErr := s.store.FinishSessionReload(in, err)
	if finishErr != nil {
		return result, finishErr
	}
	d, err = s.store.Session(in.SessionID, 0, 1)
	return domain.SessionReloadResult{Operation: op, Session: d.Session}, err
}
