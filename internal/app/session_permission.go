package app

import (
	"encoding/json"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type sessionPermissionChange struct {
	connection, epoch, permission string
	clearPlan                     bool
}

func (m *Model) permissionConnection(key string) (bool, int, string) {
	if state := m.DeviceStates[key]; state != nil {
		return state.Connected, state.WorkerMinor, state.ConnectionEpoch
	}
	return m.Connected, m.WorkerMinor, m.ConnectionEpoch
}

func (m *Model) PermissionChangePending(id string) bool {
	if change := m.permissionChanges[id]; change != nil {
		connected, _, epoch := m.permissionConnection(change.connection)
		return connected && epoch == change.epoch
	}
	return false
}

func (m *Model) SelectPermission(key string, ready domain.Readiness, config domain.RunConfig, permission string, dispatch func(Job) bool) {
	config.Permission = permission
	clearPlan := ready.Capabilities.ExclusivePlan && ready.LivePermissionChange("", permission)
	if clearPlan {
		config.Plan = false
	}
	id, _ := strings.CutPrefix(key, "session:")
	for _, s := range m.Snapshot.Sessions {
		if s.ID != id || !ready.LivePermissionChange(s.Config.Permission, permission) {
			continue
		}
		if m.PermissionChangePending(id) {
			return
		}
		connection := m.WorkspaceConnection(s.WorkspaceID)
		if connection == "" && len(m.DeviceStates) == 0 && m.Snapshot.DeviceID != "" && m.workspaceDevice(s.WorkspaceID) == m.Snapshot.DeviceID {
			connection = "local" // The sole authenticated worker on mobile.
		}
		connected, minor, epoch := m.permissionConnection(connection)
		failure := func(model *Model, message string) {
			model.ReportFeedback(Feedback{ConnectionID: connection, SessionID: id, WorkspaceID: s.WorkspaceID,
				Kind: FeedbackFailure, Title: "权限更改未完成", Body: message})
		}
		if connection == "" || !connected || minor < protocol.LivePermissionMinor {
			failure(m, "请连接支持实时权限更改的执行服务")
			return
		}
		change := &sessionPermissionChange{connection: connection, epoch: epoch, permission: permission, clearPlan: clearPlan}
		if m.permissionChanges == nil {
			m.permissionChanges = make(map[string]*sessionPermissionChange)
		}
		m.permissionChanges[id] = change
		in := protocol.SessionPermissionInput{SessionID: id, ThreadID: s.ThreadID, Previous: s.Config.Permission, Permission: permission}
		if !dispatch(Job{ConnectionID: connection, ConnectionEpoch: epoch, Method: "session.permission", Input: in,
			Apply: func(model *Model, _ json.RawMessage, err error) {
				if model.permissionChanges[id] != change || err == nil {
					return
				}
				delete(model.permissionChanges, id)
				failure(model, err.Error())
			}}) {
			delete(m.permissionChanges, id)
			failure(m, "执行服务繁忙，请重试")
		}
		return
	}
	m.SetConfig(key, config)
}
