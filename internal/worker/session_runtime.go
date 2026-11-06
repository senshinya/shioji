package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness/codex"
)

// All callers that create/replace a session runtime hold dispatchMu. The catalog
// adapter never starts/resumes an execution thread.
func (s *Server) sessionRuntime(ctx context.Context, id string) (*codex.Runtime, error) {
	if r := s.currentRuntime(id); r != nil && !r.Stopped() {
		s.runtimeMu.RLock()
		executor := s.executors[id]
		s.runtimeMu.RUnlock()
		if scope, err := s.executions.Existing(id); err == nil && scope != nil && scope.Admitting() && executor != nil && !executor.Stopped() {
			return r, nil
		}
	}
	dir := s.sessionRuntimeDirectory(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := s.cleanSessionRuntime(ctx, id); err != nil {
		return nil, err
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
	executor, err := codex.StartExecutor(ctx, codex.ExecutorOptions{Executable: s.config.CodexExecutable, Home: s.config.CodexHome, Scope: scope, OnLost: func() error {
		if err := s.guardManagedExecution(id); err != nil {
			s.cancel()
			return err
		}
		s.wakeRecovery()
		return nil
	}})
	if err != nil {
		cleanup := scope.Stop(ctx)
		return nil, errors.Join(err, cleanup)
	}
	s.runtimeMu.Lock()
	s.executors[id] = executor
	s.runtimeMu.Unlock()
	r, err := codex.Start(ctx, codex.Options{Executable: s.config.CodexExecutable, Home: s.config.CodexHome, Scope: scope, Executor: executor, OnEvent: s.onEvent, OnStart: func(pid int) error {
		return recordSessionProcess(dir, id, pid)
	}})
	if err != nil {
		return nil, errors.Join(err, s.cleanManagedExecution(ctx, id))
	}
	s.runtimeMu.Lock()
	s.runtimes[id] = r
	s.runtimeMu.Unlock()
	s.mu.Lock()
	delete(s.bound, id)
	s.mu.Unlock()
	s.watchRuntime(r)
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
	d.Session.Error = cause.Error()
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
		s.cancel()
		return
	}
	if _, err = s.store.PauseQueue(op.SessionID, cause.Error()); err != nil {
		s.cancel()
		return
	}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + op.ID, Kind: "user", Text: in.Text, Attachments: in.Attachments, Status: "failed", Final: true}}); err != nil {
		s.cancel()
	}
}

// Restart only our own session roots, never the legacy shared runtime or another
// worker's processes. Unknown launch records intentionally require attention.
func (s *Server) cleanSessionRuntime(ctx context.Context, id string) error {
	if scope, err := s.executions.Existing(id); err != nil {
		return err
	} else if scope != nil {
		return s.cleanManagedExecution(ctx, id)
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
