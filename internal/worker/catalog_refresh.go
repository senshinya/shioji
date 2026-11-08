package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

// A catalog is either ready, refreshing, or unavailable. Its last confirmed
// choices remain visible while replacement starts; they cannot accept new work.
type catalogState struct {
	phase    domain.ReadinessState
	previous domain.Readiness
	revision string
	retryAt  time.Time
}

func (s *Server) catalogSourceVersion(id string) (string, error) {
	if err := s.runtimeSettingError(id); err != nil {
		return "", err
	}
	if source, ok := s.adapter(id).(harness.CatalogVersion); ok {
		return source.CatalogVersion()
	}
	return "", nil
}
func (s *Server) catalogFailure(id string) error {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if err := s.catalogErrors[id]; err != nil {
		return err
	}
	return errors.New("运行环境暂不可用")
}

var errCatalogRefreshing = errors.New("正在刷新模型和能力列表，草稿已保留")

// The background poll claims maintenance admission before this transition.
// Recovery callers already hold dispatch; they must never acquire maintenance
// recursively or after dispatch (worker replacement takes the opposite order).
func (s *Server) markCatalogRefreshing(id, revision string) error {
	if err := s.updateAdmission(); err != nil {
		return err
	}
	previous := s.catalogRuntime(id)
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	state := s.catalogStates[id]
	if previous != nil && !previous.Stopped() {
		state.previous = previous.Readiness()
	}
	state.phase = domain.RuntimeRefreshing
	state.revision = revision
	s.catalogStates[id] = state
	s.catalogErrors[id] = errCatalogRefreshing
	return nil
}

// Caller holds the catalog's write gate and has claimed its refreshing phase.
func (s *Server) startCatalog(ctx context.Context, id string, stop bool, revision string) (next harness.Runtime, err error) {
	previous := s.catalogRuntime(id)
	attempted := false
	defer func() {
		s.runtimeMu.Lock()
		defer s.runtimeMu.Unlock()
		state := s.catalogStates[id]
		state.phase = domain.RuntimeReady
		state.retryAt = time.Time{}
		if err != nil {
			state.phase = domain.RuntimeUnavailable
			if attempted {
				state.retryAt = time.Now().Add(time.Minute)
			}
		} else {
			state.previous = next.Readiness()
		}
		s.catalogStates[id] = state
		s.catalogErrors[id] = err
	}()
	if err = s.runtimeSettingError(id); err != nil {
		return nil, err
	}
	if previous != nil {
		if err = previous.Close(ctx); err != nil {
			return nil, err
		}
		os.Remove(filepath.Join(s.catalogDirectory(id), "runtime-process.json"))
	}
	a := s.adapter(id)
	if a == nil {
		return nil, errors.New("harness unavailable")
	}
	dir := s.catalogDirectory(id)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if stop {
		if err = stopPreviousProcess(ctx, dir); err != nil {
			return nil, err
		}
	}
	if err = checkPreviousProcess(dir); err != nil {
		return nil, err
	}
	if err = recordProcess(dir, 0); err != nil {
		return nil, err
	}
	attempted = true
	next, err = a.Start(ctx, harness.StartOptions{ImportResource: func(image harness.OutputResource) (domain.Attachment, error) {
		return s.importOutputResource(id, "", image)
	}, OnEvent: func(e domain.Event) error { return s.onHarnessEvent(id, "", e) }, OnStart: func(pid int) error { return recordProcess(dir, pid) }})
	if err != nil {
		os.Remove(filepath.Join(dir, "runtime-process.json"))
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		next.Close(context.Background())
		os.Remove(filepath.Join(dir, "runtime-process.json"))
		return nil, err
	}
	s.runtimeMu.Lock()
	s.catalogs[id] = next
	if id == s.config.DefaultHarnessID {
		s.runtime = next
	}
	s.runtimeMu.Unlock()
	s.watchRuntime(next)
	return next, nil
}
func (s *Server) borrowCatalog(id string) (harness.Runtime, func(), error) {
	gate := s.catalogGates[id]
	if gate == nil || !gate.TryRLock() {
		return nil, nil, errCatalogRefreshing
	}
	r := s.catalogRuntime(id)
	s.runtimeMu.RLock()
	state := s.catalogStates[id]
	s.runtimeMu.RUnlock()
	if !s.agentEnabled(id) {
		gate.RUnlock()
		return nil, nil, errors.New("Agent 未启用")
	}
	if state.phase == domain.RuntimeRefreshing {
		gate.RUnlock()
		return nil, nil, errCatalogRefreshing
	}
	if r == nil || r.Stopped() || state.phase == domain.RuntimeUnavailable || s.ctx.Err() != nil {
		gate.RUnlock()
		return nil, nil, s.catalogFailure(id)
	}
	return r, gate.RUnlock, nil
}
func (s *Server) borrowInputRuntime(in domain.Submission) (harness.Runtime, func(), error) {
	id, err := s.inputHarness(in)
	if err != nil {
		return nil, nil, err
	}
	if in.SessionID != "" {
		if own := s.currentRuntime(in.SessionID); own != nil && !own.Stopped() {
			return own, func() {}, nil
		}
	}
	return s.borrowCatalog(id)
}
func (s *Server) wakeCatalog() {
	select {
	case s.catalogWake <- struct{}{}:
	default:
	}
}
func (s *Server) catalogLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.catalogWake:
		}
		for _, id := range s.adapterOrder {
			if !s.agentEnabled(id) {
				continue
			}
			if _, ok := s.adapter(id).(harness.CatalogVersion); !ok {
				continue
			}
			r := s.catalogRuntime(id)
			installed, err := s.catalogSourceVersion(id)
			s.runtimeMu.RLock()
			state := s.catalogStates[id]
			s.runtimeMu.RUnlock()
			if r != nil && !r.Stopped() {
				running, ok := r.(harness.CatalogVersion)
				if !ok {
					continue
				}
				version, e := running.CatalogVersion()
				if err == nil && e == nil && installed == version && state.phase == domain.RuntimeReady {
					continue
				}
			}
			if state.phase == domain.RuntimeUnavailable && state.revision == installed && time.Now().Before(state.retryAt) {
				continue
			}
			gate := s.catalogGates[id]
			if !gate.TryLock() {
				continue
			}
			if err != nil {
				s.runtimeMu.Lock()
				state.phase = domain.RuntimeUnavailable
				state.revision = installed
				state.retryAt = time.Now().Add(time.Minute)
				if r != nil && !r.Stopped() {
					state.previous = r.Readiness()
				}
				s.catalogStates[id] = state
				s.catalogErrors[id] = err
				s.runtimeMu.Unlock()
			} else {
				s.maintenance.RLock()
				err = s.markCatalogRefreshing(id, installed)
				s.maintenance.RUnlock()
				if err == nil {
					ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
					_, err = s.startCatalog(ctx, id, false, installed)
					cancel()
				}
			}
			gate.Unlock()
			if err == nil {
				s.recoveryRefresh.Add(1)
				s.wakeRecovery()
				s.wakeQueue()
			}
		}
	}
}

func validateAvailableConfiguration(runtime harness.Runtime, config domain.RunConfig) error {
	for _, model := range runtime.Readiness().Models {
		if model.ID == config.Model {
			return runtime.Validate(config)
		}
	}
	return errors.New(domain.ModelUnavailableMessage)
}
