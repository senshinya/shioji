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
	// The worker owns accepted maintenance even if the client disconnects.
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if previous := s.currentRuntime(in.SessionID); previous != nil && !previous.Stopped() {
		if unloader, ok := previous.(harness.SessionUnloader); ok {
			err = unloader.Unload(ctx, d.Session)
		}
	}
	if err == nil {
		err = s.cleanSessionRuntime(ctx, in.SessionID)
	}
	if err == nil {
		var runtime harness.Runtime
		runtime, err = s.sessionRuntime(ctx, in.SessionID)
		if err == nil {
			err = runtime.Resume(ctx, d.Session)
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
	if err != nil {
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
