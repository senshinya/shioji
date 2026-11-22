package mobileclient

import (
	"encoding/json"
	"testing"
)

func TestSavedDeviceMergesRoutesWithoutDuplicatingWorker(t *testing.T) {
	var device SavedDevice
	if err := json.Unmarshal([]byte(`{"slot":"work","endpoint":{"deviceID":"worker","address":"mac.local:22","user":"alice","hostFingerprint":"direct-pin"}}`), &device); err != nil {
		t.Fatal(err)
	}
	lan := device.Endpoint
	mesh := Endpoint{DeviceID: "worker", Address: "100.64.0.2:22", User: "bob", HostFingerprint: "mesh-pin", TailnetPeerID: "peer"}
	next, err := device.WithRoute(mesh)
	if err != nil {
		t.Fatal(err)
	}
	if next.Slot != "work" || next.Endpoint != lan || len(next.Routes) != 2 || next.Routes[0] != lan || next.Routes[1] != mesh {
		t.Fatalf("did not preserve the existing route and device slot: %+v", next)
	}
	if mesh.RouteID() == lan.RouteID() || mesh.RouteID() == "" {
		t.Fatal("routes do not have distinct, stable IDs")
	}
	newTrust := mesh
	newTrust.HostFingerprint = "new-mesh-pin"
	repaired, err := next.WithRoute(newTrust)
	if err != nil || len(repaired.Routes) != 2 || repaired.Routes[1] != newTrust || repaired.Routes[0] != lan || next.Routes[1] != mesh {
		t.Fatal("reverification failed to replace exactly one scoped route", repaired, err)
	}
	other := mesh
	other.DeviceID = "different-worker"
	if _, err := repaired.WithRoute(other); err == nil {
		t.Fatal("foreign Worker was merged into this device")
	}
	raw, err := json.Marshal(repaired)
	if err != nil || json.Unmarshal(raw, &device) != nil || device.Routes[1].RouteID() != newTrust.RouteID() {
		t.Fatal("routes did not survive persistence", err)
	}
}

func TestSavedDeviceRemovesOnlyVerifiedRouteAndPromotesReplacement(t *testing.T) {
	first := Endpoint{DeviceID: "worker", Address: "mac.local:22", User: "alice", HostFingerprint: "direct-pin"}
	second := Endpoint{DeviceID: "worker", Address: "100.64.0.2:22", User: "bob", HostFingerprint: "tailnet-pin", TailnetPeerID: "peer"}
	device := SavedDevice{Slot: "worker-slot", Endpoint: first, Routes: []Endpoint{first, second}, Name: "Build Mac"}
	next, err := device.WithoutRoute(first.RouteID(), second.RouteID())
	if err != nil || next.Endpoint != second || next.Slot != device.Slot || next.Name != device.Name ||
		len(next.Endpoints()) != 1 || next.Endpoints()[0] != second || len(device.Endpoints()) != 2 || device.Endpoints()[0] != first {
		t.Fatalf("unexpected route or device changes: %+v (%v)", next, err)
	}
	if _, err := next.WithoutRoute(second.RouteID(), ""); err == nil {
		t.Fatal("removed the last enrolled route")
	}
	edited := first
	edited.HostFingerprint = "new-pin"
	if _, err := device.WithoutRoute(edited.RouteID(), second.RouteID()); err == nil {
		t.Fatal("a route with changed trust settings was removed")
	}
}
