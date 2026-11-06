package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"time"
)

func (s *Server) currentRuntime(session ...string) *codex.Runtime {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if len(session) > 0 {
		return s.runtimes[session[0]]
	}
	return s.runtime
}

// Caller holds dispatchMu. An exited app-server can be replaced, but a recorded
// live predecessor must first be stopped by an explicit user action.
func (s *Server) recoveryRuntime(ctx context.Context, stop bool) (*codex.Runtime, error) {
	r := s.currentRuntime()
	if r != nil && !r.Stopped() {
		return r, nil
	}
	if stop {
		if err := stopPreviousProcess(ctx, s.config.Directory); err != nil {
			return nil, err
		}
	}
	if err := checkPreviousProcess(s.config.Directory); err != nil {
		return nil, err
	}
	if err := recordProcess(s.config.Directory, 0); err != nil {
		return nil, err
	}
	next, err := codex.Start(ctx, codex.Options{Executable: s.config.CodexExecutable, Home: s.config.CodexHome, OnEvent: s.onEvent, OnStart: func(pid int) error { return recordProcess(s.config.Directory, pid) }})
	if err != nil {
		os.Remove(filepath.Join(s.config.Directory, "runtime-process.json"))
		return nil, err
	}
	s.runtimeMu.Lock()
	s.runtime = next
	s.runtimeMu.Unlock()
	s.recoveryChecked = map[string]recoveryAttempt{}
	s.watchRuntime(next)
	return next, nil
}
func (s *Server) reconcile(in protocol.RecoveryInput) (protocol.RecoveryResult, error) {
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := ctx.Err(); err != nil {
		return protocol.RecoveryResult{}, err
	}
	return s.reconcileLocked(ctx, in)
}
func (s *Server) reconcileLocked(ctx context.Context, in protocol.RecoveryInput) (protocol.RecoveryResult, error) {
	generation := s.recoveryRefresh.Load()
	result := protocol.RecoveryResult{SessionID: in.SessionID, Status: "unknown"}
	check := in.Action == "" || in.Action == "check"
	if !check && in.Action != "end" && in.Action != "interrupt" && in.Action != "stop-runtime" && in.Action != "stop-tasks" {
		return result, errors.New("未知的恢复操作")
	}
	d, err := s.store.Session(in.SessionID, 0, 500)
	if err != nil {
		return result, err
	}
	if !check && (d.Session.Revision != in.Revision || d.Session.Status != "unknown") {
		return result, errors.New("会话状态已改变，请重新核对")
	}
	if d.Session.Status != "unknown" && d.Session.Status != "abandoned" {
		result.Status = d.Session.Status
		result.Readiness = s.runtimeReadiness()
		if d.Session.Recovery != nil {
			result.CanContinue = d.Session.Recovery.CanContinue
		}
		return result, nil
	}
	info := domain.SessionRecovery{Phase: "checking"}
	if old := d.Session.Recovery; old != nil {
		info = *old
		info.Phase = "checking"
	}
	session, err := s.store.SetRecovery(in.SessionID, &info)
	if err != nil {
		return result, err
	}
	d.Session = session
	finish := func() (protocol.RecoveryResult, error) {
		phase := "attention"
		if result.Status != "unknown" && result.Status != "active" {
			phase = "ready"
		}
		updated, e := s.store.SetRecovery(in.SessionID, &domain.SessionRecovery{Phase: phase, Message: result.Message, CanContinue: result.CanContinue, CanEnd: result.CanEnd, NeedsStop: result.NeedsStop, Tasks: result.Tasks, CanStopTasks: result.CanStopTasks})
		if e == nil {
			s.recoveryChecked[in.SessionID] = recoveryAttempt{Revision: updated.Revision, Generation: generation}
		}
		return result, e
	}
	runtime := s.currentRuntime(in.SessionID)
	managed, managedErr := s.executions.Existing(in.SessionID)
	if managedErr != nil {
		result.Message = "执行归属记录无法读取，请重新核对"
		return finish()
	}
	if managed != nil && runtime != nil && !runtime.Observes(d.Session) {
		if cleanupErr := s.cleanManagedExecution(ctx, in.SessionID); cleanupErr != nil {
			result.Message = "旧工具尚未确认停止，请重新核对"
			return finish()
		}
	}
	if in.Action == "stop-runtime" && runtime != nil && !runtime.Stopped() {
		// The explicit action targets this session's adapter, never the catalog
		// or another session. Descendant execution remains unknown after exit.
		runtime.Close()
	}
	if runtime == nil || runtime.Stopped() || d.Session.ExecutionGuard != "" {
		if cleanupErr := s.cleanSessionRuntime(ctx, in.SessionID); cleanupErr != nil {
			result.Message = "会话进程归属记录尚未确认，未启动新的 Agent"
			result.Readiness = domain.Readiness{State: "unavailable", Error: cleanupErr.Error()}
			return finish()
		}
		runtime, err = s.recoveryRuntime(ctx, in.Action == "stop-runtime")
		if latest, e := s.store.Session(in.SessionID, 0, 1); e == nil {
			d.Session = latest.Session
		}
	}
	if err != nil {
		result.Message = "暂时无法核对执行状态"
		result.NeedsStop = ownedProcessPresent(s.config.Directory)
		result.Readiness = domain.Readiness{State: "unavailable", Error: err.Error()}
		return finish()
	}
	result.Readiness = runtime.Readiness()
	source, err := runtime.Inspect(ctx, d.Session)
	if !runtime.Observes(d.Session) {
		if proofErr := s.managedStopProof(in.SessionID, &source); proofErr != nil {
			result.Message = "旧工具尚未确认停止，请重新核对"
			return finish()
		}
	}
	result.Tasks, result.CanStopTasks = s.recoveryTasks(d, source)
	if err == nil && in.Action == "interrupt" {
		for _, turn := range source.Turns {
			if turn.Status == "running" {
				if e := runtime.Interrupt(ctx, d.Session, turn.ID); e != nil {
					result.Message = e.Error()
					return finish()
				}
			}
		}
		for source.State == "active" || recoveryRunning(source) {
			select {
			case <-ctx.Done():
				result.Message = "停止结果尚未确认，请重新核对"
				return finish()
			case <-time.After(30 * time.Millisecond):
			}
			source, err = runtime.Inspect(ctx, d.Session)
			if err != nil {
				break
			}
		}
	}
	if d.Session.Status == "abandoned" {
		result.Status = "abandoned"
		if err == nil && source.State != "active" && source.State != "systemError" && !recoveryRunning(source) {
			result.CanContinue, err = s.store.CanContinue(in.SessionID, source)
		}
		result.Message = "暂时无法恢复原线程，可重新核对或在新会话接续"
		if result.CanContinue {
			result.Message = "可在原会话继续；待发送消息仍保持暂停"
		}
		return finish()
	}
	if err != nil {
		result.Message = "暂时无法读取完整历史，请重新核对"
		result.CanStopTasks = false
		own := s.currentRuntime(d.Session.ID)
		result.CanEnd = !runtime.Observes(d.Session) && (own == nil || own.Stopped())
		result.NeedsStop = own != nil && !own.Stopped()
		if in.Action != "end" || !result.CanEnd {
			return finish()
		}
		source = domain.RecoveryHistory{State: "notLoaded", Inputs: map[string]string{}}
	}
	source.Attached = runtime.Observes(d.Session)
	if (source.State == "active" || recoveryRunning(source)) && !source.Attached {
		result.Status = "active"
		result.Message = "旧轮仍在执行，暂时无法接回"
		return finish()
	}
	if source.State == "systemError" {
		result.Message = "暂时无法确认线程状态，请稍后重新核对"
		return finish()
	}
	if in.Action == "stop-tasks" {
		if err != nil || !source.Available || source.Attached || source.State == "active" || recoveryRunning(source) || len(in.Targets) == 0 {
			result.Message = "还不能安全停止这些任务，请查看处理详情"
			return finish()
		}
		selected := map[string]bool{}
		for _, id := range in.Targets {
			if selected[id] {
				result.Message = "进程选择重复，请重新选择"
				return finish()
			}
			selected[id] = true
		}
		var records []recoveryToolProcess
		for _, task := range result.Tasks {
			if !selected[task.ID] {
				continue
			}
			r, e := readRecoveryTool(s.config.Directory, in.SessionID, task.ItemID, task.Command)
			if e != nil || toolSelectionID(r) != task.ID || task.State == "unknown" {
				result.Message = "所选进程已变化，请重新检查"
				return finish()
			}
			records = append(records, r)
			delete(selected, task.ID)
		}
		if len(selected) > 0 {
			result.Message = "所选进程已变化，请重新检查"
			return finish()
		}
		for _, r := range records {
			e := stopRecoveryTool(ctx, r)
			if e != nil {
				result.Message = "任务尚未全部停止，请重新检查"
				result.Tasks, result.CanStopTasks = s.recoveryTasks(d, source)
				return finish()
			}
		}
		result.Tasks, result.CanStopTasks = s.recoveryTasks(d, source)
		result.CanEnd = true
		result.Message = "所选进程组已停止。请另外确认没有其他后台任务仍在运行，再结束旧轮记录。"
		return finish()
	}
	updated, err := s.store.Reconcile(in.SessionID, d.Session.Revision, source, in.Action == "end")
	if err != nil {
		latest, e := s.store.Session(in.SessionID, 0, 1)
		if e != nil {
			return result, e
		}
		result.Status = latest.Session.Status
		result.Message = "状态已更新，请重新核对"
		return finish()
	}
	result.Status = updated.Status
	result.CanEnd = updated.Status == "unknown"
	result.Message = updated.Error
	if updated.Recovery != nil {
		result.CanContinue = updated.Recovery.CanContinue
	}
	if result.Message == "" {
		result.Message = "已核对执行状态"
	}
	return finish()
}
func (s *Server) runtimeReadiness() domain.Readiness {
	if r := s.currentRuntime(); r != nil {
		return r.Readiness()
	}
	return s.readiness
}

func recoveryRunning(h domain.RecoveryHistory) bool {
	for _, t := range h.Turns {
		if t.Status == "running" {
			return true
		}
	}
	return false
}

// Caller holds dispatchMu, shared with reconciliation. Accepted work belongs to
// the runtime it reserved; a delayed goroutine never adopts a replacement.
func (s *Server) dispatchReserved(op domain.Operation, runtime *codex.Runtime) bool {
	current, err := s.store.Operation(op.ID)
	if err != nil || current.State != "accepted" {
		return false
	}
	if runtime != nil && runtime == s.currentRuntime(op.SessionID) && !runtime.Stopped() && s.ctx.Err() == nil {
		return true
	}
	defer s.wakeRecovery()
	message := "Runtime changed before dispatch; input was not replayed"
	if err = s.store.SetOperation(op.ID, "indeterminate", message); err != nil {
		s.cancel()
		return false
	}
	if _, err = s.store.PauseQueue(op.SessionID, message); err != nil {
		s.cancel()
		return false
	}
	return false
}
