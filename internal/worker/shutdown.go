package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/managedexec"
	"sync"
	"time"
)

func (s *Server) shutdown() error {
	deadline := time.Now().Add(7 * time.Second)
	work, cancel := context.WithDeadline(context.Background(), deadline.Add(-time.Second))
	defer cancel()
	s.backgroundStopping.Store(true)
	s.store.SuspendCompletionPublication()
	s.cancel()
	s.listener.Close()
	s.mu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.mu.Unlock()
	var result error
	var resultMu sync.Mutex
	var release sync.WaitGroup
	add := func(run func() error) {
		release.Add(1)
		go func() {
			defer release.Done()
			err := run()
			resultMu.Lock()
			result = errors.Join(result, err)
			resultMu.Unlock()
		}()
	}
	add(func() error { s.closeToolAuthorizations(); return nil })
	add(func() error { s.closeSignIns(); return nil })
	add(func() error { return s.retireAuxiliaryExecutions(work) })
	add(func() error { return s.retireRuntimesForShutdown(work) })
	add(func() error { s.wg.Wait(); return nil })
	if s.health != nil {
		add(s.health.Close)
	}
	done := make(chan struct{})
	go func() { release.Wait(); close(done) }()
	select {
	case <-done:
		resultMu.Lock()
		failure := result
		resultMu.Unlock()
		failure = errors.Join(failure, s.store.RecordShutdownUnknown(work))
		failure = errors.Join(failure, s.store.Close())
		return errors.Join(failure, s.owner.Close())
	case <-work.Done():
		record, stop := context.WithDeadline(context.Background(), deadline)
		defer stop()
		failure := s.store.RecordShutdownUnknown(record)
		// In-process callers retain the writer while producers are unsettled.
		// The CLI exits on this error; the supervisor requires an explicit drain.
		return errors.Join(work.Err(), failure)
	}
}

func (s *Server) retireAuxiliaryExecutions(ctx context.Context) error {
	directory := filepath.Join(s.config.Directory, "title-executions")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	manager, err := managedexec.New(directory, s.config.LauncherExecutable)
	if err != nil {
		return err
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	var mu sync.Mutex
	var result error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			scope, e := manager.Existing(id)
			if e == nil && scope != nil {
				e = scope.Terminate(work, true)
			}
			mu.Lock()
			result = errors.Join(result, e)
			mu.Unlock()
		}(entry.Name())
	}
	wait.Wait()
	return result
}

// Capture and fence under admission; callbacks and native cleanup settle after
// releasing it, so their last messages can finish within the common deadline.
func (s *Server) retireRuntimesForShutdown(ctx context.Context) error {
	s.dispatchMu.Lock()
	ids := map[string]bool{}
	s.runtimeMu.RLock()
	for id := range s.runtimes {
		ids[id] = true
	}
	s.runtimeMu.RUnlock()
	entries, err := os.ReadDir(s.executions.Directory)
	if err != nil {
		s.dispatchMu.Unlock()
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			ids[entry.Name()] = true
		}
	}
	var tasks []func() error
	var result error
	for id := range ids {
		op, err := s.beginRuntimeRetirement(ctx, id, "shutdown", true)
		result = errors.Join(result, err)
		if op != nil {
			tasks = append(tasks, func() error { return waitRuntimeRetirement(ctx, op) })
		}
	}
	for _, id := range s.adapterOrder {
		if r := s.catalogRuntime(id); r != nil {
			tasks = append(tasks, func() error {
				err := r.Close(ctx)
				if err == nil {
					os.Remove(filepath.Join(s.catalogDirectory(id), "runtime-process.json"))
				}
				return err
			})
		}
	}
	pending, err := s.executions.Detached()
	result = errors.Join(result, err)
	for _, scope := range pending {
		s.retirementMu.Lock()
		retired := s.detachedRuntimes[scope.Generation]
		if retired == nil {
			retired = &detachedRuntime{scope: scope}
		}
		attempt := *retired
		s.retirementMu.Unlock()
		tasks = append(tasks, func() error {
			err := closeDetachedRuntime(ctx, &attempt)
			if err == nil {
				err = scope.ResolveDetached()
			}
			return err
		})
	}
	s.dispatchMu.Unlock()
	var group sync.WaitGroup
	var mu sync.Mutex
	for _, task := range tasks {
		group.Add(1)
		go func() { defer group.Done(); err := task(); mu.Lock(); result = errors.Join(result, err); mu.Unlock() }()
	}
	group.Wait()
	return result
}
