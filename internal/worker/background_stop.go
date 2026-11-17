package worker

import (
	"context"
	"errors"
	"os"
	"shioji/internal/domain"
	"time"
)

// System unregister terminates the managed owner. Admission belongs to the
// worker so another client cannot race a new turn through a UI-only idle check.
type backgroundStop struct {
	id   string
	undo []func() error
}

func (s *Server) disableBackground(ids ...string) (result domain.BackgroundService, err error) {
	id := domain.NewID()
	if len(ids) > 0 {
		id = ids[0]
	}
	result, err = s.prepareBackgroundStop(id)
	if err != nil || !s.backgroundStopping.Load() {
		return result, err
	}
	// This callback only asks the independent host to accept the action. Native
	// unregister never runs in, or holds the lifetime of, this RPC handler.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err = s.config.DisableBackground(ctx, id)
	if result.NotStarted || err == nil && result.State != "disabling" && result.State != "notRegistered" && result.State != "notInstalled" {
		err = errors.Join(err, s.cancelBackgroundStop(id))
	}
	return result, err
}

func (s *Server) prepareBackgroundStop(id string) (result domain.BackgroundService, err error) {
	result = domain.BackgroundService{Format: 1, State: "enabled"}
	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.config.DisableBackground == nil {
		return result, errors.New("执行服务不支持关闭后台运行，请先更新")
	}
	if s.backgroundStopping.Load() {
		return result, errors.New("后台服务正在关闭，请查询已提交操作")
	}
	if s.build.Managed && s.hasActiveSignIn() {
		result.Message = "正在登录 Agent，完成后可关闭后台服务"
		return result, nil
	}
	if s.build.Managed {
		var undo []func() error
		defer func() {
			if !s.backgroundStopping.Load() {
				for i := len(undo) - 1; i >= 0; i-- {
					err = errors.Join(err, undo[i]())
				}
			}
		}()
		entries, e := os.ReadDir(s.executions.Directory)
		if e != nil {
			return result, e
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			scope, e := s.executions.Existing(entry.Name())
			if e != nil {
				return result, e
			}
			if scope == nil {
				continue
			}
			restore, e := scope.PauseAdmission()
			if e != nil {
				result.Message = e.Error()
				return result, nil
			}
			if restore != nil {
				undo = append(undo, restore)
			}
		}
		idle, e := s.store.PrepareWorkerStop("后台服务关闭，队列已保留")
		if e != nil {
			return result, e
		}
		if !idle.Ready {
			result.Message = idle.Reason
			return result, nil
		}
		s.backgroundStop = &backgroundStop{id: id, undo: undo}
		s.backgroundStopping.Store(true)
	}
	return result, nil
}

func (s *Server) cancelBackgroundStop(id string) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.backgroundStop == nil || s.backgroundStop.id != id {
		return errors.New("background operation mismatch")
	}
	if s.drainOperation != "" || s.ctx.Err() != nil {
		return errors.New("worker drain already accepted")
	}
	var err error
	for i := len(s.backgroundStop.undo) - 1; i >= 0; i-- {
		err = errors.Join(err, s.backgroundStop.undo[i]())
	}
	if err == nil {
		s.backgroundStopping.Store(false)
		// Retain the exact completed ID so a lost reply can be acknowledged.
		// The next accepted preparation replaces it; no old scope is retained.
		s.backgroundStop.undo = nil
	}
	return err
}
