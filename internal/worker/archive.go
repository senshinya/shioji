package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/store"
)

func (s *Server) archiveLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
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
	result := s.cleanRuntimeRetirements("")
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
		// A restore or revision change cancels this expired candidate. Physical
		// cleanup of an accepted tombstone proceeds independently.
		if _, err = s.deleteArchive(method, in); err != nil {
			continue
		}
	}
	return errors.Join(result, s.cleanArchiveGarbage(), s.cleanOwnedWorktrees(now))
}

// Caller holds dispatchMu, shared with submissions, checkout and restoration.
func (s *Server) deleteArchive(method string, in protocol.ArchiveDeleteInput) (domain.ArchiveDeletion, error) {
	var zero domain.ArchiveDeletion
	if err := s.discoverOwnedWorktrees(in.SessionID, in.WorkspaceID); err != nil {
		return zero, err
	}
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
	// Deletion is accepted before physical cleanup. The durable tombstone owns
	// every retry; paused input and a failed shutdown cannot veto this intent.
	in.CloseRuntime = true
	if in.OperationID == "" {
		in.OperationID = fmt.Sprintf("delete:%s:%s:%d", in.WorkspaceID, in.SessionID, in.Revision)
	}
	return s.commitArchiveDeletion(method, in, sessions)
}

// Permanent deletion removes the visible archive first; scoped process records
// and the durable outbox retain ownership until cleanup is physically proven.
func (s *Server) commitArchiveDeletion(method string, in protocol.ArchiveDeleteInput, sessions []domain.Session) (domain.ArchiveDeletion, error) {
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
			gate.deactivate()
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
	// The receipt follows the tombstone, never native shutdown. The archive
	// consumer owns cleanup even if this RPC disconnects or the worker restarts.
	s.wakeArchiveRetirement()
	if warning {
		result.Warning = "归档记录已删除；部分资源尚未完成回收，后台将继续清理。"
	}
	return result, nil
}

// Caller holds dispatchMu; producer waits inside each item release it.
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
		deleted, e := s.store.SessionDeleted(item.SessionID)
		if e != nil || !deleted {
			return errors.Join(e, errors.New("缺少会话删除记录，保留资源"))
		}
		release, e := s.beginSessionTransition(s.ctx, item.SessionID)
		if e != nil {
			return e
		}
		defer release()
		retired, e := s.executions.Detached()
		if e != nil {
			return e
		}
		for _, old := range retired {
			if old.Session == item.SessionID {
				return errors.New("旧代次仍在回收，保留登记记录后重试清理")
			}
		}
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
				s.dispatchMu.Unlock()
				e = scope.Stop(ctx)
				s.dispatchMu.Lock()
				cancel()
				if e != nil {
					return e
				}
			}
			if !manual {
				// A prior preparation may have failed before it recorded a manual
				// operation. Retry retirement of this registered generation using
				// the durable tombstone, instead of only observing it forever.
				ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
				s.dispatchMu.Unlock()
				e = scope.Terminate(ctx, true)
				s.dispatchMu.Lock()
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
			s.dispatchMu.Unlock()
			e := r.Close(ctx)
			s.dispatchMu.Lock()
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
		delete(s.idleRuntimes, item.SessionID)
		s.mu.Lock()
		delete(s.bound, item.SessionID)
		s.mu.Unlock()
		root, e := os.OpenRoot(s.executions.Directory)
		if e != nil {
			return e
		}
		err = root.RemoveAll(item.SessionID)
		root.Close()
		if err == nil {
			err = os.RemoveAll(s.sessionRuntimeDirectory(item.SessionID))
		}
		if err == nil {
			err = s.cleanDeletedTitle(item.SessionID)
		}
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
