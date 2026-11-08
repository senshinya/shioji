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
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			e = scope.Stop(ctx)
			cancel()
			if e != nil {
				log.Printf("automatic title scope %s needs cleanup: %v", entry.Name(), e)
			}
		}
	}
	for s.ctx.Err() == nil {
		s.maintenance.RLock()
		if s.updating.Load() || s.backgroundStopping.Load() {
			s.maintenance.RUnlock()
			return
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
			log.Printf("automatic title save failed: %v", err)
			return
		}
	}
}

func (s *Server) generateTitle(manager *managedexec.Manager, job store.TitleJob) string {
	current, err := s.store.Session(job.Session.ID, 0, 1)
	if err != nil || current.Session.TitleOverride || current.Session.SourceTitle != "" || current.Session.TitleRevision != job.Session.TitleRevision {
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
	scope, err := manager.Begin(job.Session.ID)
	if err != nil {
		return ""
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := scope.Stop(ctx); err != nil {
			log.Printf("automatic title scope %s needs cleanup: %v", job.Session.ID, err)
		}
		if err := manager.Reap(ctx); err != nil {
			log.Printf("automatic title process retirement failed: %v", err)
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
