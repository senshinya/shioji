package app

import (
	"shioji/internal/transport"
	"testing"
)

func TestStorageTargetUsesActiveOwningDeviceRoute(t *testing.T) {
	m := NewModel()
	m.DeviceStates["air"] = &DeviceState{Connected: true}
	m.Connections = []DeviceConnection{{
		ID: "air", DeviceID: "air-worker",
		SSH:           transport.SSH{Target: "old@192.168.1.1"},
		ActiveRouteID: "tailnet",
		Routes: []DeviceRoute{{ID: "tailnet", SSH: transport.SSH{
			Target: "user@100.64.0.9", TailnetPeer: "air-peer", Worker: "/worker",
		}}},
	}}
	target, err := m.StorageTarget("/local-worker", "air")
	if err != nil {
		t.Fatal(err)
	}
	if target.DeviceID != "air-worker" || target.SSH == nil || target.SSH.Target != "user@100.64.0.9" || target.SSH.TailnetPeer != "air-peer" {
		t.Fatalf("storage ignored the owning device's active route: %+v", target)
	}
}
