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
func (s *Server) disableBackground() (result domain.BackgroundService, err error) {
	result = domain.BackgroundService{Format: 1, State: "enabled"}
	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.config.DisableBackground == nil {
		return result, errors.New("执行服务不支持关闭后台运行，请先更新")
	}
	if s.backgroundStopping.Load() {
		return result, errors.New("后台服务正在关闭")
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
			restore, e := scope.FenceIfIdle()
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
		s.backgroundStopping.Store(true)
	}
	// The helper must finish even when unregister sends SIGTERM to this worker.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err = s.config.DisableBackground(ctx)
	if err == nil && result.State != "notRegistered" && result.State != "notInstalled" {
		s.backgroundStopping.Store(false)
	}
	return result, err
}
