package worker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"sync"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
)

const normalMaintenanceTimeout = 5 * time.Second

func processSummaries(observation managedexec.MaintenanceObservation) []domain.WorkerProcessSummary {
	var out []domain.WorkerProcessSummary
	for _, p := range observation.Processes {
		if !p.Infrastructure {
			out = append(out, domain.WorkerProcessSummary{PID: p.Identity.PID, Name: p.Name})
		}
	}
	return out
}

func (s *Server) diagnoseWorkerUpdate() (domain.WorkerUpdateDiagnosis, error) {
	s.maintenance.RLock()
	defer s.maintenance.RUnlock()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	return s.diagnoseWorkerUpdateLocked()
}

func (s *Server) diagnoseWorkerUpdateLocked() (out domain.WorkerUpdateDiagnosis, err error) {
	out = domain.WorkerUpdateDiagnosis{DeviceID: s.store.DeviceID(), BuildID: s.build.BuildID, CheckedAt: time.Now().UTC()}
	if op, e := s.store.WorkerMaintenance(""); e == nil {
		out.Operation = &op
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	snapshot, err := s.store.Snapshot()
	if err != nil {
		return out, err
	}
	workspaces := map[string]string{}
	for _, w := range snapshot.Workspaces {
		workspaces[w.ID] = w.Name
	}
	busy := map[string]bool{}
	for _, session := range snapshot.Sessions {
		state, e := s.store.MaintenanceSessionState(session.ID)
		if e != nil {
			return out, e
		}
		if state.Idle {
			continue
		}
		busy[session.ID] = true
		out.Blockers = append(out.Blockers, domain.WorkerUpdateBlocker{ID: session.ID, Kind: domain.WorkerBlockerExecution, SessionID: session.ID, WorkspaceID: session.WorkspaceID, SessionName: session.Title, WorkspaceName: workspaces[session.WorkspaceID], HarnessID: session.HarnessID, Status: session.Status, Reason: "仍有活动任务、待处理请求或尚未确认的执行"})
	}
	entries, err := os.ReadDir(s.executions.Directory)
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || busy[entry.Name()] {
			continue
		}
		id := entry.Name()
		blocker := domain.WorkerUpdateBlocker{ID: id, SessionID: id, Kind: domain.WorkerBlockerUnknown}
		state, stateErr := s.store.MaintenanceSessionState(id)
		if stateErr == nil {
			blocker.SessionName = state.Session.Title
			blocker.WorkspaceID = state.Session.WorkspaceID
			blocker.WorkspaceName = workspaces[state.Session.WorkspaceID]
			blocker.Status = state.Session.Status
			blocker.HarnessID = state.Session.HarnessID
		}
		scope, e := s.executions.Existing(id)
		if e != nil || scope == nil {
			blocker.Reason = "后台环境的归属记录不完整"
			out.Blockers = append(out.Blockers, blocker)
			continue
		}
		observation, e := scope.InspectMaintenance()
		if e != nil {
			blocker.Reason = e.Error()
			out.Blockers = append(out.Blockers, blocker)
			continue
		}
		manual, _ := scope.ManualStopPending()
		if observation.Idle && (!manual || len(observation.Processes) == 0) {
			continue
		}
		blocker.Processes = processSummaries(observation)
		if stateErr != nil {
			blocker.Reason = "无法确认该环境所属的会话"
			out.Blockers = append(out.Blockers, blocker)
			continue
		}
		blocker.Kind = domain.WorkerBlockerEnvironment
		blocker.Reason = "后台环境仍在运行，后台活动无法确认"
		canStop := manual
		if runtime := s.currentRuntime(id); runtime != nil {
			support, ok := runtime.(harness.ManualShutdownRuntime)
			canStop = ok && support.ManualShutdownSafe()
		}
		if canStop && state.Idle {
			blocker.Target = &domain.WorkerMaintenanceTarget{SessionID: id, Generation: scope.Generation, ProcessRef: observation.Ref, Revision: state.Session.Revision, QueueRevision: state.Queue.Revision}
		} else {
			blocker.Reason = "此运行环境暂不支持人工关闭，请检查相关会话"
		}
		out.Blockers = append(out.Blockers, blocker)
	}
	if s.hasActiveSignIn() {
		out.Blockers = append(out.Blockers, domain.WorkerUpdateBlocker{ID: "agent-login", Kind: domain.WorkerBlockerRuntime, Reason: "正在登录 Agent，完成后再更新"})
	}
	s.runtimeMu.RLock()
	for id, state := range s.catalogStates {
		if state.phase == domain.RuntimeRefreshing {
			out.Blockers = append(out.Blockers, domain.WorkerUpdateBlocker{ID: "runtime:" + id, Kind: domain.WorkerBlockerRuntime, Reason: "正在更新运行环境"})
		}
	}
	s.runtimeMu.RUnlock()
	return out, nil
}

func maintenanceDigest(in domain.WorkerMaintenanceInput) string {
	b, _ := json.Marshal(in)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func maintenanceReceipt(op domain.WorkerMaintenance, in domain.WorkerMaintenanceInput) (bool, error) {
	for _, action := range op.Actions {
		if action.ID == in.ActionID {
			if action.Digest != maintenanceDigest(in) {
				return false, errors.New("处理动作标识与原请求不一致")
			}
			return true, nil
		}
	}
	return false, nil
}
func validMaintenanceInput(s *Server, in domain.WorkerMaintenanceInput) error {
	if in.ID == "" || len(in.ID) > 256 || in.ActionID == "" || len(in.ActionID) > 256 || in.DeviceID != s.store.DeviceID() || len(in.Targets) > 32 {
		return workNotSent(errors.New("设备或处理标识无效"))
	}
	return nil
}

func (s *Server) applyWorkerMaintenance(in domain.WorkerMaintenanceInput) (op domain.WorkerMaintenance, err error) {
	sort.Slice(in.Targets, func(i, j int) bool { return in.Targets[i].SessionID < in.Targets[j].SessionID })
	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err = validMaintenanceInput(s, in); err != nil {
		return op, err
	}
	previous, e := s.store.WorkerMaintenance(in.ID)
	if e == nil {
		if repeated, err := maintenanceReceipt(previous, in); err != nil || repeated {
			return previous, err
		}
		if in.Action == "force" {
			return s.forceWorkerMaintenanceLocked(previous, in)
		}
		return previous, workNotSent(errors.New("此处理已接纳，请查询原操作"))
	} else if !errors.Is(e, sql.ErrNoRows) {
		return op, e
	}
	if in.Action != "stop" || len(in.Targets) == 0 || s.ctx.Err() != nil {
		return op, workNotSent(errors.New("缺少明确的后台环境选择"))
	}
	if s.config.VerifyReplacement == nil || s.config.ReplaceWorker == nil {
		return op, harness.Unsupported("worker-maintenance")
	}
	if in.ExpectedBuildID != s.build.BuildID || in.BuildID == "" || in.BuildID == s.build.BuildID {
		return op, workNotSent(errors.New("执行服务版本已变化，请重新检查"))
	}
	if err = s.config.VerifyReplacement(s.ctx, domain.WorkerReplacement{ExpectedBuildID: in.ExpectedBuildID, BuildID: in.BuildID, Explicit: true}); err != nil {
		return op, workNotSent(err)
	}
	diagnosis, err := s.diagnoseWorkerUpdateLocked()
	if err != nil {
		return op, err
	}
	eligible := map[string]domain.WorkerUpdateBlocker{}
	for _, b := range diagnosis.Blockers {
		if b.Target != nil {
			eligible[b.SessionID] = b
		}
	}
	op = domain.WorkerMaintenance{ID: in.ID, DeviceID: in.DeviceID, ExpectedBuildID: in.ExpectedBuildID, BuildID: in.BuildID, Phase: domain.WorkerMaintenanceStopping, Actions: []domain.WorkerMaintenanceAction{{ID: in.ActionID, Digest: maintenanceDigest(in)}}}
	for _, target := range in.Targets {
		b, ok := eligible[target.SessionID]
		if !ok || *b.Target != target {
			return op, workNotSent(errors.New("会话或后台进程已变化，请重新检查并确认"))
		}
		op.Targets = append(op.Targets, domain.WorkerMaintenanceResult{Target: target, SessionName: b.SessionName, WorkspaceName: b.WorkspaceName, State: "stopping", Processes: b.Processes, ProcessRef: target.ProcessRef})
	}
	var undo []func() error
	accepted := false
	defer func() {
		if !accepted {
			for i := len(undo) - 1; i >= 0; i-- {
				err = errors.Join(err, undo[i]())
			}
		}
	}()
	for _, target := range in.Targets {
		scope, e := s.executions.Existing(target.SessionID)
		if e != nil {
			return op, e
		}
		if scope == nil || scope.Generation != target.Generation {
			return op, workNotSent(errors.New("运行环境已变化，请重新检查"))
		}
		restore, e := scope.BeginManualStop(in.ID, target.ProcessRef)
		if e != nil {
			return op, workNotSent(e)
		}
		undo = append(undo, restore)
	}
	op, err = s.store.AcceptWorkerMaintenance(op)
	if err != nil {
		return op, workNotSent(err)
	}
	accepted = true
	for _, target := range op.Targets {
		s.suspendMaintenanceEvents(target.Target.SessionID)
	}
	s.startMaintenanceTargets(op)
	return op, nil
}

func (s *Server) forceWorkerMaintenanceLocked(op domain.WorkerMaintenance, in domain.WorkerMaintenanceInput) (domain.WorkerMaintenance, error) {
	if op.Phase != domain.WorkerMaintenanceNeedsForce || op.Revision != in.Revision || in.DeviceID != op.DeviceID || len(in.Targets) == 0 || len(op.Actions) >= 64 {
		return op, workNotSent(errors.New("处理状态已变化，请重新检查"))
	}
	if s.config.VerifyReplacement == nil {
		return op, harness.Unsupported("worker-maintenance")
	}
	if err := s.config.VerifyReplacement(s.ctx, domain.WorkerReplacement{ExpectedBuildID: op.ExpectedBuildID, BuildID: op.BuildID, Explicit: true}); err != nil {
		return op, workNotSent(err)
	}
	wanted := map[string]bool{}
	for _, target := range in.Targets {
		found := false
		for _, result := range op.Targets {
			found = found || (result.Target.SessionID == target.SessionID && result.Target.Generation == target.Generation && result.State == "awaitingForce")
		}
		if !found || wanted[target.SessionID] {
			return op, workNotSent(errors.New("强制结束超出原确认范围"))
		}
		wanted[target.SessionID] = true
		scope, err := s.executions.Existing(target.SessionID)
		if err != nil || scope == nil || scope.Generation != target.Generation {
			return op, workNotSent(errors.New("运行环境已变化"))
		}
		observation, err := scope.InspectMaintenance()
		if err != nil {
			return op, workNotSent(err)
		}
		if observation.Ref != target.ProcessRef {
			return op, workNotSent(errors.New("进程已变化，请重新确认强制结束范围"))
		}
	}
	op.Actions = append(op.Actions, domain.WorkerMaintenanceAction{ID: in.ActionID, Digest: maintenanceDigest(in)})
	op.Phase = domain.WorkerMaintenanceStopping
	op.StartedAt = time.Now().UTC()
	op.Message = "正在强制结束已确认的环境"
	for i := range op.Targets {
		if wanted[op.Targets[i].Target.SessionID] {
			op.Targets[i].State = "stopping"
		}
	}
	saved, err := s.store.SaveWorkerMaintenance(op, op.Revision)
	if err != nil {
		return op, err
	}
	op = saved
	for _, target := range in.Targets {
		scope, scopeErr := s.executions.Existing(target.SessionID)
		if scopeErr != nil || scope == nil {
			op.Phase = domain.WorkerMaintenanceNeedsForce
			op.Message = "进程归属记录已变化，请重新检查"
			return s.store.SaveWorkerMaintenance(op, op.Revision)
		}
		if err = scope.ForceManualStop(op.ID, in.ActionID); err != nil {
			op.Phase = domain.WorkerMaintenanceNeedsForce
			op.Message = err.Error()
			return s.store.SaveWorkerMaintenance(op, op.Revision)
		}
	}
	s.startMaintenanceTargets(op)
	return op, nil
}

func (s *Server) suspendMaintenanceEvents(id string) {
	s.runtimeMu.RLock()
	events := s.runtimeEvents[id]
	s.runtimeMu.RUnlock()
	if events != nil {
		events.Store(false)
	}
}

func (s *Server) startMaintenanceTargets(op domain.WorkerMaintenance) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		var closers sync.WaitGroup
		defer closers.Wait()
		for _, result := range op.Targets {
			if result.State != "stopping" {
				continue
			}
			s.maintenance.Lock()
			s.dispatchMu.Lock()
			current, err := s.store.WorkerMaintenance(op.ID)
			if err != nil || s.ctx.Err() != nil || current.Phase != domain.WorkerMaintenanceStopping {
				s.dispatchMu.Unlock()
				s.maintenance.Unlock()
				return
			}
			scope, err := s.executions.Existing(result.Target.SessionID)
			if err == nil && scope != nil && scope.Generation == result.Target.Generation {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				err = scope.Stop(ctx)
				cancel()
			}
			runtime := s.currentRuntime(result.Target.SessionID)
			s.dispatchMu.Unlock()
			s.maintenance.Unlock()
			if runtime != nil && scope != nil && (err == nil || errors.Is(err, managedexec.ErrManualStillRunning)) {
				closers.Add(1)
				go func() {
					defer closers.Done()
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					defer cancel()
					_ = runtime.Close(ctx)
				}()
			}
		}
	}()
}

func (s *Server) cancelWorkerMaintenance(in domain.WorkerMaintenanceInput) (domain.WorkerMaintenance, error) {
	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	var none domain.WorkerMaintenance
	if err := validMaintenanceInput(s, in); err != nil {
		return none, err
	}
	op, err := s.store.WorkerMaintenance(in.ID)
	if err != nil {
		return op, err
	}
	if repeated, e := maintenanceReceipt(op, in); e != nil || repeated {
		return op, e
	}
	if op.Phase == domain.WorkerMaintenanceUpdating {
		return op, workNotSent(errors.New("已接纳执行服务替换，请等待重新连接"))
	}
	if !op.Phase.Active() {
		return op, nil
	}
	if op.Revision != in.Revision {
		return op, workNotSent(errors.New("处理状态已变化，请重新查询"))
	}
	op.Phase = domain.WorkerMaintenanceCancelled
	op.Message = "已取消本次处理，待发消息保持暂停"
	op.Actions = append(op.Actions, domain.WorkerMaintenanceAction{ID: in.ActionID, Digest: maintenanceDigest(in)})
	op, err = s.store.SaveWorkerMaintenance(op, op.Revision)
	if err != nil {
		return op, err
	}
	for _, result := range op.Targets {
		scope, e := s.executions.Existing(result.Target.SessionID)
		if e != nil || scope == nil {
			continue
		}
		if e = scope.CancelManualStop(op.ID); e != nil {
			return op, e
		}
		if scope.Admitting() {
			s.runtimeMu.RLock()
			events := s.runtimeEvents[result.Target.SessionID]
			s.runtimeMu.RUnlock()
			if events != nil {
				events.Store(true)
			}
		}
	}
	return op, nil
}

func (s *Server) sessionMaintenanceAdmission(id string) error {
	if id == "" {
		return nil
	}
	blocked, err := s.store.MaintenanceSessionBlocked(id)
	if err != nil {
		return err
	}
	if blocked {
		return workNotSent(errors.New("正在结束运行环境，消息未发送；草稿已保留"))
	}
	if s.executions == nil {
		return nil
	}
	scope, err := s.executions.Existing(id)
	if err != nil {
		return workNotSent(err)
	}
	if scope == nil {
		return nil
	}
	manual, err := scope.ManualStopPending()
	if err != nil {
		return workNotSent(err)
	}
	if manual && !scope.Admitting() {
		if err = scope.VerifyStopped(); err != nil {
			return workNotSent(errors.New("原后台环境尚未确认退出，请先完成处理；草稿已保留"))
		}
	}
	return nil
}

func (s *Server) workerMaintenanceLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			_ = s.advanceWorkerMaintenance(now)
		}
	}
}

func (s *Server) advanceWorkerMaintenance(now time.Time) error {
	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.ctx.Err() != nil || s.updating.Load() {
		return nil
	}
	op, err := s.store.WorkerMaintenance("")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !op.Phase.Active() || op.Phase == domain.WorkerMaintenanceUpdating {
		return nil
	}
	before, _ := json.Marshal(op)
	allStopped := true
	needsDecision := false
	for i := range op.Targets {
		result := &op.Targets[i]
		if result.State == "stopped" {
			continue
		}
		allStopped = false
		scope, e := s.executions.Existing(result.Target.SessionID)
		if e != nil || scope == nil || scope.Generation != result.Target.Generation {
			result.State = "unknown"
			result.Message = "运行环境的归属已变化，请保留现场核对"
			needsDecision = true
			continue
		}
		observation, e := scope.InspectMaintenance()
		if e != nil {
			result.State = "unknown"
			result.Message = e.Error()
			needsDecision = true
			continue
		}
		result.Processes = processSummaries(observation)
		result.ProcessRef = observation.Ref
		e = scope.ObserveManualStop()
		if e == nil {
			result.State = "stopped"
			result.Message = ""
			result.Processes = nil
			s.releaseMaintenanceRuntime(result.Target.SessionID)
			continue
		}
		if !errors.Is(e, managedexec.ErrManualStillRunning) {
			result.State = "unknown"
			result.Message = e.Error()
			needsDecision = true
			continue
		}
		if now.Sub(op.StartedAt) >= normalMaintenanceTimeout || op.Phase == domain.WorkerMaintenanceNeedsForce {
			result.State = "awaitingForce"
			result.Message = "正常关闭尚未完成，强制结束需要单独确认"
			needsDecision = true
		}
	}
	allStopped = true
	for _, result := range op.Targets {
		allStopped = allStopped && result.State == "stopped"
	}
	if allStopped {
		op.Phase = domain.WorkerMaintenanceWaiting
		op.Message = "所选环境已结束，等待设备空闲后更新"
	} else if needsDecision {
		op.Phase = domain.WorkerMaintenanceNeedsForce
		op.Message = "仍有后台环境未退出，请选择下一步"
	}
	after, _ := json.Marshal(op)
	if !reflect.DeepEqual(before, after) {
		op, err = s.store.SaveWorkerMaintenance(op, op.Revision)
		if err != nil {
			return err
		}
	}
	if op.Phase == domain.WorkerMaintenanceWaiting {
		result, e := s.prepareWorkerUpdateLocked(domain.WorkerReplacement{ExpectedBuildID: s.build.BuildID, BuildID: op.BuildID})
		if e != nil {
			current, readErr := s.store.WorkerMaintenance(op.ID)
			if readErr != nil {
				return errors.Join(e, readErr)
			}
			if current.Phase == domain.WorkerMaintenanceFailed {
				return e
			}
			current.Phase = domain.WorkerMaintenanceFailed
			current.Message = e.Error()
			_, err = s.store.SaveWorkerMaintenance(current, current.Revision)
			return errors.Join(e, err)
		}
		if result.Ready {
			s.cancel()
			s.listener.Close()
		}
	}
	return nil
}

func (s *Server) releaseMaintenanceRuntime(id string) {
	s.runtimeMu.Lock()
	delete(s.runtimes, id)
	delete(s.runtimeCatalogs, id)
	delete(s.runtimeEvents, id)
	s.runtimeMu.Unlock()
	delete(s.idleRuntimes, id)
	s.mu.Lock()
	delete(s.bound, id)
	s.mu.Unlock()
}

func (s *Server) recoverWorkerMaintenance() error {
	op, err := s.store.WorkerMaintenance("")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !op.Phase.Active() {
		return nil
	}
	if op.Phase == domain.WorkerMaintenanceUpdating {
		if op.BuildID == s.build.BuildID {
			op.Phase = domain.WorkerMaintenanceCompleted
			op.Message = "执行服务已更新"
		} else {
			op.Phase = domain.WorkerMaintenanceFailed
			op.Message = "上次更新结果未确认，请检查实际构建后再重试"
		}
	} else if op.Phase != domain.WorkerMaintenanceWaiting {
		op.Phase = domain.WorkerMaintenanceNeedsForce
		op.Message = "上次关闭结果未确认，请重新核对后决定是否强制结束"
		for i := range op.Targets {
			if op.Targets[i].State != "stopped" {
				op.Targets[i].State = "awaitingForce"
			}
		}
	}
	_, err = s.store.SaveWorkerMaintenance(op, op.Revision)
	return err
}

func (s *Server) workerMaintenanceStatus(in protocol.WorkerMaintenanceQuery) (domain.WorkerMaintenance, error) {
	if in.DeviceID != s.store.DeviceID() {
		return domain.WorkerMaintenance{}, workNotSent(errors.New("查询不属于此设备"))
	}
	return s.store.WorkerMaintenance(in.ID)
}
