package app

import (
	"errors"
	"shioji/internal/domain"
	"slices"
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
	m.initializeHomeDefaults()
	if selected := m.HomeHarnesses[m.WorkspaceID]; selected != "" {
		return selected
	}
	return m.Readiness.HarnessID
}

// Snapshots retain the worker's insertion order; metadata updates replace a
// session in place. Sidebar order and local edits to old sessions are separate.
func (m *Model) latestDeviceSession() (domain.Session, bool) {
	device := m.workspaceDevice(m.WorkspaceID)
	if device == "" {
		return domain.Session{}, false
	}
	workspaces := map[string]bool{}
	for _, w := range m.Snapshot.Workspaces {
		if w.DeviceID == device {
			workspaces[w.ID] = true
		}
	}
	for i := len(m.Snapshot.Sessions) - 1; i >= 0; i-- {
		if s := m.Snapshot.Sessions[i]; workspaces[s.WorkspaceID] {
			return s, true
		}
	}
	return domain.Session{}, false
}

// PrepareNewSession opens the independent home draft, without creating a
// worker session. Existing input and manual draft choices remain intact.
func (m *Model) PrepareNewSession() {
	m.SessionID = ""
	m.homeDefaultsPending = true
	m.RefreshDevices()
}

func (m *Model) initializeHomeDefaults() {
	if m.SessionID != "" {
		return
	}
	legacy := m.homeConfigContext == "" && m.Configs["home"].Model != ""
	configured := legacy || strings.HasPrefix(m.homeConfigContext, m.WorkspaceID+"/") || m.HomeHarnesses[m.WorkspaceID] != ""
	if !m.homeDefaultsPending && configured {
		return
	}
	if configured && (m.HasDraftContent("home") || m.HasPendingInput()) {
		m.homeDefaultsPending = false
		return
	}
	// Cached catalogs can render while disconnected. Wait for the owning
	// worker's fresh snapshot before replacing an empty composer's defaults.
	if !m.Connected || m.workspaceDevice(m.WorkspaceID) == "" {
		return
	}
	m.homeDefaultsPending = false
	latest, ok := m.latestDeviceSession()
	if !ok {
		return
	}
	id := latest.HarnessID
	if id == "" {
		id = m.Readiness.HarnessID
	}
	if m.HomeHarnesses == nil {
		m.HomeHarnesses = map[string]string{}
	}
	m.HomeHarnesses[m.WorkspaceID] = id
	m.syncHomeContext(id)
	config := latest.Config
	if config.Permission == "" {
		config.Permission = "default"
		ready := m.selectReadiness(m.Readiness, id)
		if !slices.Contains(ready.Permissions, config.Permission) && len(ready.Permissions) > 0 {
			config.Permission = ready.Permissions[0]
		}
	}
	m.Configs["home"] = config
}

// Explicit choices also freeze an offline draft's defaults while reconnecting.
func (m *Model) SetConfig(key string, config domain.RunConfig) {
	if key == "home" && m.SessionID == "" {
		m.homeDefaultsPending = false
		id := m.HarnessID()
		if m.HomeHarnesses == nil {
			m.HomeHarnesses = map[string]string{}
		}
		m.HomeHarnesses[m.WorkspaceID] = id
	}
	m.Configs[key] = config
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
	selected := selectReadiness(ready, s.HarnessID, minor)
	// Next-turn model choices follow current availability; live control actions
	// belong to the process actually executing this session.
	if own, ok := ready.Sessions[s.ID]; ok && own.ID == s.HarnessID && own.State == domain.RuntimeReady {
		live := own.Capabilities
		live.Skills = live.Skills && selected.Capabilities.Skills
		live.Fork = live.Fork && selected.Capabilities.Fork
		live.Restart = live.Restart && selected.Capabilities.Restart
		live.Continue = live.Continue && selected.Capabilities.Continue
		live.Reload = live.Reload && selected.Capabilities.Reload
		selected.Capabilities = live
	}
	return selected
}
func (m *Model) RunReadiness() domain.Readiness {
	if m.SessionID != "" {
		if d := m.Details[m.SessionID]; d.Session.ID != "" {
			return m.SessionReadiness(d.Session)
		}
		for _, s := range m.Snapshot.Sessions {
			if s.ID == m.SessionID {
				return m.SessionReadiness(s)
			}
		}
	}
	return m.selectReadiness(m.Readiness, m.HarnessID())
}

const ModelUnavailableMessage = domain.ModelUnavailableMessage

func (m *Model) ReadOnlyConversationReason() string {
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID && s.ReadOnlyHistory() {
			if s.Origin.Reason != "" {
				return s.Origin.Reason
			}
			return "此导入历史仅可查看"
		}
	}
	return ""
}

func (m *Model) ModelUnavailable() bool {
	ready := m.RunReadiness()
	config := m.Config()
	if ready.State != domain.RuntimeReady || config.Model == "" {
		return false
	}
	for _, model := range ready.Models {
		if model.ID == config.Model {
			return false
		}
	}
	return true
}
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
	return domain.Readiness{Sessions: ready.Sessions, HarnessID: id, State: domain.RuntimeUnavailable, Error: "所选 Agent 暂不可用，草稿已保留"}
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
	m.homeDefaultsPending = false
	if m.HomeHarnesses == nil {
		m.HomeHarnesses = map[string]string{}
	}
	if previous == id {
		m.HomeHarnesses[m.WorkspaceID] = id
		return nil
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
	m.syncHomeContext(m.HarnessID())
}

func (m *Model) syncHomeContext(id string) {
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
