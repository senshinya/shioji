package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"time"
)

// All callers that create/replace a session runtime hold dispatchMu. The catalog
// adapter never starts/resumes an execution thread.
func (s *Server) sessionRuntime(ctx context.Context, id string) (harness.Runtime, error) {
	// Callers settle archive retirements before acquiring their session gate.
	// Never recursively wait for that gate if an archive arrives during setup.
	pendingRetirements, err := s.store.PendingRuntimeRetirements()
	if err != nil {
		return nil, err
	}
	for _, pending := range pendingRetirements {
		if pending.SessionID == id {
			return nil, errors.New("会话正在归档，输入未发送")
		}
	}
	if err := s.sessionMaintenanceAdmission(id); err != nil {
		return nil, err
	}
	if err := s.updateAdmission(); err != nil {
		return nil, err
	}
	d, err := s.store.Session(id, 0, 1)
	if err != nil {
		return nil, err
	}
	if d.Session.Archived {
		return nil, errors.New("会话已归档，输入未发送")
	}
	workspace, err := s.store.Workspace(d.Session.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if workspace.Archived {
		return nil, errors.New("工作区已归档，输入未发送")
	}
	adapter := s.adapter(d.Session.HarnessID)
	if adapter == nil {
		return nil, errors.New("session harness unavailable")
	}
	if r := s.currentRuntime(id); r != nil && r.ExecutionReady() {
		replace := false
		if pending, ok := r.(harness.PendingRuntime); ok {
			replace = pending.PendingOperations() != 0
		}
		source, tracked := adapter.(harness.CatalogVersion)
		running, captured := r.(harness.CatalogVersion)
		if tracked && captured {
			installed, e := source.CatalogVersion()
			if e != nil {
				return nil, e
			}
			version, e := running.CatalogVersion()
			if e != nil {
				return nil, e
			}
			replace = replace || installed != version
		}
		if !replace {
			return r, nil
		}
		busy, e := s.store.HasActiveWork(id)
		if e != nil {
			return nil, e
		}
		if busy {
			return r, nil
		}
		// A new accepted turn may adopt the new installation, but live turns and
		// pending forms retain the process that owns their protocol lifecycle.
		for _, turn := range d.Turns {
			if turn.Status.Active() {
				return r, nil
			}
		}
		for _, request := range d.Requests {
			if request.State.AwaitingResolution() {
				return r, nil
			}
		}
	}

	if err := s.runtimeSettingError(d.Session.HarnessID); err != nil {
		return nil, err
	}
	dir := s.sessionRuntimeDirectory(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := s.cleanSessionRuntime(ctx, id); err != nil {
		if detach := s.detachRuntimeInstance(id, err, false); detach != nil {
			return nil, errors.Join(err, detach)
		}
	}
	// Close drains callbacks already admitted before retirement. An unexpected
	// loss during that drain must be reconciled before a new generation exists.
	latest, err := s.store.Session(id, 0, 1)
	if err != nil {
		return nil, err
	}
	if latest.Session.Archived {
		return nil, errors.New("会话已归档，输入未发送")
	}
	workspace, err = s.store.Workspace(latest.Session.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if workspace.Archived {
		return nil, errors.New("工作区已归档，输入未发送")
	}
	if latest.Session.ExecutionGuard != "" {
		return nil, errors.New("执行状态发生变化，正在重新核对；本次输入未发送")
	}
	if err := recordSessionProcess(dir, id, 0); err != nil {
		return nil, err
	}
	scope, err := s.executions.Begin(id)
	if err != nil {
		return nil, err
	}
	if err = scope.Bind("worker", os.Getpid()); err != nil {
		return nil, err
	}
	accepting := &runtimeEventGate{done: make(chan struct{})}
	accepting.Store(true)
	s.runtimeMu.Lock()
	s.runtimeEvents[id] = accepting
	s.runtimeCatalogs[id] = s.catalogs[d.Session.HarnessID]
	if d.Session.HarnessID == s.config.DefaultHarnessID {
		s.runtimeCatalogs[id] = s.runtime
	}
	s.runtimeMu.Unlock()
	startCtx, startCancel := context.WithCancel(ctx)
	pending := &runtimeStart{cancel: startCancel, gate: accepting, scope: scope}
	s.runtimeMu.Lock()
	if s.runtimeStarts == nil {
		s.runtimeStarts = map[string]*runtimeStart{}
	}
	s.runtimeStarts[id] = pending
	s.runtimeMu.Unlock()
	s.dispatchMu.Unlock()
	started := make(chan runtimeStartResult, 1)
	go func() {
		r, err := adapter.Start(startCtx, harness.StartOptions{Scope: scope, ImportResource: func(image harness.OutputResource) (domain.Attachment, error) {
			return s.importOutputResource(d.Session.HarnessID, id, image)
		}, OnMessage: func(ctx context.Context, input harness.IncomingMessage) error {
			if !accepting.Load() {
				return errors.New("旧运行环境已退休，消息未接纳")
			}
			return s.receiveHarnessMessage(ctx, d.Session.HarnessID, id, scope.Generation, input)
		}, OnEvent: func(e domain.Event) error {
			accepting.mu.RLock()
			defer accepting.mu.RUnlock()
			if !accepting.Load() {
				return nil
			}
			return s.onHarnessEvent(d.Session.HarnessID, id, e)
		}, OnStart: func(pid int) error {
			accepting.mu.RLock()
			defer accepting.mu.RUnlock()
			if !accepting.Load() {
				return errRuntimeStartCancelled
			}
			return recordSessionProcess(dir, id, pid)
		}, OnLost: func() error {
			accepting.mu.RLock()
			defer accepting.mu.RUnlock()
			if !accepting.Load() {
				return nil
			}
			if s.runtimeRetiring(id) {
				return s.store.InterruptCompletion(id)
			}
			if err := s.store.InterruptCompletion(id); err != nil {
				s.cancel()
				return err
			}
			if err := s.guardManagedExecution(id); err != nil {
				s.cancel()
				return err
			}
			s.wakeRecovery()
			return nil
		}})
		started <- runtimeStartResult{runtime: r, err: err}
	}()
	var r harness.Runtime
	creationPending := false
	select {
	case result := <-started:
		r, err = result.runtime, result.err
	case <-startCtx.Done():
		err, creationPending = startCtx.Err(), true
	}
	s.dispatchMu.Lock()
	s.runtimeMu.Lock()
	if s.runtimeStarts[id] == pending {
		delete(s.runtimeStarts, id)
	}
	s.runtimeMu.Unlock()
	cancelled := pending.cancelled.Load() || startCtx.Err() != nil
	startCancel()
	if cancelled {
		accepting.deactivate()
		go func() {
			if creationPending {
				late := <-started
				r = late.runtime
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = scope.Terminate(cleanup, true)
			if r != nil {
				_ = r.Close(cleanup)
			}
		}()
		if pending.cancelled.Load() {
			return nil, errRuntimeStartCancelled
		}
		return nil, err
	}
	if err != nil {
		return nil, errors.Join(err, s.cleanManagedExecutionOutsideDispatch(ctx, id))
	}

	s.runtimeMu.Lock()
	s.runtimes[id] = r
	s.runtimeMu.Unlock()
	s.mu.Lock()
	delete(s.bound, id)
	s.mu.Unlock()
	s.watchRuntime(r, id)
	return r, nil
}
func (s *Server) sessionRuntimeDirectory(id string) string {
	return filepath.Join(s.config.Directory, "runtimes", id)
}
func (s *Server) failRuntimeLaunch(op domain.Operation, in domain.Submission, cause error) {
	if scope, err := s.executions.Existing(op.SessionID); err != nil || scope != nil && scope.VerifyStopped() != nil {
		if scope != nil {
			if e := s.store.GuardExecution(op.SessionID, scope.Generation); e != nil {
				s.cancel()
				return
			}
		}
		defer s.wakeRecovery()
	}
	if err := s.store.SetOperation(op.ID, "rejected", cause.Error()); err != nil {
		s.cancel()
		return
	}
	d, err := s.store.Session(op.SessionID, 0, 1)
	if err != nil {
		s.cancel()
		return
	}
	d.Session.Status = "failed"
	if d.Session.ExecutionGuard != "" {
		d.Session.Status = "unknown"
		defer s.wakeRecovery()
	}
	d.Session.Error = cause.Error()
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
		s.cancel()
		return
	}
	if _, err = s.store.PauseQueue(op.SessionID, cause.Error()); err != nil {
		s.cancel()
		return
	}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + op.ID, Kind: "user", Text: in.Text, ReferencePositions: in.ReferencePositions, Skills: in.Skills, Attachments: in.Attachments, Status: "failed", Final: true}}); err != nil {
		s.cancel()
	}
}

// Restart only our own session roots, never the legacy shared runtime or another
// worker's processes. Unknown launch records intentionally require attention.
func (s *Server) cleanSessionRuntime(ctx context.Context, id string) error {
	if scope, err := s.executions.Existing(id); err != nil {
		return err
	} else if scope != nil {
		return s.cleanManagedExecutionOutsideDispatch(ctx, id)
	}
	dir := s.sessionRuntimeDirectory(id)
	raw, err := os.ReadFile(filepath.Join(dir, "runtime-process.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record processRecord
	if json.Unmarshal(raw, &record) != nil || record.Session != id || record.Starting || record.PID <= 1 {
		return errors.New("会话进程归属记录尚未确认")
	}
	if err = stopPreviousProcess(ctx, dir); err != nil {
		return err
	}
	// Retain the original identity as stopped-execution evidence until the next
	// accepted launch replaces it. A worker restart must not erase that proof.
	return writeProcessRecord(dir, record)
}
