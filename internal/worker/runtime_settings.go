package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"strings"
	"time"
)

func (s *Server) adapter(id string) harness.Adapter {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.adapters[id]
}
func (s *Server) loadRuntimeSettings() error {
	s.runtimeSettingErrors = map[string]error{}
	data, err := os.ReadFile(filepath.Join(s.config.Directory, "runtime-settings.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var document runtimeSettingsDocument
	if len(data) > 256<<10 {
		return errors.New("运行环境配置过大")
	}
	if err = json.Unmarshal(data, &document); err != nil {
		return err
	}
	if document.Version == 0 {
		if err = json.Unmarshal(data, &document.Paths); err != nil {
			return err
		}
	}
	s.runtimeSettings = document.Paths
	s.runtimeOptions = document.Options
	s.selectedHarnesses = document.Selected
	for id, path := range document.Paths {
		if a, ok := s.adapters[id].(harness.InstallationProvider); ok {
			next, e := a.WithExecutable(path)
			if e != nil {
				s.runtimeSettingErrors[id] = e
				s.catalogErrors[id] = e
				s.catalogStates[id] = catalogState{phase: domain.RuntimeUnavailable}
				continue
			}
			s.adapters[id] = next
		}
	}
	for id, options := range document.Options {
		if provider, ok := s.adapters[id].(harness.RuntimeOptionsProvider); ok {
			next, e := provider.WithRuntimeOptions(options)
			if e != nil {
				s.runtimeSettingErrors[id] = e
				s.catalogErrors[id] = e
				s.catalogStates[id] = catalogState{phase: domain.RuntimeUnavailable}
				continue
			}
			s.adapters[id] = next
		}
	}

	return nil
}

// A failed restore blocks only new launches of that Agent; live runtimes keep
// their accepted configuration and the device remains available for repair.
func (s *Server) runtimeSettingError(id string) error {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return s.runtimeSettingErrors[id]
}
func (s *Server) applyRuntimeOptions(id string, next harness.Adapter, options *domain.RuntimeOptions) (harness.Adapter, error) {
	if options == nil {
		if saved, ok := s.runtimeOptions[id]; ok {
			options = &saved
		}
	}
	if options == nil {
		return next, nil
	}
	provider, ok := next.(harness.RuntimeOptionsProvider)
	if !ok {
		return nil, harness.Unsupported("runtime-options")
	}
	return provider.WithRuntimeOptions(*options)
}
func (s *Server) configureRuntime(in protocol.RuntimeConfiguration) (domain.Readiness, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	path := strings.TrimSpace(in.Executable)
	if path != "" {
		if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			return domain.Readiness{}, errors.New("请输入该设备上的绝对可执行文件路径")
		}
		st, e := os.Stat(path)
		if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return domain.Readiness{}, errors.New("该设备上不存在可执行的普通文件")
		}
	}
	provider, ok := s.adapter(in.HarnessID).(harness.InstallationProvider)
	if !ok {
		return domain.Readiness{}, harness.Unsupported("configure-runtime")
	}
	gate := s.catalogGates[in.HarnessID]
	if !gate.TryLock() {
		return domain.Readiness{}, errCatalogRefreshing
	}
	defer gate.Unlock()
	next, err := provider.WithExecutable(path)
	if err != nil {
		return domain.Readiness{}, err
	}
	options := cloneRuntimeOptions(s.runtimeOptions)
	next, err = s.applyRuntimeOptions(in.HarnessID, next, in.Options)
	if err != nil {
		return domain.Readiness{}, err
	}
	if in.Options != nil {
		options[in.HarnessID] = *in.Options
	}

	saved := map[string]string{}
	for id, value := range s.runtimeSettings {
		saved[id] = value
	}
	saved[in.HarnessID] = path
	data, err := json.Marshal(runtimeSettingsDocument{Version: 1, Paths: saved, Selected: s.selectedHarnesses, Options: options})
	if err != nil {
		return domain.Readiness{}, err
	}
	if len(data) > 256<<10 {
		return domain.Readiness{}, errors.New("运行环境配置过大")
	}
	if err = writeDeviceFile(s.config.Directory, "runtime-settings.json", data); err != nil {
		return domain.Readiness{}, err
	}
	s.runtimeMu.Lock()
	s.runtimeSettings = saved
	s.runtimeOptions = options
	delete(s.runtimeSettingErrors, in.HarnessID)
	s.adapters[in.HarnessID] = next
	s.runtimeMu.Unlock()
	if err = s.markCatalogRefreshing(in.HarnessID, ""); err != nil {
		return domain.Readiness{}, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	_, _ = s.startCatalog(ctx, in.HarnessID, false, "")
	s.wakeQueue()
	s.recoveryRefresh.Add(1)
	s.wakeRecovery()
	return s.runtimeReadiness(), nil
}
func writeDeviceFile(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, ".device-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Server) recheckRuntime(id string) (domain.Readiness, error) {
	if s.activeToolAuthorization(id) {
		return domain.Readiness{}, errors.New("请先完成工具授权")
	}
	if s.adapter(id) == nil {
		return domain.Readiness{}, errors.New("Agent 不存在")
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	gate := s.catalogGates[id]
	if !gate.TryLock() {
		return domain.Readiness{}, errCatalogRefreshing
	}
	defer gate.Unlock()
	version, err := s.catalogSourceVersion(id)
	if err == nil {
		err = s.markCatalogRefreshing(id, version)
	}
	if err == nil {
		ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
		_, err = s.startCatalog(ctx, id, false, version)
		cancel()
	}
	s.wakeQueue()
	s.recoveryRefresh.Add(1)
	s.wakeRecovery()
	// Detection failures are readable status, not a successful-ready claim.
	if err != nil {
		s.runtimeMu.Lock()
		state := s.catalogStates[id]
		state.phase = domain.RuntimeUnavailable
		s.catalogStates[id] = state
		s.catalogErrors[id] = err
		s.runtimeMu.Unlock()
	}
	return s.runtimeReadiness(), nil
}

// A live session may still belong to a previous installation or credential
// catalog. Its late failure cannot invalidate a newly checked configuration.
func (s *Server) reportRuntimeSetup(sessionID string, runtime harness.Runtime, cause error) {
	var setup *harness.SetupError
	if !errors.As(cause, &setup) {
		return
	}
	d, err := s.store.Session(sessionID, 0, 1)
	if err != nil {
		return
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	current := s.catalogs[d.Session.HarnessID]
	if d.Session.HarnessID == s.config.DefaultHarnessID {
		current = s.runtime
	}
	if s.runtimes[sessionID] != runtime || s.runtimeCatalogs[sessionID] != current {
		return
	}
	state := s.catalogStates[d.Session.HarnessID]
	state.phase = domain.RuntimeUnavailable
	state.retryAt = time.Now().Add(time.Minute)
	s.catalogStates[d.Session.HarnessID] = state
	s.catalogErrors[d.Session.HarnessID] = setup
}

type runtimeSettingsDocument struct {
	Options  map[string]domain.RuntimeOptions `json:"options,omitempty"`
	Version  int                              `json:"version"`
	Paths    map[string]string                `json:"paths"`
	Selected []string                         `json:"selected"`
}

func (s *Server) agentEnabled(id string) bool {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if s.selectedHarnesses == nil {
		return true
	}
	for _, selected := range s.selectedHarnesses {
		if id == selected {
			return true
		}
	}
	return false
}
func (s *Server) configureAgents(in protocol.DeviceAgents) (protocol.AgentSetupResult, error) {
	if len(in.Agents) > len(s.adapterOrder) {
		return protocol.AgentSetupResult{}, errors.New("请至少选择一个可用 Agent")
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	candidates := map[string]harness.Adapter{}
	options := cloneRuntimeOptions(s.runtimeOptions)
	paths := map[string]string{}
	for id, path := range s.runtimeSettings {
		paths[id] = path
	}
	selected := []string{}
	for _, entry := range in.Agents {
		if _, duplicate := candidates[entry.HarnessID]; duplicate {
			return protocol.AgentSetupResult{}, errors.New("Agent 不能重复选择")
		}
		provider, ok := s.adapter(entry.HarnessID).(harness.InstallationProvider)
		if !ok {
			return protocol.AgentSetupResult{}, harness.Unsupported("configure-runtime")
		}
		path := strings.TrimSpace(entry.Executable)
		if path != "" {
			if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
				return protocol.AgentSetupResult{}, errors.New("请输入所属设备上的绝对可执行文件路径")
			}
			st, e := os.Stat(path)
			if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
				return protocol.AgentSetupResult{}, errors.New("所属设备上不存在可执行的普通文件")
			}
		}
		next, e := provider.WithExecutable(path)
		if e != nil {
			return protocol.AgentSetupResult{}, e
		}
		next, e = s.applyRuntimeOptions(entry.HarnessID, next, entry.Options)
		if e != nil {
			return protocol.AgentSetupResult{}, e
		}
		if entry.Options != nil {
			options[entry.HarnessID] = *entry.Options
		}

		candidates[entry.HarnessID] = next
		paths[entry.HarnessID] = path
		selected = append(selected, entry.HarnessID)
	}
	var unlock []func()
	defer func() {
		for i := len(unlock) - 1; i >= 0; i-- {
			unlock[i]()
		}
	}()
	for _, id := range s.adapterOrder {
		gate := s.catalogGates[id]
		if !gate.TryLock() {
			return protocol.AgentSetupResult{}, errCatalogRefreshing
		}
		unlock = append(unlock, gate.Unlock)
	}
	old := map[string]harness.Adapter{}
	oldErrors := map[string]error{}
	s.runtimeMu.Lock()
	for id, next := range candidates {
		old[id] = s.adapters[id]
		oldErrors[id] = s.runtimeSettingErrors[id]
		delete(s.runtimeSettingErrors, id)
		s.adapters[id] = next
	}
	s.runtimeMu.Unlock()
	committed := false
	defer func() {
		if committed {
			return
		}
		s.runtimeMu.Lock()
		for id, previous := range old {
			s.adapters[id] = previous
			s.runtimeSettingErrors[id] = oldErrors[id]
		}
		s.runtimeMu.Unlock()
		for id := range old {
			_ = s.markCatalogRefreshing(id, "")
			ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
			_, _ = s.startCatalog(ctx, id, false, "")
			cancel()
		}
	}()
	for _, id := range selected {
		if e := s.markCatalogRefreshing(id, ""); e != nil {
			return protocol.AgentSetupResult{}, e
		}
		ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
		_, e := s.startCatalog(ctx, id, false, "")
		cancel()
		if e != nil {
			catalog := s.runtimeProfiles(nil, true)
			return protocol.AgentSetupResult{Catalog: catalog.Harnesses, Error: e.Error()}, nil
		}
	}
	data, e := json.Marshal(runtimeSettingsDocument{Version: 1, Paths: paths, Selected: selected, Options: options})
	if e != nil {
		return protocol.AgentSetupResult{}, e
	}
	if len(data) > 256<<10 {
		return protocol.AgentSetupResult{}, errors.New("运行环境配置过大")
	}
	if e = writeDeviceFile(s.config.Directory, "runtime-settings.json", data); e != nil {
		return protocol.AgentSetupResult{}, e
	}
	s.runtimeMu.Lock()
	s.runtimeSettings = paths
	s.runtimeOptions = options
	s.selectedHarnesses = selected
	s.runtimeMu.Unlock()
	committed = true
	for _, id := range s.adapterOrder {
		if !s.agentEnabled(id) {
			if r := s.catalogRuntime(id); r != nil {
				ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
				r.Close(ctx)
				cancel()
			}
		}
	}
	s.wakeQueue()
	s.recoveryRefresh.Add(1)
	s.wakeRecovery()
	ready := s.runtimeReadiness()
	return protocol.AgentSetupResult{Ready: true, Readiness: ready, Catalog: s.runtimeProfiles(nil, true).Harnesses}, nil
}

func cloneRuntimeOptions(source map[string]domain.RuntimeOptions) map[string]domain.RuntimeOptions {
	out := make(map[string]domain.RuntimeOptions, len(source))
	for id, value := range source {
		out[id] = value
	}
	return out
}
