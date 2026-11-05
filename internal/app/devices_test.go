package app

import (
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/transport"
	"testing"
)

func TestDeviceProjectionsKeepIndependentSequencesAndRequests(t *testing.T) {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "air", Name: "Air", DeviceID: "B", SSH: transport.SSH{Target: "air", Worker: "/worker"}}}
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.Connected = true
		p.ConnectionEpoch = "a"
		p.ApplySnapshot(domain.Snapshot{DeviceID: "A", Generation: "g-a", Sequence: 10, Workspaces: []domain.Workspace{{ID: "wa", DeviceID: "A"}}, Sessions: []domain.Session{{ID: "sa", WorkspaceID: "wa"}}})
	})
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.Connected = true
		p.ConnectionEpoch = "b"
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Generation: "g-b", Sequence: 3, Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B"}}, Sessions: []domain.Session{{ID: "sb", WorkspaceID: "wb"}}})
	})
	m.Details["sa"] = domain.SessionDetail{Requests: []domain.PendingRequest{{ID: "qa", State: "pending"}}}
	m.Details["sb"] = domain.SessionDetail{Requests: []domain.PendingRequest{{ID: "qb", State: "pending"}}}
	m.ApplyDeviceUpdate("air", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 4, Type: "session", SessionID: "sb", Session: &domain.Session{ID: "sb", WorkspaceID: "wb", Title: "remote title"}}); err != nil {
			t.Fatal(err)
		}
	})
	m.WorkspaceID = "wb"
	m.RefreshDevices()
	if len(m.Snapshot.Workspaces) != 2 || m.ConnectionID() != "air" || m.ConnectionEpoch != "b" {
		t.Fatal("device projection or route wrong", m.Snapshot, m.ConnectionID(), m.ConnectionEpoch)
	}
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "A", Generation: "g-a", Sequence: 11, Workspaces: []domain.Workspace{{ID: "wa", DeviceID: "A"}}, Sessions: []domain.Session{{ID: "sa", WorkspaceID: "wa"}}})
	})
	if len(m.Details["sb"].Requests) != 1 {
		t.Fatal("local reconnect clears remote questions")
	}
	m.ApplyDeviceUpdate("local", func(p *Model) { p.Connected = false; p.ConnectionEpoch = "" })
	if !m.Connected || m.ConnectionEpoch != "b" {
		t.Fatal("local disconnect disables remote composer")
	}
	if m.DeviceStates["local"].Sequence != 11 || m.DeviceStates["air"].Sequence != 4 {
		t.Fatal("device sequences interfere")
	}
}

func TestDeviceReconnectCannotNavigateAwayFromVisibleWorkspace(t *testing.T) {
	m := NewModel()
	m.WorkspaceID = "visible"
	m.SessionID = "reading"
	m.Drafts["session:reading"] = "keep"
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Generation: "g", Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B"}}})
	})
	if m.WorkspaceID != "visible" || m.SessionID != "reading" || m.Drafts["session:reading"] != "keep" {
		t.Fatal("background device changed current reading or draft")
	}
}

func TestDeviceCacheRestoresOfflineWithoutBorrowingOtherDeviceIdentity(t *testing.T) {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "air", Name: "Air", DeviceID: "B"}}
	for key, device := range map[string]string{"local": "A", "air": "B"} {
		m.ApplyDeviceUpdate(key, func(p *Model) {
			p.Connected = true
			p.ConnectionEpoch = device
			p.ApplySnapshot(domain.Snapshot{DeviceID: device, Generation: device, Workspaces: []domain.Workspace{{ID: "w" + device, DeviceID: device}}})
		})
	}
	m.WorkspaceID = "wB"
	m.ActiveConnection = "air"
	m.RefreshDevices()
	path := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	restored.RefreshDevices()
	if len(restored.Snapshot.Workspaces) != 2 || restored.ConnectionID() != "air" || restored.LocalDeviceID != "A" {
		t.Fatal("restart lost catalogs or local identity", restored.Snapshot, restored.LocalDeviceID)
	}
	for _, s := range restored.DeviceStates {
		if s.Connected || s.ConnectionEpoch != "" {
			t.Fatal("cached device presented as connected")
		}
	}
}

func TestDeviceProjectionRejectsCrossDeviceWorkspaceIdentity(t *testing.T) {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B"}}
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.Connected = true
		p.ApplySnapshot(domain.Snapshot{DeviceID: "A", Generation: "a", Workspaces: []domain.Workspace{{ID: "wa", DeviceID: "A", Name: "Keep"}}})
	})
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.Connected = true
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Generation: "b", Workspaces: []domain.Workspace{{ID: "wa", DeviceID: "A", Name: "Wrong"}}})
	})
	if m.DeviceStates["air"].Connected || len(m.Snapshot.Workspaces) != 1 || m.Snapshot.Workspaces[0].Name != "Keep" {
		t.Fatal("remote snapshot overwrites local device scope", m.Snapshot)
	}
}
