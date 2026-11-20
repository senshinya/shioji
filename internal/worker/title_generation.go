package worker

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/store"
)

// One bounded background consumer is independent of dispatch and GUI lifetime.
// Its own process scopes cannot fence or interrupt conversation execution.
func (s *Server) titleLoop() {
	defer s.wg.Done()
	manager, err := managedexec.New(filepath.Join(s.config.Directory, "title-executions"), s.config.LauncherExecutable)
	if err != nil {
		log.Printf("automatic title queue unavailable: %v", err)
		return
	}
	s.titleMu.Lock()
	s.titleExecutions = manager
	s.titleMu.Unlock()
	entries, err := os.ReadDir(manager.Directory)
	if err != nil {
		log.Printf("automatic title recovery unavailable: %v", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		scope, e := manager.Existing(entry.Name())
		if e != nil {
			log.Printf("automatic title scope %s needs cleanup: %v", entry.Name(), e)
			continue
		}
		if scope != nil {
			s.titleMu.Lock()
			if s.titleActive == nil {
				s.titleActive = map[string]bool{}
			}
			s.titleActive[entry.Name()] = true
			s.titleMu.Unlock()
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			e = scope.Stop(ctx)
			cancel()
			s.titleMu.Lock()
			delete(s.titleActive, entry.Name())
			s.titleMu.Unlock()
			if e != nil {
				log.Printf("automatic title scope %s needs cleanup: %v", entry.Name(), e)
			}
		}
	}
	for s.ctx.Err() == nil {
		s.maintenance.RLock()
		if s.updating.Load() || s.backgroundStopping.Load() {
			s.maintenance.RUnlock()
			select {
			case <-s.ctx.Done():
				return
			case <-s.titleWake:
				continue
			}
		}
		job, err := s.store.ClaimTitleGeneration()
		s.maintenance.RUnlock()
		if errors.Is(err, sql.ErrNoRows) {
			select {
			case <-s.ctx.Done():
				return
			case <-s.titleWake:
				continue
			}
		}
		if err != nil {
			log.Printf("automatic title claim failed: %v", err)
			return
		}
		title := s.generateTitle(manager, job)
		if err = s.store.FinishTitleGeneration(job, title); err != nil {
			// An archive may remove the job while its independent generator runs.
			// Its late result must not retire the queue for other sessions.
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			log.Printf("automatic title save failed: %v", err)
			return
		}
	}
}

func (s *Server) generateTitle(manager *managedexec.Manager, job store.TitleJob) string {
	current, err := s.store.SessionRecord(job.Session.ID)
	if err != nil || current.TitleOverride || current.SourceTitle != "" || current.TitleRevision != job.Session.TitleRevision {
		return ""
	}
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()
	var runtime harness.Runtime
	var release func()
	for {
		runtime, release, err = s.borrowCatalog(job.Session.HarnessID)
		if err == nil {
			break
		}
		s.runtimeMu.RLock()
		refreshing := s.catalogStates[job.Session.HarnessID].phase == domain.RuntimeRefreshing
		s.runtimeMu.RUnlock()
		if !refreshing {
			return ""
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ""
		case <-timer.C:
		}
	}
	defer release()
	generator, ok := runtime.(harness.TitleGenerator)
	if !ok || job.Session.TitleOverride || job.Session.SourceTitle != "" {
		return ""
	}
	prompt := job.Prompt
	if prompt == "" {
		prompt = "用户提交了附件或引用：" + job.Session.Title
	}
	s.titleMu.Lock()
	deleted, e := s.store.SessionDeleted(job.Session.ID)
	if e != nil || deleted {
		s.titleMu.Unlock()
		return ""
	}
	scope, err := manager.Begin(job.Session.ID)
	if err != nil {
		s.titleMu.Unlock()
		return ""
	}
	if s.titleExecutions == nil {
		s.titleExecutions = manager
	}
	if s.titleActive == nil {
		s.titleActive = map[string]bool{}
	}
	s.titleActive[job.Session.ID] = true
	s.titleMu.Unlock()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := scope.Stop(ctx); err != nil {
			log.Printf("automatic title scope %s needs cleanup: %v", job.Session.ID, err)
		}
		if err := manager.Reap(ctx); err != nil {
			log.Printf("automatic title process retirement failed: %v", err)
		}
		s.titleMu.Lock()
		delete(s.titleActive, job.Session.ID)
		s.titleMu.Unlock()
		if deleted, _ := s.store.SessionDeleted(job.Session.ID); deleted {
			s.wakeArchiveRetirement()
		}
	}()
	if err = scope.Bind("worker", os.Getpid()); err != nil {
		return ""
	}
	title, err := generator.GenerateTitle(ctx, harness.TitleInput{Prompt: prompt, Scope: scope})
	if err != nil {
		return ""
	}
	return title
}

// Caller holds dispatchMu. A title owns a separate short-lived scope; deleting
// its conversation never removes that scope while a producer can still write.
func (s *Server) cleanDeletedTitle(id string) error {
	s.titleMu.Lock()
	if s.titleActive[id] {
		s.titleMu.Unlock()
		return errors.New("命名短任务仍在退出，保留登记记录后重试")
	}
	manager := s.titleExecutions
	s.titleMu.Unlock()
	if manager == nil {
		if _, err := os.Stat(filepath.Join(s.config.Directory, "title-executions", id)); os.IsNotExist(err) {
			return nil
		}
		return errors.New("命名任务正在恢复，稍后重试清理")
	}
	scope, err := manager.Existing(id)
	if err != nil {
		return err
	}
	if scope != nil {
		ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
		s.dispatchMu.Unlock()
		err = scope.Stop(ctx)
		s.dispatchMu.Lock()
		cancel()
		if err != nil {
			return err
		}
		if err = scope.ForgetStopped(); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(manager.Directory)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(id)
}
