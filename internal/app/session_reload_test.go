package app

import (
	"encoding/json"
	"shioji/internal/domain"
	"testing"
)

func TestReloadReceiptCannotCrossDeviceSessionOrSourceThread(t *testing.T) {
	m := NewModel()
	m.LocalDeviceID = "A"
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B"}}
	s := domain.Session{ID: "sb", WorkspaceID: "wb", ThreadID: "source-B", Context: domain.LaunchContext{DeviceID: "B"}}
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B"}}, Sessions: []domain.Session{s}})
	})
	in := domain.SessionReloadRequest{OperationID: "reload", SessionID: "sb", Revision: 7}
	result := domain.SessionReloadResult{Operation: domain.SessionReloadOperation{Input: in, State: "completed"}, Session: s}
	job := Job{Method: "session.reload", Input: in}
	for _, field := range []string{"valid", "device", "session", "thread", "operation"} {
		v := result
		switch field {
		case "device":
			v.Session.Context.DeviceID = "A"
		case "session":
			v.Session.ID = "sa"
		case "thread":
			v.Session.ThreadID = "source-A"
		case "operation":
			v.Operation.Input.OperationID = "other"
		}
		b, _ := json.Marshal(v)
		err := m.validateDeviceReply("air", job, b)
		if field == "valid" && err != nil || field != "valid" && err == nil {
			t.Fatalf("%s receipt validation: %v", field, err)
		}
	}
}

func TestSessionReloadReadinessRequiresCatalogAndActualProcess(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		for _, actual := range []bool{false, true} {
			m := NewModel()
			m.WorkerMinor = 45
			m.Readiness = domain.Readiness{HarnessID: "codex", Capabilities: domain.HarnessCapabilities{Reload: catalog}, Sessions: map[string]domain.HarnessInfo{"s": {ID: "codex", State: domain.RuntimeReady, Capabilities: domain.HarnessCapabilities{Reload: actual}}}}
			got := m.SessionReadiness(domain.Session{ID: "s", HarnessID: "codex"}).Capabilities.Reload
			if got != (catalog && actual) {
				t.Fatalf("catalog=%v actual=%v: reload=%v", catalog, actual, got)
			}
		}
	}
}
