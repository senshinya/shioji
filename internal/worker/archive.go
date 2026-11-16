package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/store"
)

func (s *Server) archiveLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.cleanupArchives(time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "shioji: archive cleanup: %v\n", err)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.archiveWake:
		}
	}
}

func (s *Server) cleanupArchives(now time.Time) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.ctx.Err() != nil || s.updating.Load() || s.backgroundStopping.Load() {
		return nil
	}
	if err := s.cleanRuntimeRetirements(""); err != nil {
		return err
	}
	candidates, err := s.store.ArchiveCandidates(now)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		in := protocol.ArchiveDeleteInput{SessionID: c.SessionID, WorkspaceID: c.WorkspaceID, Revision: c.Revision}
		method := "session.delete"
		if in.WorkspaceID != "" {
			method = "workspace.delete"
		}
		// Active work, pending questions, a restore or an unknown process keep
		// the archive intact. The next sweep reevaluates authoritative state.
		if _, err = s.deleteArchive(method, in); err != nil {
			continue
		}
	}
	return s.cleanArchiveGarbage()
}

// Caller holds dispatchMu, shared with submissions, checkout and restoration.
func (s *Server) deleteArchive(method string, in protocol.ArchiveDeleteInput) (domain.ArchiveDeletion, error) {
	var zero domain.ArchiveDeletion
	if (in.CloseRuntime || in.Force) && (in.OperationID == "" || len(in.OperationID) > 256) {
		return zero, errors.New("缺少有效的删除操作标识")
	}
	var sessions []domain.Session
	if method == "workspace.delete" {
		if in.WorkspaceID == "" || in.SessionID != "" {
			return zero, errors.New("无效的工作区删除请求")
		}
		w, err := s.store.Workspace(in.WorkspaceID)
		if err != nil {
			return zero, err
		}
		if !w.Archived || !in.Force && w.Revision != in.Revision {
			return zero, errors.New("工作区已改变或未归档，请重新核对")
		}
		snapshot, err := s.store.Snapshot()
		if err != nil {
			return zero, err
		}
		for _, session := range snapshot.Sessions {
			if session.WorkspaceID == w.ID {
				sessions = append(sessions, session)
			}
		}
	} else {
		if in.SessionID == "" || in.WorkspaceID != "" {
			return zero, errors.New("无效的会话删除请求")
		}
		d, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return zero, err
		}
		w, err := s.store.Workspace(d.Session.WorkspaceID)
		if err != nil {
			return zero, err
		}
		if !in.Force && d.Session.Revision != in.Revision || !d.Session.Archived && !w.Archived {
			return zero, errors.New("会话已改变或未归档，请重新核对")
		}
		sessions = []domain.Session{d.Session}
	}
	if in.Force {
		return s.forceDeleteArchive(method, in, sessions)
	}
	// Validate every member before retiring any idle runtime in a workspace.
	for _, session := range sessions {
		if r := s.currentRuntime(session.ID); r != nil && !r.Stopped() && !r.ManagedCoverage() {
			support, ok := r.(harness.ManualShutdownRuntime)
			if !in.CloseRuntime || !ok || !support.ManualShutdownSafe() {
				return zero, errors.New("运行环境仍有未核实的执行活动，请停止并核对后再删除")
			}
		}
		state, err := s.store.RuntimeIdle(session.ID)
		if err != nil {
			return zero, err
		}
		if !state.Idle {
			return zero, errors.New("仍有运行中或待核对的任务，请处理后再删除")
		}
	}
	var undo []func() error
	scopes := map[string]*managedexec.Scope{}
	defer func() {
		for i := len(undo) - 1; i >= 0; i-- {
			if undo[i] != nil {
				_ = undo[i]()
			}
		}
	}()
	for _, session := range sessions {
		scope, err := s.executions.Existing(session.ID)
		if err != nil {
			return zero, err
		}
		if scope != nil {
			var reopen func() error
			if in.CloseRuntime {
				op := in.OperationID + ":" + session.ID
				previous, e := scope.ManualStopOperation()
				if e != nil {
					return zero, e
				}
				if previous == "" {
					observation, e := scope.InspectExplicitMaintenance()
					if e != nil {
						return zero, e
					}
					reopen, err = scope.BeginExplicitStop(op, observation.Ref, false)
				} else if previous != op {
					// A completed earlier shutdown is also sufficient; an active
					// maintenance operation must finish through its own lifecycle.
					proven, e := scope.ExplicitStopProven()
					if e != nil || !proven {
						return zero, errors.New("运行环境正在处理中，请完成核对后重试")
					}
				}
			} else {
				reopen, err = scope.FenceIfIdle()
			}
			if err != nil {
				return zero, err
			}
			undo = append(undo, reopen)
			scopes[session.ID] = scope
		} else if r := s.currentRuntime(session.ID); in.CloseRuntime && r != nil && !r.Stopped() && !r.ManagedCoverage() {
			return zero, errors.New("运行环境缺少归属记录，请先核对后再删除")
		}
	}
	for _, session := range sessions {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		var err error
		if scope := scopes[session.ID]; in.CloseRuntime && scope != nil {
			err = s.closeArchiveEnvironment(ctx, session.ID, scope)
		} else {
			err = s.cleanManagedExecution(ctx, session.ID)
		}
		cancel()
		if err != nil {
			return zero, err
		}
		// A harness without a managed scope must also retire its source pipe.
		if r := s.currentRuntime(session.ID); r != nil && !r.Stopped() {
			s.runtimeMu.RLock()
			gate := s.runtimeEvents[session.ID]
			s.runtimeMu.RUnlock()
			if gate != nil {
				gate.Store(false)
			}
			ctx, cancel = context.WithTimeout(s.ctx, 10*time.Second)
			err = r.Close(ctx)
			cancel()
			if err != nil {
				return zero, err
			}
		}
	}
	var result domain.ArchiveDeletion
	var err error
	if method == "workspace.delete" {
		result, err = s.store.DeleteArchivedWorkspace(in.WorkspaceID, in.Revision)
	} else {
		result, err = s.store.DeleteArchivedSession(in.SessionID, in.Revision)
	}
	if err != nil {
		return zero, err
	}
	// Admission stays closed for every successfully deleted session.
	undo = nil
	for _, id := range result.SessionIDs {
		s.runtimeMu.Lock()
		delete(s.runtimes, id)
		delete(s.runtimeCatalogs, id)
		delete(s.runtimeEvents, id)
		s.runtimeMu.Unlock()
		delete(s.idleRuntimes, id)
		delete(s.recoveryChecked, id)
		s.mu.Lock()
		delete(s.bound, id)
		s.mu.Unlock()
	}
	// The durable outbox retries interrupted filesystem cleanup independently
	// of the successful deletion receipt, including after a worker restart.
	if err = s.cleanArchiveGarbage(); err != nil {
		fmt.Fprintf(os.Stderr, "shioji: archive resources: %v\n", err)
	}
	return result, nil
}

// Force removes the user-visible archive even when activity cannot be proven
// idle. Durable cleanup records and scoped process identities survive it.
func (s *Server) forceDeleteArchive(method string, in protocol.ArchiveDeleteInput, sessions []domain.Session) (domain.ArchiveDeletion, error) {
	var zero domain.ArchiveDeletion
	if !in.CloseRuntime {
		return zero, errors.New("强制删除需要明确确认运行环境风险")
	}
	scopes := map[string]*managedexec.Scope{}
	var undo []func() error
	defer func() {
		for _, f := range undo {
			if f != nil {
				_ = f()
			}
		}
	}()
	warning := false
	for _, session := range sessions {
		scope, err := s.executions.Existing(session.ID)
		if err != nil {
			warning = true
			continue
		}
		if scope == nil {
			continue
		}
		scopes[session.ID] = scope
		previous, err := scope.ManualStopOperation()
		if err != nil {
			warning = true
			continue
		}
		if previous == "" {
			observation, e := scope.InspectExplicitMaintenance()
			if e != nil {
				warning = true
				continue
			}
			reopen, e := scope.BeginExplicitStop(in.OperationID+":"+session.ID, observation.Ref, true)
			if e != nil {
				warning = true
				continue
			}
			undo = append(undo, reopen)
		}
	}
	var result domain.ArchiveDeletion
	var err error
	if method == "workspace.delete" {
		result, err = s.store.ForceDeleteArchivedWorkspace(in.WorkspaceID)
	} else {
		result, err = s.store.ForceDeleteArchivedSession(in.SessionID)
	}
	if err != nil {
		return zero, err
	}
	undo = nil
	for _, id := range result.SessionIDs {
		s.runtimeMu.RLock()
		gate := s.runtimeEvents[id]
		s.runtimeMu.RUnlock()
		if gate != nil {
			gate.Store(false)
		}
		if scope := scopes[id]; scope != nil {
			op, e := scope.ManualStopOperation()
			if e != nil || op == "" {
				_ = scope.Fence()
				warning = true
			} else if e = scope.ForceManualStop(op, in.OperationID+":force:"+id); e != nil {
				warning = true
			}
		}
		delete(s.idleRuntimes, id)
		delete(s.recoveryChecked, id)
	}
	// Cleanup errors cannot veto accepted forced deletion. The same durable
	// outbox is retried after reconnects/restarts and by the archive sweep.
	for _, session := range sessions {
		if err = s.cleanArchiveGarbageItem(store.ArchiveGarbage{SessionID: session.ID, WorkspaceID: session.WorkspaceID}); err != nil {
			warning = true
		}
	}
	if warning {
		result.Warning = "归档记录已强制删除；部分运行环境尚未确认退出，后台将继续清理。"
	}
	return result, nil
}

// Explicit archive deletion grants normal shutdown only. The durable scope
// captures process identities before teardown so detached children remain
// observable after their parent exits. No process-name sweep or force follows.
func (s *Server) closeArchiveEnvironment(ctx context.Context, id string, scope *managedexec.Scope) error {
	op, err := scope.ManualStopOperation()
	if err != nil {
		return err
	}
	if err = scope.AcceptManualStop(op); err != nil {
		return err
	}
	s.runtimeMu.RLock()
	gate := s.runtimeEvents[id]
	s.runtimeMu.RUnlock()
	if gate != nil {
		gate.Store(false)
	}
	r := s.currentRuntime(id)
	if r != nil {
		_ = r.Close(ctx)
	}
	err = scope.StopExplicit(ctx)
	for errors.Is(err, managedexec.ErrManualStillRunning) {
		select {
		case <-ctx.Done():
			return errors.New("会话运行环境未正常退出，记录已保留；请在设备详情核对后重试")
		case <-time.After(20 * time.Millisecond):
			err = scope.ObserveExplicitStop()
		}
	}
	if err != nil {
		return err
	}
	if r != nil && !r.Stopped() {
		return errors.New("会话运行环境尚未确认退出，记录已保留")
	}
	return nil
}

func (s *Server) cleanArchiveGarbage() error {
	items, err := s.store.ArchiveGarbage()
	if err != nil {
		return err
	}
	for _, item := range items {
		err = errors.Join(err, s.cleanArchiveGarbageItem(item))
	}
	return err
}

func (s *Server) cleanArchiveGarbageItem(item store.ArchiveGarbage) error {
	var err error
	switch {
	case item.SessionID != "":
		scope, e := s.executions.Existing(item.SessionID)
		if e != nil {
			return e
		}
		if scope != nil {
			manual, e := scope.ManualStopPending()
			if e != nil {
				return e
			}
			if manual {
				// A forced preparation already persisted this generation's
				// operation, captured identities and force grant. The committed
				// tombstone completes acceptance if the worker died between the
				// database commit and ForceManualStop. Never accept normal-only
				// preparation or invent a new shutdown scope during recovery.
				if scope.ForceAllowed() {
					deleted, e := s.store.SessionDeleted(item.SessionID)
					if e != nil {
						return e
					}
					if deleted {
						op, e := scope.ManualStopOperation()
						if e != nil {
							return e
						}
						if e = scope.AcceptManualStop(op); e != nil {
							return e
						}
					}
				}
				ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
				e = scope.Stop(ctx)
				cancel()
				if e != nil {
					return e
				}
			}
			if e = scope.VerifyStopped(); e != nil {
				return e
			}
		}
		if r := s.currentRuntime(item.SessionID); r != nil && !r.Stopped() {
			ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
			e := r.Close(ctx)
			cancel()
			if e != nil || !r.Stopped() {
				return errors.New("已删除会话的运行环境仍在退出，稍后重试清理")
			}
		}
		if scope != nil {
			if e := scope.ForgetStopped(); e != nil {
				return e
			}
		}
		s.runtimeMu.Lock()
		delete(s.runtimes, item.SessionID)
		delete(s.runtimeCatalogs, item.SessionID)
		delete(s.runtimeEvents, item.SessionID)
		s.runtimeMu.Unlock()
		s.mu.Lock()
		delete(s.bound, item.SessionID)
		s.mu.Unlock()
		root, e := os.OpenRoot(s.executions.Directory)
		if e != nil {
			return e
		}
		err = root.RemoveAll(item.SessionID)
		root.Close()
	case item.AttachmentID != "":
		shared, e := s.store.ArchiveResourceReferenced(item.WorkspaceID, item.AttachmentID)
		if e != nil {
			return e
		}
		if !shared {
			shared, e = s.attachments.ArchiveResourceReferenced(item.WorkspaceID, item.AttachmentID)
			if e != nil {
				return e
			}
		}
		if !shared {
			dependencies, e := s.attachments.ArchiveDependencies(item.WorkspaceID, item.AttachmentID)
			if e != nil {
				return e
			}
			// Persist children before removing their only typed dependency edge.
			if e = s.store.ScheduleArchiveResources(item.WorkspaceID, dependencies); e != nil {
				return e
			}
			err = s.attachments.DeleteArchived(item.WorkspaceID, item.AttachmentID)
		}
	default:
		err = s.attachments.DeleteArchivedWorkspace(item.WorkspaceID)
	}
	if err != nil {
		return err
	}
	return s.store.FinishArchiveGarbage(item)
}
