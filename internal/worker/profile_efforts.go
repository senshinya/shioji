package worker

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

func (s *Server) changeProfileEffort(in protocol.ProfileEffortInput) (protocol.AgentSetupResult, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.updateAdmission(); err != nil {
		return protocol.AgentSetupResult{}, err
	}
	profile, ok := s.agentProfile(in.ID)
	if !ok || !profile.SupportsEffortOverrides() {
		return protocol.AgentSetupResult{}, errors.New("此 Profile 不支持手工配置推理档位")
	}
	if !profile.Authorized {
		return protocol.AgentSetupResult{}, errors.New("请先连接此 Profile")
	}
	if in.Model == "" || len(in.Model) > 256 || strings.ContainsAny(in.Model, "\x00\r\n") {
		return protocol.AgentSetupResult{}, errors.New("请选择有效的模型")
	}
	if in.Efforts != nil {
		if err := validateProfileEfforts(profile.HarnessID, *in.Efforts); err != nil {
			return protocol.AgentSetupResult{}, err
		}
	}
	if err := s.checkAgentProfileHome(in.ID, false); err != nil {
		return protocol.AgentSetupResult{}, err
	}
	home := s.agentProfileHome(in.ID)
	overrides, err := harness.ReadProfileEfforts(home)
	if err != nil {
		return protocol.AgentSetupResult{}, err
	}
	current, exists := overrides[in.Model]
	if profileEffortsMatch(current, exists, in.Efforts) {
		return s.agentSetupResult(), nil
	}
	if !profileEffortsMatch(current, exists, in.Previous) {
		return protocol.AgentSetupResult{}, errors.New("推理档位已被其他客户端修改，请重新读取")
	}
	if in.Efforts != nil {
		runtime := s.catalogRuntime(in.ID)
		if runtime == nil || !slices.ContainsFunc(runtime.Readiness().Models, func(m domain.Model) bool { return m.ID == in.Model }) {
			return protocol.AgentSetupResult{}, errors.New("设备未报告此模型，请刷新模型目录")
		}
		if overrides == nil {
			overrides = make(map[string][]string)
		}
		overrides[in.Model] = append([]string{}, (*in.Efforts)...)
	} else {
		delete(overrides, in.Model)
	}
	data, err := json.Marshal(overrides)
	if err != nil || len(data) > 64<<10 {
		return protocol.AgentSetupResult{}, errors.New("推理档位配置过大")
	}
	if err := writeDeviceFile(home, harness.ProfileEffortsFile, data); err != nil {
		return protocol.AgentSetupResult{}, err
	}
	s.wakeQueue()
	return s.agentSetupResult(), nil
}

func profileEffortsMatch(current []string, exists bool, expected *[]string) bool {
	if expected == nil {
		return !exists
	}
	return exists && slices.Equal(current, *expected)
}

func validateProfileEfforts(agent string, levels []string) error {
	allowed := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
	if agent == domain.AgentAnthropic {
		allowed = []string{"low", "medium", "high", "xhigh", "max"}
	}
	seen := map[string]bool{}
	for _, level := range levels {
		if seen[level] || !slices.Contains(allowed, level) {
			return errors.New("推理档位重复或无效，可选 " + strings.Join(allowed, " / "))
		}
		seen[level] = true
	}
	return nil
}
