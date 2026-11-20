package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"time"
)

func (s *Server) currentRuntime(session ...string) harness.Runtime {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if len(session) > 0 {
		return s.runtimes[session[0]]
	}
	return s.runtime
}

func recoveryRunning(h domain.RecoveryHistory) bool {
	for _, t := range h.Turns {
		if t.Status == "running" {
			return true
		}
	}
	return false
}

// Caller holds dispatchMu. An exited app-server can be replaced, but a recorded
// live predecessor must first be stopped by an explicit user action.
func (s *Server) recoveryRuntime(ctx context.Context, stop bool, selected ...string) (harness.Runtime, error) {
	if err := s.updateAdmission(); err != nil {
		return nil, err
	}
	id := s.config.DefaultHarnessID
	if len(selected) > 0 {
		id = selected[0]
	}
	previous := s.catalogRuntime(id)
	// Catalog discovery is read-only native I/O. Its own gate serializes
	// replacement; device-wide lifecycle admission must remain available.
	s.dispatchMu.Unlock()
	next, err := s.openCatalog(ctx, id, stop)
	s.dispatchMu.Lock()
	if err == nil {
		err = s.updateAdmission()
	}
	if err == nil && (previous == nil || next.Done() != previous.Done()) {
		s.recoveryChecked = map[string]recoveryAttempt{}
	}
	return next, err
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
	if in.SessionID == "" {
		return protocol.RecoveryResult{}, errors.New("缺少会话标识")
	}
	if err := s.cleanRuntimeRetirements(in.SessionID); err != nil {
		return protocol.RecoveryResult{}, err
	}
	release, err := s.beginSessionTransition(ctx, in.SessionID)
	if err != nil {
		return protocol.RecoveryResult{}, err
	}
	defer release()
	transition := s.sessionTransitions[in.SessionID]
	ctx = transition.ctx

	generation := s.recoveryRefresh.Load()
	result := protocol.RecoveryResult{SessionID: in.SessionID, Status: "unknown", Readiness: s.runtimeReadiness(in.SessionID)}
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
	if !d.Session.Status.NeedsRecovery() {
		result.Status = string(d.Session.Status)
		result.Readiness = s.runtimeReadiness(in.SessionID)
		if d.Session.Recovery != nil {
			result.CanContinue = d.Session.Recovery.CanContinue
		}
		return result, nil
	}

	s.mu.Lock()
	epoch := s.dispatchEpochs[in.SessionID]
	s.mu.Unlock()
	stillCurrent := func() bool {
		s.mu.Lock()
		unchanged := s.dispatchEpochs[in.SessionID] == epoch
		s.mu.Unlock()
		latest, e := s.store.Session(in.SessionID, 0, 1)
		return transition.active() && unchanged && e == nil && latest.Session.ThreadID == d.Session.ThreadID && latest.Session.Archived == d.Session.Archived && latest.Session.Status.NeedsRecovery()
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
		if !stillCurrent() {
			latest, e := s.store.Session(in.SessionID, 0, 1)
			if e == nil {
				result.Status = string(latest.Session.Status)
				result.Readiness = s.runtimeReadiness(in.SessionID)
			}
			return result, e
		}
		result.Readiness = s.runtimeReadiness(in.SessionID)
		phase := domain.RecoveryAttention
		if result.Status != "unknown" && result.Status != "active" {
			phase = domain.RecoveryReady
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
	if in.Action == "stop-runtime" && runtime != nil && !runtime.Stopped() {
		// The explicit action targets this session's adapter, never the catalog
		// or another session. Descendant execution remains unknown after exit.
		s.dispatchMu.Unlock()
		runtime.Close(ctx)
		s.dispatchMu.Lock()
		if !stillCurrent() {
			return finish()
		}
	}
	if runtime == nil || runtime.Stopped() || d.Session.ExecutionGuard != "" || managed != nil && !runtime.Observes(d.Session) {
		cleanupErr := s.cleanSessionRuntime(ctx, in.SessionID)
		if !stillCurrent() {
			return finish()
		}
		if current := s.currentRuntime(in.SessionID); runtime != nil && (current == nil || current.Done() != runtime.Done()) {
			return finish()
		}
		if cleanupErr != nil {
			if err := s.detachFailedRuntime(in.SessionID, cleanupErr); err != nil {
				result.Message = "会话进程归属记录尚未确认，未启动新的 Agent"
				return finish()
			}
			result.Status, result.Message = "failed", "旧 Agent 已退休，可使用新的 Agent 继续；原执行结果未确认"
			return finish()
		}
		runtime, err = s.recoveryRuntime(ctx, in.Action == "stop-runtime", d.Session.HarnessID)
		if !stillCurrent() {
			return finish()
		}
		if latest, e := s.store.Session(in.SessionID, 0, 1); e == nil {
			d.Session = latest.Session
		}
	}
	if err != nil {
		result.Message = "暂时无法核对执行状态"
		result.NeedsStop = ownedProcessPresent(s.catalogDirectory(d.Session.HarnessID))
		return finish()
	}
	if runtime == s.catalogRuntime(d.Session.HarnessID) {
		var release func()
		runtime, release, err = s.borrowCatalog(d.Session.HarnessID)
		if err != nil {
			result.Message = "正在更新运行环境，稍后自动核对"
			return finish()
		}
		defer release()
	}
	if !runtime.Readiness().Capabilities.Recovery {
		result.Message = "此 Agent 暂不支持核对执行状态"
		return finish()
	}
	if in.Action == "interrupt" && !runtime.Readiness().Capabilities.Interrupt {
		result.Message = "此 Agent 不支持中断执行"
		result, finishErr := finish()
		return result, errors.Join(harness.Unsupported("interrupt"), finishErr)
	}
	result.Readiness = s.runtimeReadiness(in.SessionID)
	s.dispatchMu.Unlock()
	source, err := runtime.Inspect(ctx, d.Session)
	s.dispatchMu.Lock()
	if !stillCurrent() {
		return finish()
	}
	if !runtime.Observes(d.Session) {
		if proofErr := s.managedStopProof(in.SessionID, &source); proofErr != nil {
			result.Message = "旧工具尚未确认停止，请重新核对"
			return finish()
		}
	}
	settleExplicitStoppedHistory(&source, in.Action == "end")
	result.Tasks, result.CanStopTasks = s.recoveryTasks(d, source)
	if err == nil && in.Action == "interrupt" {
		for _, turn := range source.Turns {
			if turn.Status == "running" {
				s.dispatchMu.Unlock()
				e := runtime.Interrupt(ctx, d.Session, turn.ID)
				s.dispatchMu.Lock()
				if !stillCurrent() {
					return finish()
				}
				if e != nil {
					result.Message = e.Error()
					return finish()
				}
			}
		}
		for source.State == "active" || recoveryRunning(source) {
			s.dispatchMu.Unlock()
			select {
			case <-ctx.Done():
				s.dispatchMu.Lock()
				result.Message = "停止结果尚未确认，请重新核对"
				return finish()
			case <-time.After(30 * time.Millisecond):
			}
			source, err = runtime.Inspect(ctx, d.Session)
			s.dispatchMu.Lock()
			if !stillCurrent() {
				return finish()
			}
			if err != nil {
				break
			}
		}
	}
	if d.Session.Status == "abandoned" {
		result.Status = "abandoned"
		if err == nil && source.State != "active" && source.State != "failed" && !recoveryRunning(source) {
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
		source = domain.RecoveryHistory{State: domain.SourceUnavailable, Inputs: map[string]string{}}
	}
	source.Attached = runtime.Observes(d.Session)
	if (source.State == "active" || recoveryRunning(source)) && !source.Attached {
		result.Status = "active"
		result.Message = "旧轮仍在执行，暂时无法接回"
		if source.ExplicitlyStopped {
			result.Status = "unknown"
			result.CanEnd = true
			result.Message = "原运行环境已关闭，但源历史仍未确认结束；可确认结束旧轮记录后继续"
		}
		return finish()
	}
	if source.State == "failed" {
		result.Message = "暂时无法确认线程状态，请稍后重新核对"
		result.CanEnd = source.ExplicitlyStopped
		if !source.ExplicitlyStopped || in.Action != "end" {
			return finish()
		}
		source.State = domain.SourceUnavailable
		source.Available = false
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
			s.dispatchMu.Unlock()
			e := stopRecoveryTool(ctx, r)
			s.dispatchMu.Lock()
			if !stillCurrent() {
				return finish()
			}
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
		result.Status = string(latest.Session.Status)
		result.Message = "状态已更新，请重新核对"
		return finish()
	}
	result.Status = string(updated.Status)
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

// Runtime exit proves local execution stopped, never that input was completed.
// Only covered turns may automatically become interrupted. The end action is
// separate user consent to abandon an unconfirmed local record.
func settleExplicitStoppedHistory(source *domain.RecoveryHistory, end bool) {
	if !source.ExplicitlyStopped || source.Attached {
		return
	}
	active := false
	for i := range source.Turns {
		turn := &source.Turns[i]
		if turn.Status == "running" || turn.Status == "unknown" {
			if source.ManagedStoppedTurns[turn.ID] {
				turn.Status = "interrupted"
				turn.Error = "用户关闭了原运行环境"
			} else if end {
				turn.Status = "abandoned"
				turn.Error = "用户确认结束旧轮记录，原执行结果未确认"
			} else {
				active = true
			}
		}
	}
	if !active && source.State != domain.SourceFailed {
		source.State = domain.SourceIdle
	}
}

// Caller holds dispatchMu, shared with reconciliation. Accepted work belongs to
// the runtime it reserved; a delayed goroutine never adopts a replacement.
func (s *Server) dispatchReserved(op domain.Operation, runtime harness.Runtime) bool {
	current, err := s.store.Operation(op.ID)
	if err != nil || current.State != "accepted" {
		return false
	}
	if runtime != nil && s.currentRuntime(op.SessionID) != nil && runtime.Done() == s.currentRuntime(op.SessionID).Done() && !runtime.Stopped() && s.ctx.Err() == nil {
		return true
	}
	defer s.wakeRecovery()
	message := "运行环境在交接输入前已改变，输入已保留"
	err = s.store.RejectUnhandedInput(op.ID, message)
	if errors.Is(err, store.ErrInputHandoffRevoked) {
		err = s.store.SetOperation(op.ID, "indeterminate", message)
	}
	if err != nil {
		s.cancel()
		return false
	}
	if _, err = s.store.PauseQueue(op.SessionID, message); err != nil {
		s.cancel()
		return false
	}
	return false
}
