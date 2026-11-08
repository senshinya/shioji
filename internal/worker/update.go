package worker

import (
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"time"
)

func (s *Server) prepareWorkerUpdate(in domain.WorkerReplacement) (result domain.WorkerUpdateResult, err error) {
	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.updating.Load() {
		return domain.WorkerUpdateResult{Ready: true}, nil
	}
	if s.config.ReplaceWorker == nil {
		return domain.WorkerUpdateResult{Reason: "此服务尚不支持安全更新"}, nil
	}
	if in.ExpectedBuildID == "" || in.ExpectedBuildID != s.build.BuildID || in.BuildID == s.build.BuildID {
		return result, errors.New("执行服务版本已变化，请重新检查")
	}
	s.runtimeMu.RLock()
	refreshing := false
	for _, state := range s.catalogStates {
		if state.phase == domain.RuntimeRefreshing {
			refreshing = true
			break
		}
	}
	s.runtimeMu.RUnlock()
	if refreshing {
		return domain.WorkerUpdateResult{Reason: "正在更新运行环境，完成后更新执行服务"}, nil
	}
	var undo []func() error
	defer func() {
		if !s.updating.Load() {
			for i := len(undo) - 1; i >= 0; i-- {
				err = errors.Join(err, undo[i]())
			}
		}
	}()
	entries, err := os.ReadDir(s.executions.Directory)
	if err != nil {
		return result, err
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
			return domain.WorkerUpdateResult{Reason: e.Error()}, nil
		}
		if restore != nil {
			undo = append(undo, restore)
		}
	}
	backup := filepath.Join(s.config.Directory, "update-backup-"+time.Now().UTC().Format("20060102T150405.000000000")+".sqlite")
	result, err = s.store.PrepareWorkerUpdate(backup)
	if err != nil || !result.Ready {
		return result, err
	}
	if err = s.config.ReplaceWorker(in); err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	s.updating.Store(true)
	return result, nil
}

func (s *Server) updateAdmission() error {
	if s.backgroundStopping.Load() {
		return &protocol.RemoteError{Code: "notSent", Message: "后台服务正在关闭，草稿已保留"}
	}
	if s.updating.Load() {
		return &protocol.RemoteError{Code: "notSent", Message: "执行服务正在更新，操作未发送；草稿已保留"}
	}
	return nil
}
