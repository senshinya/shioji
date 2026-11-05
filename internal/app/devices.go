package app

import (
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/transport"
	"slices"
	"sort"
	"time"
)

type DeviceConnection struct {
	ID       string        `json:"id"`
	Home     string        `json:"home,omitempty"`
	Name     string        `json:"name"`
	DeviceID string        `json:"deviceId"`
	SSH      transport.SSH `json:"ssh"`
}

type DeviceTiming struct {
	Milliseconds float64
	ObservedAt   time.Time
}

type DeviceState struct {
	Timing          *DeviceTiming
	sourceToken     string
	Snapshot        domain.Snapshot
	Sequence        int64
	Connected       bool
	ConnectionEpoch string
	WorkerMinor     int
	Readiness       domain.Readiness
	Error           string
}

type cachedDevice struct {
	Snapshot  domain.Snapshot  `json:"snapshot"`
	Readiness domain.Readiness `json:"readiness"`
}

func (m *Model) cachedDevices() map[string]cachedDevice {
	result := map[string]cachedDevice{}
	for key, state := range m.DeviceStates {
		if state.Snapshot.DeviceID != "" {
			result[key] = cachedDevice{state.Snapshot, state.Readiness}
		}
	}
	return result
}
func (m *Model) validateDeviceSnapshot(key string, snapshot domain.Snapshot) error {
	if snapshot.DeviceID == "" {
		return nil
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
	return "这台 Mac"
}
func (m *Model) DeviceName(device string) string {
	for _, c := range m.Connections {
		if c.DeviceID == device {
			return c.Name
		}
	}
	return "这台 Mac"
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
	p := *m
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
	p.KnownDeviceID = state.Snapshot.DeviceID
	p.ConnectionTiming = state.Timing
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
	state.Connected, state.ConnectionEpoch = p.Connected, p.ConnectionEpoch
	state.Readiness, state.WorkerMinor, state.Error = p.Readiness, p.WorkerMinor, p.Error
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
	m.RefreshDevices()
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
			m.Sequence, m.KnownDeviceID = state.Sequence, s.DeviceID
			m.Connected, m.ConnectionEpoch = state.Connected, state.ConnectionEpoch
			m.Readiness, m.WorkerMinor = state.Readiness, state.WorkerMinor
			m.ConnectionTiming = state.Timing
		}
	}
	if m.DeviceStates[current] == nil {
		m.Connected = false
		m.ConnectionEpoch = ""
		m.Readiness = domain.Readiness{State: "unavailable"}
	}
	m.Snapshot = all
}
