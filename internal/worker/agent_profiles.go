package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

const agentProfilesFile = "agent-profiles.json"

var agentProfileID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (s *Server) agentProfile(id string) (domain.AgentProfile, bool) {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	profile, ok := s.agentProfiles[id]
	return profile, ok
}

func (s *Server) loadAgentProfiles() error {
	s.agentProfiles = map[string]domain.AgentProfile{}
	data, err := os.ReadFile(filepath.Join(s.config.Directory, agentProfilesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) > 256<<10 {
		return errors.New("Agent 配置过大")
	}
	var profiles []domain.AgentProfile
	if err = json.Unmarshal(data, &profiles); err != nil {
		return err
	}
	for _, profile := range profiles {
		adapter, err := s.prepareAgentProfile(profile)
		if err != nil {
			return err
		}
		s.registerAgentProfile(profile, adapter)
		if err = s.checkAgentProfileHome(profile.ID, false); err != nil {
			s.runtimeSettingErrors[profile.ID] = err
			s.catalogErrors[profile.ID] = err
			s.catalogStates[profile.ID] = catalogState{phase: domain.RuntimeUnavailable}
		}
	}
	return nil
}

func (s *Server) prepareAgentProfile(in domain.AgentProfile) (harness.Adapter, error) {
	if !agentProfileID.MatchString(in.ID) || in.Name == "" || len(in.Name) > 256 || strings.ContainsFunc(in.Name, unicode.IsControl) {
		return nil, errors.New("请输入有效的 Profile 名称")
	}
	if !domain.ValidProfileIcon(in.Icon) || in.Icon != "" && !domain.IsCustomAgent(in.HarnessID) {
		return nil, errors.New("此 Profile 的图标无效")
	}
	if s.adapter(in.ID) != nil {
		return nil, errors.New("Profile 标识已被使用")
	}
	provider := s.profileProviders[in.HarnessID]
	if provider == nil || !slices.Contains(provider.ProfileModes(), in.Mode) {
		return nil, errors.New("此 Agent 不支持所选登录方式")
	}
	if in.Executable != "" {
		installation, ok := provider.(harness.InstallationProvider)
		if !ok {
			return nil, harness.Unsupported("profile-executable")
		}
		configured, err := installation.WithExecutable(in.Executable)
		if err != nil {
			return nil, err
		}
		provider, ok = configured.(harness.AgentProfileProvider)
		if !ok {
			return nil, harness.Unsupported("agent-profile")
		}
	}
	return provider.WithAgentProfile(in.ID, in.Name, s.agentProfileHome(in.ID), in.Mode)
}

func (s *Server) agentProfileHome(id string) string {
	return filepath.Join(s.config.Directory, "agent-profiles", id)
}

func (s *Server) checkAgentProfileHome(id string, create bool) error {
	directory := s.agentProfileHome(id)
	for _, path := range []string{filepath.Dir(directory), directory} {
		if create {
			if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return errors.New("设备上的 Profile 目录不可用")
		}
	}
	return nil
}

func (s *Server) registerAgentProfile(profile domain.AgentProfile, adapter harness.Adapter) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	s.agentProfiles[profile.ID] = profile
	s.adapters[profile.ID] = adapter
	s.adapterOrder = append(s.adapterOrder, profile.ID)
	s.catalogGates[profile.ID] = &sync.RWMutex{}
}

func (s *Server) writeAgentProfile(in domain.AgentProfile) error {
	s.runtimeMu.RLock()
	saved := make([]domain.AgentProfile, 0, len(s.agentProfiles)+1)
	for _, id := range s.adapterOrder {
		if profile, ok := s.agentProfiles[id]; ok {
			if profile.ID == in.ID {
				profile = in
			}
			saved = append(saved, profile)
		}
	}
	if _, exists := s.agentProfiles[in.ID]; !exists {
		saved = append(saved, in)
	}
	s.runtimeMu.RUnlock()
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if len(data) > 256<<10 {
		return errors.New("Agent 配置过大")
	}
	return writeDeviceFile(s.config.Directory, agentProfilesFile, data)
}

func (s *Server) createAgentProfile(in domain.AgentProfile) (protocol.AgentSetupResult, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Disabled, in.Authorized, in.Executable = false, false, ""
	s.dispatchMu.Lock()
	if err := s.updateAdmission(); err != nil {
		s.dispatchMu.Unlock()
		return protocol.AgentSetupResult{}, err
	}
	if existing, ok := s.agentProfile(in.ID); ok {
		s.dispatchMu.Unlock()
		existing.Disabled, existing.Authorized, existing.Executable = false, false, ""
		if existing != in {
			return protocol.AgentSetupResult{}, errors.New("Profile 标识已被使用")
		}
		return s.agentSetupResult(), nil
	}
	in.Executable = s.runtimeSettings[in.HarnessID]
	adapter, err := s.prepareAgentProfile(in)
	if err == nil {
		err = s.checkAgentProfileHome(in.ID, true)
	}
	if err == nil {
		err = s.writeAgentProfile(in)
	}
	if err != nil {
		s.dispatchMu.Unlock()
		return protocol.AgentSetupResult{}, err
	}
	s.registerAgentProfile(in, adapter)
	s.dispatchMu.Unlock()
	// Creating a profile succeeds even while its native runtime awaits sign-in.
	_, _ = s.recheckRuntime(in.ID)
	s.runtimeMu.RLock()
	_, discovered := s.installations[in.ID]
	s.runtimeMu.RUnlock()
	if !discovered {
		s.refreshInstallation(in.ID, adapter)
	}
	return s.agentSetupResult(), nil
}

func (s *Server) changeProfileIcon(in protocol.ProfileIconInput) (protocol.AgentSetupResult, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.updateAdmission(); err != nil {
		return protocol.AgentSetupResult{}, err
	}
	profile, ok := s.agentProfile(in.ID)
	if !ok || !domain.IsCustomAgent(profile.HarnessID) || !domain.ValidProfileIcon(in.Icon) {
		return protocol.AgentSetupResult{}, errors.New("此 Profile 的图标无效")
	}
	if profile.Icon != in.Previous && profile.Icon != in.Icon {
		return protocol.AgentSetupResult{}, errors.New("图标已被其他客户端修改，请重新读取")
	}
	if profile.Icon != in.Icon {
		profile.Icon = in.Icon
		if err := s.writeAgentProfile(profile); err != nil {
			return protocol.AgentSetupResult{}, err
		}
		s.runtimeMu.Lock()
		s.agentProfiles[in.ID] = profile
		s.runtimeMu.Unlock()
	}
	return s.agentSetupResult(), nil
}

// Publish a binding only after the adapter confirms the native authorization.
func (s *Server) bindAgentProfile(id string) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	profile, managed := s.agentProfile(id)
	if !managed || profile.Authorized {
		return nil
	}
	profile.Authorized = true
	if err := s.writeAgentProfile(profile); err != nil {
		return err
	}
	s.runtimeMu.Lock()
	s.agentProfiles[id] = profile
	s.runtimeMu.Unlock()
	return nil
}

// Caller holds dispatchMu. Existing live sessions keep their owned runtime.
func (s *Server) setAgentProfileEnabled(profile domain.AgentProfile, enabled bool) (protocol.AgentSetupResult, error) {
	if profile.Disabled == enabled {
		profile.Disabled = !enabled
		if err := s.updateAdmission(); err != nil {
			return protocol.AgentSetupResult{}, err
		}
		if err := s.writeAgentProfile(profile); err != nil {
			return protocol.AgentSetupResult{}, err
		}
		s.runtimeMu.Lock()
		s.agentProfiles[profile.ID] = profile
		s.runtimeMu.Unlock()
		if enabled {
			s.dispatchMu.Unlock()
			_, _ = s.recheckRuntime(profile.ID)
			s.dispatchMu.Lock()
		}
		s.wakeQueue()
		s.wakeRecovery()
	}
	return s.agentSetupResult(), nil
}

func applyProfileBinding(info *domain.HarnessInfo) {
	if info.Profile == nil || !info.Profile.Authorized || info.Installation == nil {
		return
	}
	info.Installation.SignInMethods = nil
	info.Installation.SignInCommand = nil
	info.Installation.APISetup = false
	info.Installation.Guide = "此 Profile 已绑定账号或服务。请为新的账号或服务创建 Profile"
}
