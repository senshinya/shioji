package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
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
		}
	}
}

func (s *Server) cleanupArchives(now time.Time) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.ctx.Err() != nil || s.updating.Load() || s.backgroundStopping.Load() {
		return nil
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
	var sessions []domain.Session
	if method == "workspace.delete" {
		if in.WorkspaceID == "" || in.SessionID != "" {
			return zero, errors.New("无效的工作区删除请求")
		}
		w, err := s.store.Workspace(in.WorkspaceID)
		if err != nil {
			return zero, err
		}
		if !w.Archived || w.Revision != in.Revision {
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
		if d.Session.Revision != in.Revision || !d.Session.Archived && !w.Archived {
			return zero, errors.New("会话已改变或未归档，请重新核对")
		}
		sessions = []domain.Session{d.Session}
	}
	// Validate every member before retiring any idle runtime in a workspace.
	for _, session := range sessions {
		state, err := s.store.RuntimeIdle(session.ID)
		if err != nil {
			return zero, err
		}
		if !state.Idle {
			return zero, errors.New("仍有运行中或待核对的任务，请处理后再删除")
		}
	}
	var undo []func() error
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
			reopen, err := scope.FenceIfIdle()
			if err != nil {
				return zero, err
			}
			undo = append(undo, reopen)
		}
	}
	for _, session := range sessions {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		err := s.cleanManagedExecution(ctx, session.ID)
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

func (s *Server) cleanArchiveGarbage() error {
	items, err := s.store.ArchiveGarbage()
	if err != nil {
		return err
	}
	for _, item := range items {
		switch {
		case item.SessionID != "":
			scope, e := s.executions.Existing(item.SessionID)
			if e != nil {
				return e
			}
			if scope != nil {
				if e = scope.VerifyStopped(); e != nil {
					return e
				}
			}
			root, e := os.OpenRoot(s.executions.Directory)
			if e != nil {
				return e
			}
			err = root.RemoveAll(item.SessionID)
			root.Close()
		case item.AttachmentID != "":
			shared, e := s.store.ArchiveResourceReferenced(item.AttachmentID)
			if e != nil {
				return e
			}
			if !shared {
				err = s.attachments.DeleteArchived(item.WorkspaceID, item.AttachmentID)
			}
		default:
			err = s.attachments.DeleteArchivedWorkspace(item.WorkspaceID)
		}
		if err != nil {
			return err
		}
		if err = s.store.FinishArchiveGarbage(item); err != nil {
			return err
		}
	}
	return nil
}
