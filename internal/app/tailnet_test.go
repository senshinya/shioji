package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"strings"
	"testing"
)

func TestTailnetRouteSurvivesClientPersistenceWithoutAuthURL(t *testing.T) {
	m := NewModel()
	m.Tailnet = tailnet.Snapshot{AuthURL: "https://login.example/secret"}
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "same-device", Name: "Air", SSH: transport.SSH{Target: "user@100.64.0.1", TailnetPeer: "peer-id", ProxySocket: "/private/transient.sock"}}}
	raw, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "transient.sock") {
		t.Fatal("transient secret or proxy in client state")
	}
	var state map[string]any
	if json.Unmarshal(raw, &state) != nil {
		t.Fatal("invalid JSON")
	}
	p := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(p, raw, 0600)
	restored := NewModel()
	if err = restored.Load(p); err != nil {
		t.Fatal(err)
	}
	s := restored.Connections[0].SSH
	if s.TailnetPeer != "peer-id" || restored.Connections[0].DeviceID != "same-device" {
		t.Fatal("identity lost")
	}
	c := NewController("")
	n := tailnet.New(filepath.Join(t.TempDir(), "node"), nil)
	defer n.Close()
	c.Tailnet = n
	s, err = c.TailnetSSH(s)
	if err != nil || s.ProxySocket == "" {
		t.Fatal("route not restored", err)
	}
}

func TestAddingTailnetRoutePreservesExistingDeviceAndSSHAddress(t *testing.T) {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "old-id", DeviceID: "worker-id", Name: "My Air", SSH: transport.SSH{Target: "user@192.168.7.119"}}}
	connection := m.BindDeviceConnection(DeviceConnection{ID: "new-id", DeviceID: "worker-id", Name: "MacBook Air", SSH: transport.SSH{Target: "user@100.64.0.1", TailnetPeer: "tailnet-peer"}})
	if len(m.Connections) != 1 || connection.ID != "old-id" || connection.Name != "My Air" {
		t.Fatal("device duplicated or alias reset", connection)
	}
	if len(connection.OtherSSH) != 1 || connection.OtherSSH[0].Target != "user@192.168.7.119" {
		t.Fatal("original direct connection lost", connection)
	}
}
