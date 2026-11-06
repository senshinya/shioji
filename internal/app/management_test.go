package app

import (
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func TestArchivedSessionKeepsDraftAndRejectsNewSubmission(t *testing.T) {
	m := NewModel()
	m.Connected = true
	m.Readiness = domain.Readiness{State: "ready"}
	m.WorkspaceID = "w"
	m.SessionID = "s"
	m.Snapshot = domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w", Folders: []string{"/fixture"}}}, Sessions: []domain.Session{{ID: "s", WorkspaceID: "w", Status: "completed", Archived: true}}}
	m.Drafts["session:s"] = "keep draft"
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("archived session accepted input")
	}
	if m.Drafts["session:s"] != "keep draft" || len(m.Pending) != 0 {
		t.Fatal("archive guard changed draft or created pending submission")
	}
}
func TestMetadataReceiptCannotReplaceAnotherDevicesSession(t *testing.T) {
	m := NewModel()
	m.LocalDeviceID = "A"
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B"}}
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "A", Workspaces: []domain.Workspace{{ID: "wa", DeviceID: "A"}}, Sessions: []domain.Session{{ID: "sa", WorkspaceID: "wa"}}})
	})
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B"}}, Sessions: []domain.Session{{ID: "sb", WorkspaceID: "wb"}}})
	})
	body, _ := json.Marshal(domain.Session{ID: "sa", WorkspaceID: "wa", Title: "forged"})
	if err := m.validateDeviceReply("air", Job{Method: "session.rename", Input: protocol.SessionChangeInput{SessionID: "sb", Title: "name"}}, body); err == nil {
		t.Fatal("foreign metadata receipt accepted")
	}
}
