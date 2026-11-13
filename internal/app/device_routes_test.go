package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/transport"
	"testing"
)

func routeJSON(t *testing.T, c DeviceConnection) map[string]any {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDeviceRouteMigrationAndMergeKeepsActive(t *testing.T) {
	for _, tailnet := range []bool{false, true} {
		m := NewModel()
		m.Connections = []DeviceConnection{{ID: "air", DeviceID: "worker", Name: "My Air", SSH: transport.SSH{Target: "shinya@192.168.7.119", Worker: "/worker"}}}
		next := transport.SSH{Target: "shinya@mba.local", Worker: "/worker"}
		if tailnet {
			next.Target, next.TailnetPeer = "shinya@100.88.0.105", "peer"
		}
		c := m.BindDeviceConnection(DeviceConnection{ID: "new", DeviceID: "worker", Name: "New name", SSH: next})
		out := routeJSON(t, c)
		routes, ok := out["routes"].([]any)
		if !ok || len(routes) != 2 {
			t.Fatalf("both routes must be persisted: %s", mustJSON(c))
		}
		active := out["activeRouteId"]
		found := false
		for _, value := range routes {
			r := value.(map[string]any)
			if r["id"] == active {
				found = r["ssh"].(map[string]any)["target"] == "shinya@192.168.7.119"
			}
		}
		if !found || c.ID != "air" || c.Name != "My Air" || len(m.Connections) != 1 {
			t.Fatal("adding a route replaced device, name or active route", out)
		}
		c = m.BindDeviceConnection(DeviceConnection{ID: "again", DeviceID: "worker", SSH: next})
		if len(routeJSON(t, c)["routes"].([]any)) != 2 {
			t.Fatal("duplicate route added")
		}
	}
}

func TestDeviceRouteLegacyLoadPersistsStableIDs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(p, []byte(`{"connections":[{"id":"air","name":"Air","deviceId":"worker","ssh":{"target":"air","worker":"/worker"},"otherSSH":[{"target":"air-lan","worker":"/worker"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.Load(p); err != nil {
		t.Fatal(err)
	}
	first := routeJSON(t, m.Connections[0])
	if _, ok := first["routes"]; !ok {
		t.Fatal("legacy endpoints did not migrate to stable routes")
	}
	b, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	n := NewModel()
	if err := n.Load(p); err != nil {
		t.Fatal(err)
	}
	if string(mustJSON(first)) != string(mustJSON(routeJSON(t, n.Connections[0]))) {
		t.Fatal("route identity changed on reopen")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
