package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"strings"
	"sync"
	"time"
)

func (s *Server) configureAdapters() error {
	adapters := s.config.Harnesses
	if len(adapters) == 0 {
		adapters = defaultAdapters()
	}
	s.adapters = map[string]harness.Adapter{}
	for _, a := range adapters {
		d := a.Descriptor()
		if d.ID == "" || d.ID == "." || d.ID == ".." || filepath.Base(d.ID) != d.ID || d.Name == "" {
			return errors.New("invalid harness descriptor")
		}
		if _, exists := s.adapters[d.ID]; exists {
			return fmt.Errorf("duplicate harness %q", d.ID)
		}
		s.adapters[d.ID] = a
		s.adapterOrder = append(s.adapterOrder, d.ID)
	}
	if s.config.DefaultHarnessID == "" {
		s.config.DefaultHarnessID = s.adapterOrder[0]
	}
	if s.adapters[s.config.DefaultHarnessID] == nil {
		return errors.New("default harness is not configured")
	}
	if err := s.loadCatalogOwner(); err != nil {
		return err
	}
	s.catalogs = map[string]harness.Runtime{}
	s.catalogErrors = map[string]error{}
	s.catalogStates = map[string]catalogState{}
	s.catalogGates = map[string]*sync.RWMutex{}
	for _, id := range s.adapterOrder {
		s.catalogGates[id] = &sync.RWMutex{}
	}
	return s.loadRuntimeSettings()
}
func (s *Server) catalogDirectory(id string) string {
	if id == s.rootCatalogID {
		return s.config.Directory
	} // Existing ownership records stay valid.
	return filepath.Join(s.config.Directory, "catalogs", id)
}
func (s *Server) catalogRuntime(id string) harness.Runtime {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if id == s.config.DefaultHarnessID || id == "" {
		return s.runtime
	}
	return s.catalogs[id]
}
func (s *Server) inputHarness(in domain.Submission) (string, error) {
	id := in.HarnessID
	if in.SessionID != "" {
		d, err := s.store.Session(in.SessionID, 0, 1)
		if err != nil {
			return "", err
		}
		if id != "" && id != d.Session.HarnessID {
			return "", errors.New("session harness cannot change")
		}
		id = d.Session.HarnessID
		if id == "" {
			return "", errors.New("saved session has no harness identity")
		}
	} else if id == "" {
		id = s.config.DefaultHarnessID
	}
	if s.adapter(id) == nil {
		return "", fmt.Errorf("harness %q is unavailable", id)
	}
	return id, nil
}
func (s *Server) inputRuntime(in domain.Submission) (harness.Runtime, error) {
	id, err := s.inputHarness(in)
	if err != nil {
		return nil, err
	}
	if in.SessionID != "" {
		if own := s.currentRuntime(in.SessionID); own != nil && !own.Stopped() {
			return own, nil
		}
	}
	if !s.agentEnabled(id) {
		return nil, errors.New("Agent 未启用")
	}
	r := s.catalogRuntime(id)
	s.runtimeMu.RLock()
	state := s.catalogStates[id]
	s.runtimeMu.RUnlock()
	if r == nil || r.Stopped() || state.phase == domain.RuntimeRefreshing || state.phase == domain.RuntimeUnavailable || s.ctx.Err() != nil {
		return nil, errors.New("runtime unavailable")
	}
	return r, nil
}
func (s *Server) openCatalog(ctx context.Context, id string, stop bool) (harness.Runtime, error) {
	if r := s.catalogRuntime(id); r != nil && !r.Stopped() {
		return r, nil
	}
	gate := s.catalogGates[id]
	if gate == nil || !gate.TryLock() {
		return nil, errCatalogRefreshing
	}
	defer gate.Unlock()
	revision, err := s.catalogSourceVersion(id)
	if err != nil {
		return nil, err
	}
	s.runtimeMu.RLock()
	state := s.catalogStates[id]
	s.runtimeMu.RUnlock()
	if !stop && state.phase == domain.RuntimeUnavailable && state.revision == revision && time.Now().Before(state.retryAt) {
		return nil, s.catalogFailure(id)
	}
	if err := s.markCatalogRefreshing(id, revision); err != nil {
		return nil, err
	}
	return s.startCatalog(ctx, id, stop, revision)
}
func (s *Server) runtimeReadiness(sessions ...string) domain.Readiness {
	return s.runtimeProfiles(sessions, false)
}
func (s *Server) runtimeProfiles(sessions []string, allAdapters bool) domain.Readiness {
	var all []domain.HarnessInfo
	for _, id := range s.adapterOrder {
		enabled := s.agentEnabled(id)
		if !allAdapters && !enabled {
			continue
		}
		descriptor := s.adapter(id).Descriptor()
		info := domain.HarnessInfo{Configured: enabled, ID: id, Name: descriptor.Name, Icon: descriptor.Icon, State: domain.RuntimeUnavailable}
		if r := s.catalogRuntime(id); r != nil {
			ready := r.Readiness()
			info.Account = ready.Account
			info.State, info.Error, info.Models, info.Permissions, info.PermissionChoices, info.Plan, info.Capabilities = ready.State, ready.Error, ready.Models, ready.Permissions, ready.PermissionChoices, ready.Plan, ready.Capabilities
		} else {
			s.runtimeMu.RLock()
			err := s.catalogErrors[id]
			s.runtimeMu.RUnlock()
			if err != nil {
				info.Error = err.Error()
			}
		}
		s.runtimeMu.RLock()
		state := s.catalogStates[id]
		err := s.catalogErrors[id]
		s.runtimeMu.RUnlock()
		if state.phase == domain.RuntimeRefreshing || state.phase == domain.RuntimeUnavailable {
			if len(state.previous.Models) > 0 {
				info.Models, info.Permissions, info.PermissionChoices, info.Plan, info.Capabilities = state.previous.Models, state.previous.Permissions, state.previous.PermissionChoices, state.previous.Plan, state.previous.Capabilities
			}
			info.State = state.phase
			if err != nil {
				info.Error = err.Error()
			}
		}
		if provider, ok := s.adapter(id).(harness.InstallationProvider); ok {
			installation := provider.Installation()
			s.runtimeMu.RLock()
			if failure := s.runtimeSettingErrors[id]; failure != nil {
				installation.Options = s.runtimeOptions[id]
				installation.Override = s.runtimeSettings[id]
				installation.Guide = "请修正该设备上的 Agent 配置后保存：" + failure.Error()
				installation.SignInCommand = nil
			}
			s.runtimeMu.RUnlock()
			if err != nil {
				var setup *harness.SetupError
				if errors.As(err, &setup) {
					installation.Issue = setup.Issue
				} else {
					installation.Issue = domain.RuntimeUnavailableIssue
				}
			}
			info.Installation = &installation
		}
		all = append(all, info)
	}
	result := domain.Readiness{HarnessID: s.config.DefaultHarnessID, Harnesses: all}
	selected := domain.Readiness{State: domain.RuntimeUnavailable, Error: "请在设备详情启用 Agent"}
	if len(all) > 0 {
		var ok bool
		selected, ok = result.ForHarness(s.config.DefaultHarnessID)
		if !ok {
			selected, _ = result.ForHarness(all[0].ID)
		}
	}
	for _, id := range sessions {
		if r := s.currentRuntime(id); r != nil && !r.Stopped() {
			ready := r.Readiness()
			profile, ok := ready.ForHarness(ready.HarnessID)
			if !ok {
				continue
			}
			if selected.Sessions == nil {
				selected.Sessions = map[string]domain.HarnessInfo{}
			}
			d := ready.Descriptor()
			selected.Sessions[id] = domain.HarnessInfo{ID: profile.HarnessID, Name: d.Name, Icon: d.Icon, State: profile.State, Error: profile.Error, Models: profile.Models, Permissions: profile.Permissions, PermissionChoices: profile.PermissionChoices, Plan: profile.Plan, Capabilities: profile.Capabilities}
		}
	}
	return selected
}

// Adapters report agent activity, not authoritative workspace/session commands.
// Bind events to the configured harness and owned session before persistence.
func (s *Server) onHarnessEvent(harnessID, sessionID string, e domain.Event) error {
	if e.SessionID == "" {
		return nil
	}
	if sessionID != "" && e.SessionID != sessionID {
		return errors.New("harness event belongs to another session")
	}
	if sessionID == "" {
		d, err := s.store.Session(e.SessionID, 0, 1)
		if err != nil {
			return err
		}
		if d.Session.HarnessID != harnessID {
			return errors.New("harness event belongs to another harness")
		}
	}
	if e.Workspace != nil || e.Session != nil || e.Operation != nil || e.Queue != nil || e.HistoryTurnOrder != nil {
		return errors.New("harness event contains worker-owned mutation")
	}
	if e.Request != nil && e.Request.SessionID != "" && e.Request.SessionID != e.SessionID {
		return errors.New("request belongs to another session")
	}
	switch e.Type {
	case "session.title", "context.usage", "turn", "item.upsert", "request.open", "request.resolve", "plan", "plan.text", "runtime.lost":
	default:
		return errors.New("harness event cannot mutate worker-owned state")
	}
	return s.onEvent(e)
}

// The legacy root process record has one durable owner. Selecting a different
// default never reassigns that process to another adapter.
func (s *Server) loadCatalogOwner() error {
	path := filepath.Join(s.config.Directory, "catalog-owner")
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
			return errors.New("invalid catalog owner record")
		}
		s.rootCatalogID = id
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	id := s.config.DefaultHarnessID
	if _, err := os.Stat(filepath.Join(s.config.Directory, "runtime-process.json")); err == nil {
		id = legacyCatalogHarnessID()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(s.config.Directory, "catalog-owner-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.WriteString(id + "\n")
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	s.rootCatalogID = id
	return nil
}
