package app

func (m *Model) ArchivedContext() string {
	workspace := m.WorkspaceID
	sessionArchived := false
	for _, session := range m.Snapshot.Sessions {
		if session.ID == m.SessionID {
			workspace = session.WorkspaceID
			sessionArchived = session.Archived
			break
		}
	}
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == workspace && w.Archived {
			return "工作区"
		}
	}
	if sessionArchived {
		return "会话"
	}
	return ""
}
func (m *Model) WorkspaceConnection(id string) string {
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == id {
			if w.DeviceID == "" || w.DeviceID == m.LocalDeviceID {
				return "local"
			}
			for _, connection := range m.Connections {
				if connection.DeviceID == w.DeviceID {
					return connection.ID
				}
			}
		}
	}
	return ""
}
