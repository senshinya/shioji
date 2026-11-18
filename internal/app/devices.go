package app

import (
	"context"
	"fmt"
	"net"
	"shioji/internal/domain"
	"shioji/internal/transport"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type DeviceConnection struct {
	Routes        []DeviceRoute   `json:"routes,omitempty"`
	ActiveRouteID string          `json:"activeRouteId,omitempty"`
	OtherSSH      []transport.SSH `json:"otherSSH,omitempty"`
	ID            string          `json:"id"`
	Home          string          `json:"home,omitempty"`
	Name          string          `json:"name"`
	Icon          DeviceIcon      `json:"icon,omitempty"`
	DeviceID      string          `json:"deviceId"`
	SSH           transport.SSH   `json:"ssh"`
}

type DeviceTiming struct {
	Milliseconds float64
	ObservedAt   time.Time
}

type DeviceState struct {
	// Transient access to independent streams, owned by this device controller.
	// Saved SSH routes cannot hold a live Tailnet bridge.
	dialStream        func(context.Context) (net.Conn, error)
	Health            *domain.DeviceHealth
	Paths             []DevicePathStatus
	WorkerService     *WorkerService
	Timing            *DeviceTiming
	sourceToken       string
	Snapshot          domain.Snapshot
	ScheduleSummaries []domain.Schedule
	Sequence          int64
	Connected         bool
	ConnectionEpoch   string
	WorkerMinor       int
	Readiness         domain.Readiness
	Error             string
}

type cachedDevice struct {
	Snapshot  domain.Snapshot   `json:"snapshot"`
	Readiness domain.Readiness  `json:"readiness"`
	Schedules []domain.Schedule `json:"schedules,omitempty"`
}

func (m *Model) cachedDevices() map[string]cachedDevice {
	result := map[string]cachedDevice{}
	for key, state := range m.DeviceStates {
		if state.Snapshot.DeviceID != "" {
			result[key] = cachedDevice{Snapshot: state.Snapshot, Readiness: state.Readiness, Schedules: cachedScheduleSummaries(state.Snapshot, state.ScheduleSummaries)}
		}
	}
	return result
}

func cachedScheduleSummaries(snapshot domain.Snapshot, tasks []domain.Schedule) []domain.Schedule {
	var result []domain.Schedule
	for _, task := range tasks {
		if task.ID == "" || task.DeviceID != snapshot.DeviceID || !slices.ContainsFunc(snapshot.Workspaces, func(w domain.Workspace) bool {
			return w.ID == task.WorkspaceID && w.DeviceID == task.DeviceID
		}) {
			continue
		}
		task.Text = ""
		if task.Latest != nil {
			latest := *task.Latest
			latest.Error = ""
			task.Latest = &latest
		}
		result = append(result, task)
	}
	return result
}
func (m *Model) validateDeviceSnapshot(key string, snapshot domain.Snapshot) error {
	if snapshot.DeviceID == "" {
		return nil
	}
	if p := snapshot.ArchivePolicy; p != nil && (p.DeviceID != snapshot.DeviceID || !domain.ValidArchiveDays(p.Days) || p.Revision < 1) {
		return fmt.Errorf("设备返回了无效的归档保留设置")
	}
	expected := m.LocalDeviceID
	if key != "local" {
		expected = ""
		for _, c := range m.Connections {
			if c.ID == key {
				expected = c.DeviceID
			}
		}
	}
	if expected != "" && expected != snapshot.DeviceID {
		return fmt.Errorf("设备身份已改变，请核对连接")
	}
	workspaces := map[string]bool{}
	sessions := map[string]bool{}
	for other, state := range m.DeviceStates {
		if other == key {
			continue
		}
		for _, w := range state.Snapshot.Workspaces {
			workspaces[w.ID] = true
		}
		for _, s := range state.Snapshot.Sessions {
			sessions[s.ID] = true
		}
	}
	for _, w := range snapshot.Workspaces {
		if w.DeviceID != snapshot.DeviceID || workspaces[w.ID] {
			return fmt.Errorf("设备返回了不属于本设备的工作区标识")
		}
	}
	ownWorkspaces := map[string]bool{}
	for _, w := range snapshot.Workspaces {
		ownWorkspaces[w.ID] = true
	}
	ownSessions := map[string]bool{}
	for _, s := range snapshot.Sessions {
		if !ownWorkspaces[s.WorkspaceID] {
			return fmt.Errorf("设备会话的工作区归属无效")
		}
		ownSessions[s.ID] = true
		if sessions[s.ID] {
			return fmt.Errorf("设备会话标识与另一台设备冲突")
		}
	}
	for _, r := range snapshot.PendingRequests {
		if !ownSessions[r.SessionID] {
			return fmt.Errorf("设备问题不属于本设备")
		}
	}
	for _, o := range snapshot.Operations {
		if o.SessionID != "" && !ownSessions[o.SessionID] {
			return fmt.Errorf("设备提交不属于本设备")
		}
	}
	return nil
}

func (m *Model) ConnectionID() string {
	workspaceID := m.WorkspaceID
	for _, s := range m.Snapshot.Sessions {
		if m.SessionID != "" && s.ID == m.SessionID {
			workspaceID = s.WorkspaceID
			break
		}
	}
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == workspaceID {
			for _, c := range m.Connections {
				if c.DeviceID == w.DeviceID {
					return c.ID
				}
			}
			if w.DeviceID == m.LocalDeviceID {
				return "local"
			}
		}
	}
	if m.ActiveConnection != "" {
		return m.ActiveConnection
	}
	return "local"
}
func (m *Model) CurrentDeviceName() string {
	key := m.ConnectionID()
	for _, c := range m.Connections {
		if c.ID == key {
			return c.Name
		}
	}
	return m.LocalName()
}
func (m *Model) DeviceName(device string) string {
	for _, c := range m.Connections {
		if c.DeviceID == device {
			return c.Name
		}
	}
	return m.LocalName()
}
func (m *Model) localIdentity() string {
	if m.LocalDeviceID != "" {
		return m.LocalDeviceID
	}
	if len(m.DeviceStates) == 0 {
		return m.KnownDeviceID
	}
	return ""
}

// Apply an existing single-worker reducer in a device-scoped projection. Drafts
// and forms stay client-owned; snapshots, sequence cursors and details stay scoped.
func (m *Model) ApplyDeviceUpdate(key string, update Update) {
	if m.DeviceStates == nil {
		m.DeviceStates = map[string]*DeviceState{}
	}
	state := m.DeviceStates[key]
	if state == nil {
		state = &DeviceState{}
		m.DeviceStates[key] = state
	}
	current := m.ConnectionID()
	if m.UncertainSubmissions == nil {
		m.UncertainSubmissions = map[string]bool{}
	}
	p := *m
	p.Navigation = m.Navigation.Clone()
	p.Notifications = nil
	p.Feedbacks = nil
	p.sourceConnection = key
	p.Pending = map[string]domain.Submission{}
	p.PendingDevices = map[string]string{}
	ownedPending := map[string]bool{}
	for id, in := range m.Pending {
		owner := m.PendingDevices[id]
		if owner == "" {
			owner = "local"
		}
		if owner == key {
			p.Pending[id] = in
			p.PendingDevices[id] = key
			ownedPending[id] = true
		}
	}
	p.Snapshot, p.Sequence = state.Snapshot, state.Sequence
	p.Snapshot.Workspaces = slices.Clone(state.Snapshot.Workspaces)
	p.Snapshot.Sessions = slices.Clone(state.Snapshot.Sessions)
	p.Snapshot.Operations = slices.Clone(state.Snapshot.Operations)
	p.Snapshot.PendingRequests = slices.Clone(state.Snapshot.PendingRequests)
	p.Connected, p.ConnectionEpoch = state.Connected, state.ConnectionEpoch
	p.Readiness, p.WorkerMinor, p.Error = state.Readiness, state.WorkerMinor, state.Error
	p.WorkerService = state.WorkerService
	p.KnownDeviceID = state.Snapshot.DeviceID
	p.ConnectionTiming = state.Timing
	p.DeviceHealth = state.Health
	p.DevicePaths = state.Paths
	p.Details = map[string]domain.SessionDetail{}
	owned := map[string]bool{}
	for _, s := range state.Snapshot.Sessions {
		owned[s.ID] = true
		if d, ok := m.Details[s.ID]; ok {
			p.Details[s.ID] = d
		}
	}
	workspace, session := m.WorkspaceID, m.SessionID
	update(&p)
	if err := m.validateDeviceSnapshot(key, p.Snapshot); err != nil {
		state.Connected = false
		state.ConnectionEpoch = ""
		state.Error = err.Error()
		if key == current {
			m.Error = err.Error()
		}
		m.RefreshDevices()
		return
	}
	if err := p.validateDeviceDetails(); err != nil {
		state.Connected = false
		state.ConnectionEpoch = ""
		state.Error = err.Error()
		if key == current {
			m.Error = err.Error()
		}
		m.RefreshDevices()
		return
	}
	for id := range ownedPending {
		delete(m.Pending, id)
		delete(m.PendingDevices, id)
	}
	for id, in := range p.Pending {
		m.Pending[id] = in
		m.PendingDevices[id] = key
	}
	if !state.Connected && p.Connected {
		p.Error = ""
	}
	state.Snapshot, state.Sequence = p.Snapshot, p.Sequence
	state.Timing = p.ConnectionTiming
	state.Health = p.DeviceHealth
	state.Paths = p.DevicePaths
	for i := range m.Connections {
		if m.Connections[i].ID != key {
			continue
		}
		for _, path := range p.DevicePaths {
			if path.Phase == PathActive {
				m.Connections[i].ActiveRouteID = path.ID
			}
		}
	}
	state.Connected, state.ConnectionEpoch = p.Connected, p.ConnectionEpoch
	state.Readiness, state.WorkerMinor, state.Error = p.Readiness, p.WorkerMinor, p.Error
	state.WorkerService = p.WorkerService
	for id := range owned {
		delete(m.Details, id)
	}
	for id, d := range p.Details {
		m.Details[id] = d
	}
	if key == "local" && p.Snapshot.DeviceID != "" {
		m.LocalDeviceID = p.Snapshot.DeviceID
	}
	if key == current {
		m.WorkspaceID, m.SessionID = p.WorkspaceID, p.SessionID
		m.Error = p.Error
	} else {
		m.WorkspaceID, m.SessionID = workspace, session
	}
	if owned[m.SecondarySessionID] || m.SecondaryWorkspaceID != "" && slices.ContainsFunc(state.Snapshot.Workspaces, func(w domain.Workspace) bool { return w.ID == m.SecondaryWorkspaceID }) {
		m.SecondarySessionID, m.SecondaryWorkspaceID, m.FocusSecondary = p.SecondarySessionID, p.SecondaryWorkspaceID, p.FocusSecondary
	}
	m.Notifications = append(m.Notifications, p.Notifications...)
	for _, feedback := range p.Feedbacks {
		m.ReportFeedback(feedback)
	}
	m.notificationSeen = p.notificationSeen
	m.CompletedReplies = p.CompletedReplies
	m.WorkDetails, m.workReads = p.WorkDetails, p.workReads
	m.Navigation = p.Navigation
	m.RefreshDevices()
	if service := state.WorkerService; service != nil && service.Phase == WorkerWaiting {
		build := ""
		if service.Available != nil {
			build = service.Available.BuildID
		}
		m.queueRecoveryNotice(key, "workerWaiting:"+build, "执行服务正在等待实际任务完成，可在设备详情取消更新或选择立即更新")
	}
}

func (m *Model) RefreshDevices() {
	if len(m.DeviceStates) == 0 {
		return
	}
	current := m.ConnectionID()
	keys := []string{"local"}
	for key := range m.DeviceStates {
		if key != "local" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys[1:])
	all := domain.Snapshot{}
	for _, key := range keys {
		state := m.DeviceStates[key]
		if state == nil {
			continue
		}
		s := state.Snapshot
		all.Workspaces = append(all.Workspaces, s.Workspaces...)
		all.Sessions = append(all.Sessions, s.Sessions...)
		all.PendingRequests = append(all.PendingRequests, s.PendingRequests...)
		all.Operations = append(all.Operations, s.Operations...)
		if key == current {
			all.DeviceID, all.Generation, all.Sequence = s.DeviceID, s.Generation, s.Sequence
			all.ArchivePolicy = s.ArchivePolicy
			m.Sequence, m.KnownDeviceID = state.Sequence, s.DeviceID
			m.Connected, m.ConnectionEpoch = state.Connected, state.ConnectionEpoch
			m.Readiness, m.WorkerMinor = state.Readiness, state.WorkerMinor
			m.WorkerService = state.WorkerService
			m.ConnectionTiming = state.Timing
			m.DeviceHealth = state.Health
			m.DevicePaths = state.Paths
		}
	}
	if m.DeviceStates[current] == nil {
		m.Connected = false
		m.ConnectionEpoch = ""
		m.Readiness = domain.Readiness{State: "unavailable"}
		m.WorkerService = nil
		m.DeviceHealth = nil
	}
	m.Snapshot = all
}

// Names are application display aliases, independent of OS hostnames and device IDs.
func (m *Model) LocalName() string {
	if m.LocalDeviceName != "" {
		return m.LocalDeviceName
	}
	return "这台 Mac"
}
func (m *Model) RenameDevice(key, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 || strings.ContainsAny(name, "\x00\r\n") {
		return fmt.Errorf("请输入 1–80 个字符的设备名称")
	}
	if key == "local" {
		m.LocalDeviceName = name
		return nil
	}
	for i := range m.Connections {
		if m.Connections[i].ID == key {
			m.Connections[i].Name = name
			return nil
		}
	}
	return fmt.Errorf("设备不存在")
}

// Keep device identity and display alias when adding another transport route.
func (m *Model) BindDeviceConnection(connection DeviceConnection) DeviceConnection {
	connection = connection.Normalized()
	for i, old := range m.Connections {
		if old.DeviceID == "" || old.DeviceID != connection.DeviceID {
			continue
		}
		old = old.Normalized()
		for _, route := range connection.Routes {
			found := false
			for j := range old.Routes {
				if old.Routes[j].ID == route.ID {
					old.Routes[j] = route
					found = true
					break
				}
			}
			if !found {
				old.Routes = append(old.Routes, route)
			}
		}
		connection.Routes, connection.ActiveRouteID = old.Routes, old.ActiveRouteID
		connection.ID = old.ID
		connection.Name = old.Name
		if old.Icon != DeviceIconAuto {
			connection.Icon = old.Icon
		}
		routes := append(append([]transport.SSH(nil), old.OtherSSH...), old.SSH)
		connection.OtherSSH = nil
		for _, route := range routes {
			if route.Target == connection.SSH.Target && route.Port == connection.SSH.Port && route.TailnetPeer == connection.SSH.TailnetPeer {
				continue
			}
			duplicate := false
			for _, saved := range connection.OtherSSH {
				if saved.Target == route.Target && saved.Port == route.Port && saved.TailnetPeer == route.TailnetPeer {
					duplicate = true
					break
				}
			}
			if !duplicate {
				route.ProxySocket = ""
				connection.OtherSSH = append(connection.OtherSSH, route)
			}
		}
		m.Connections[i] = connection
		return connection
	}
	m.Connections = append(m.Connections, connection)
	return connection
}

// SetDeviceHealth maps worker-relative sample ages to the client's clock. A
// remote machine's wall-clock skew must not make a fresh chart stale or future.
func (m *Model) SetDeviceHealth(h domain.DeviceHealth, received time.Time) {
	if old := m.DeviceHealth; old != nil && old.DeviceID == h.DeviceID && old.KeepAwake.Revision > h.KeepAwake.Revision {
		h.KeepAwake = old.KeepAwake
	}
	offset := received.Sub(h.ObservedAt)
	h.Samples = append([]domain.DeviceLoad(nil), h.Samples...)
	for i := range h.Samples {
		h.Samples[i].At = h.Samples[i].At.Add(offset)
	}
	h.ObservedAt = received
	m.DeviceHealth = &h
}
