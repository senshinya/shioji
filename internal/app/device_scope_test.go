package app

import (
	"shioji/internal/domain"
	"testing"
)

func scopedDeviceModel() *Model {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B"}}
	for key, device := range map[string]string{"local": "A", "air": "B"} {
		m.ApplyDeviceUpdate(key, func(p *Model) {
			p.Connected = true
			p.ConnectionEpoch = device
			p.ApplySnapshot(domain.Snapshot{DeviceID: device, Generation: device, Workspaces: []domain.Workspace{{ID: "w" + device, DeviceID: device}}, Sessions: []domain.Session{{ID: "s" + device, WorkspaceID: "w" + device}}})
		})
	}
	m.Details["sA"] = domain.SessionDetail{Session: domain.Session{ID: "sA", WorkspaceID: "wA"}, Items: []domain.Item{{ID: "local-item", Text: "keep local content"}}}
	m.SessionID = "sA"
	m.SetConfig(m.DraftKey(), domain.RunConfig{Permission: "read-only"})
	m.Pending["b-op"] = domain.Submission{OperationID: "b-op", WorkspaceID: "wB", SessionID: "sB", Text: "run", Config: domain.RunConfig{Permission: "yolo"}}
	m.PendingDevices["b-op"] = "air"
	m.Drafts["session:sB"] = "run"
	return m
}
func TestRemoteQueueCannotReplaceAnotherDevicesConversation(t *testing.T) {
	m := scopedDeviceModel()
	m.ApplyDeviceUpdate("air", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 1, Type: "queue", SessionID: "sB", Queue: &domain.QueueState{SessionID: "sA", Revision: 1}}); err != nil {
			p.Error = err.Error()
			p.Connected = false
		}
	})
	if len(m.Details["sA"].Items) != 1 || m.Details["sA"].Queue.SessionID != "" {
		t.Fatal("remote queue overwrites local conversation", m.Details["sA"])
	}
}
func TestRemoteOperationCannotChangeAnotherDevicesPermission(t *testing.T) {
	m := scopedDeviceModel()
	m.ApplyDeviceUpdate("air", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 1, Type: "operation", SessionID: "sB", Operation: &domain.Operation{ID: "b-op", SessionID: "sA", State: "confirmed"}}); err != nil {
			p.Error = err.Error()
			p.Connected = false
		}
	})
	if m.Config().Permission != "read-only" || m.Drafts["session:sB"] != "run" || m.Resolved["b-op"] {
		t.Fatal("remote receipt changes local permission or shared draft")
	}
}
func TestRejectedSnapshotCannotMutateDraftsOrImportForeignRequests(t *testing.T) {
	m := scopedDeviceModel()
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Generation: "B", Sequence: 2, Workspaces: []domain.Workspace{{ID: "wB", DeviceID: "B"}}, Sessions: []domain.Session{{ID: "sB", WorkspaceID: "wB"}}, PendingRequests: []domain.PendingRequest{{ID: "foreign", SessionID: "sA", State: "pending"}}, Operations: []domain.Operation{{ID: "b-op", SessionID: "sA", State: "confirmed"}}})
	})
	if m.Config().Permission != "read-only" || m.Drafts["session:sB"] != "run" || len(m.Details["sA"].Requests) != 0 || len(m.Details["sA"].Items) != 1 || m.Resolved["b-op"] {
		t.Fatal("rejected snapshot mutated shared state")
	}
}
func TestDeviceCannotResolveAnotherDevicesPendingOperation(t *testing.T) {
	m := scopedDeviceModel()
	m.Pending["a-op"] = domain.Submission{OperationID: "a-op", WorkspaceID: "wA", SessionID: "sA", Text: "local"}
	m.PendingDevices["a-op"] = "local"
	m.Drafts["session:sA"] = "local"
	m.ApplyDeviceUpdate("air", func(p *Model) { p.ApplyOperation(domain.Operation{ID: "a-op", SessionID: "sB", State: "confirmed"}) })
	if m.Resolved["a-op"] || m.Drafts["session:sA"] != "local" {
		t.Fatal("remote reconciliation resolved local pending operation")
	}
}

func TestRejectedDetailCannotMutateCachedSessionOwnership(t *testing.T) {
	m := scopedDeviceModel()
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.Snapshot.Sessions[0].WorkspaceID = "wA"
		p.Details["sB"] = domain.SessionDetail{Session: domain.Session{ID: "sB", WorkspaceID: "wA"}}
	})
	if m.DeviceStates["air"].Snapshot.Sessions[0].WorkspaceID != "wB" {
		t.Fatal("rejected detail already mutated cached session owner")
	}
}

func TestForeignSessionMetadataCannotAuthorizeForeignReceipt(t *testing.T) {
	m := scopedDeviceModel()
	m.ApplyDeviceUpdate("air", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 1, Type: "operation", SessionID: "sA", Session: &domain.Session{ID: "sA", WorkspaceID: "wB"}, Operation: &domain.Operation{ID: "b-op", SessionID: "sA", State: "confirmed"}}); err != nil {
			p.Connected = false
			p.Error = err.Error()
		}
	})
	if m.Config().Permission != "read-only" || m.Resolved["b-op"] {
		t.Fatal("foreign session metadata bypasses receipt ownership check")
	}
}
