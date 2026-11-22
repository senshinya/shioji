package app

import (
	"errors"
	"shioji/internal/domain"
)

func (m *Model) ownsSession(id string) bool {
	for _, s := range m.Snapshot.Sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}
func (m *Model) ownsWorkspace(id string) bool {
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == id {
			return true
		}
	}
	return false
}
func (m *Model) scopeFailure(err error) {
	m.Error = err.Error()
	m.Connected = false
	m.ConnectionEpoch = ""
	m.ConnectionPhase, m.ConnectionError = ConnectionUnavailable, err.Error()
}
func (m *Model) validateDeviceEvent(e domain.Event) error {
	fail := errors.New("设备事件引用了其他设备的会话或工作区")
	if e.Deletion != nil {
		return m.validateArchiveDeletion(*e.Deletion)
	}
	if e.ArchivePolicy != nil && (e.ArchivePolicy.DeviceID != m.Snapshot.DeviceID || !domain.ValidArchiveDays(e.ArchivePolicy.Days)) {
		return fail
	}
	if e.Workspace != nil && e.Workspace.DeviceID != m.Snapshot.DeviceID {
		return fail
	}
	if e.Workspace != nil {
		for key, state := range m.DeviceStates {
			if key == m.sourceConnection {
				continue
			}
			for _, w := range state.Snapshot.Workspaces {
				if w.ID == e.Workspace.ID {
					return fail
				}
			}
		}
	}
	if e.Session != nil && m.sessionOnOtherDevice(m.sourceConnection, e.Session.ID) {
		return fail
	}

	workspace := func(id string) bool { return m.ownsWorkspace(id) || e.Workspace != nil && e.Workspace.ID == id }
	if e.Session != nil && !workspace(e.Session.WorkspaceID) {
		return fail
	}
	session := func(id string) bool { return m.ownsSession(id) || e.Session != nil && e.Session.ID == id }
	if e.SessionID != "" && !session(e.SessionID) {
		return fail
	}
	if e.Queue != nil && !session(e.Queue.SessionID) {
		return fail
	}
	if e.Request != nil && !session(e.Request.SessionID) {
		return fail
	}
	if e.Operation != nil && e.Operation.SessionID != "" && !session(e.Operation.SessionID) {
		return fail
	}
	return nil
}
func (m *Model) validateDeviceDetails() error {
	for id, d := range m.Details {
		if !m.ownsSession(id) || d.Session.ID != "" && d.Session.ID != id || d.Session.WorkspaceID != "" && !m.ownsWorkspace(d.Session.WorkspaceID) || d.Queue.SessionID != "" && d.Queue.SessionID != id {
			return errors.New("设备返回了其他会话的内容")
		}
		for _, r := range d.Requests {
			if r.SessionID != "" && r.SessionID != id {
				return errors.New("设备问题不属于当前会话")
			}
		}
	}
	return nil
}
