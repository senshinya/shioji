package app

import (
	"errors"
	"shioji/internal/domain"
	"strings"
)

func (m *Model) HarnessID() string {
	if m.SessionID != "" {
		if d := m.Details[m.SessionID]; d.Session.HarnessID != "" {
			return d.Session.HarnessID
		}
		for _, s := range m.Snapshot.Sessions {
			if s.ID == m.SessionID {
				return s.HarnessID
			}
		}
	}
	if selected := m.HomeHarnesses[m.WorkspaceID]; selected != "" {
		return selected
	}
	return m.Readiness.HarnessID
}
func (m *Model) SessionReadiness(s domain.Session) domain.Readiness {
	ready := m.Readiness
	if state := m.DeviceStates[m.WorkspaceConnection(s.WorkspaceID)]; state != nil {
		ready = state.Readiness
	}
	minor := m.WorkerMinor
	if state := m.DeviceStates[m.WorkspaceConnection(s.WorkspaceID)]; state != nil {
		minor = state.WorkerMinor
	}
	return selectReadiness(ready, s.HarnessID, minor)
}
func (m *Model) RunReadiness() domain.Readiness { return m.selectReadiness(m.Readiness, m.HarnessID()) }
func (m *Model) selectReadiness(ready domain.Readiness, id string) domain.Readiness {
	return selectReadiness(ready, id, m.WorkerMinor)
}
func selectReadiness(ready domain.Readiness, id string, minor int) domain.Readiness {
	// Protocol <=9 exposed one fixed runtime and no capability catalog. Keep
	// that negotiated contract usable; never apply it to the new multi-harness API.
	if len(ready.Harnesses) == 0 && minor < 10 {
		ready.Capabilities = domain.HarnessCapabilities{Steer: minor >= 2, Interrupt: true, Recovery: minor >= 7, Continue: minor >= 7, Fork: minor >= 9, Restart: minor >= 9}
		return ready
	}
	if selected, ok := ready.ForHarness(id); ok {
		return selected
	}
	return domain.Readiness{HarnessID: id, State: domain.RuntimeUnavailable, Error: "所选 Agent 暂不可用，草稿已保留"}
}
func (m *Model) SelectHarness(id string) error {
	if m.SessionID != "" {
		return errors.New("会话的 Agent 创建后不能更改")
	}
	if len(m.Readiness.Harnesses) == 0 {
		return errors.New("执行服务尚未提供 Agent 目录")
	}
	if _, ok := m.Readiness.ForHarness(id); !ok {
		return errors.New("Agent 不在此设备的目录中")
	}
	if m.HasPendingInput() {
		return errors.New("请等待当前提交确认")
	}
	previous := m.HarnessID()
	if previous == id {
		return nil
	}
	if m.HomeHarnesses == nil {
		m.HomeHarnesses = map[string]string{}
	}
	if m.HarnessConfigs == nil {
		m.HarnessConfigs = map[string]domain.RunConfig{}
	}
	m.Config() // Capture the current context before changing selection.
	m.HomeHarnesses[m.WorkspaceID] = id
	m.Config()
	return nil
}
func (m *Model) PermissionName(id string) string {
	for _, choice := range m.RunReadiness().PermissionChoices {
		if choice.ID == id {
			return choice.Name
		}
	}
	// These are Shioji's legacy permission intents, never native wire policies.
	switch id {
	case "read-only":
		return "只读"
	case "auto-approve":
		return "自动批准"
	case "yolo":
		return "YOLO"
	case "default":
		return "默认权限"
	}
	return id
}

// Only one home composer is visible, but its run choices belong to the owning
// workspace and harness. The ordinary prompt remains one independent draft.
func (m *Model) syncHomeConfiguration() {
	id := m.HarnessID()
	// A disconnected first frame cannot establish a new configuration owner.
	if id == "" && strings.HasPrefix(m.homeConfigContext, m.WorkspaceID+"/") {
		return
	}
	key := m.WorkspaceID + "/" + id
	if m.homeConfigContext == key {
		return
	}
	if m.HarnessConfigs == nil {
		m.HarnessConfigs = map[string]domain.RunConfig{}
	}
	if m.homeConfigContext != "" {
		m.HarnessConfigs[m.homeConfigContext] = m.Configs["home"]
		if config, ok := m.HarnessConfigs[key]; ok {
			m.Configs["home"] = config
		} else if m.homeConfigContext != m.WorkspaceID+"/" {
			delete(m.Configs, "home")
		}
	}
	m.homeConfigContext = key
}

func (m *Model) PermissionDescription(id string) string {
	for _, choice := range m.RunReadiness().PermissionChoices {
		if choice.ID == id {
			return choice.Description
		}
	}
	if m.WorkerMinor < 10 {
		return map[string]string{"read-only": "仅查看", "default": "按需请求批准", "auto-approve": "沙盒内自动批准", "yolo": "不限制沙盒或批准"}[id]
	}
	return ""
}
