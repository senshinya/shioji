package mobileclient

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"shioji/internal/app"
)

func TestSavedDeviceAppearanceFollowsVerifiedIdentityAcrossRoutes(t *testing.T) {
	const legacy = `{"slot":"isolated","endpoint":{"deviceID":"worker-a","address":"mac.local:22","user":"dev","hostFingerprint":"SHA256:confirmed"}}`
	var device SavedDevice
	if err := json.Unmarshal([]byte(legacy), &device); err != nil {
		t.Fatal(err)
	}
	if device.Name != "" || device.Icon != app.DeviceIconAuto {
		t.Fatalf("existing devices must keep their automatic appearance: %+v", device)
	}

	updated, err := device.WithAppearance("  Studio Mac  ", app.DeviceIconServer)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Studio Mac" || updated.Icon != app.DeviceIconServer ||
		updated.Endpoint != device.Endpoint || updated.Slot != device.Slot ||
		!reflect.DeepEqual(updated.Endpoints(), device.Endpoints()) {
		t.Fatalf("appearance must preserve the saved connection: %+v", updated)
	}
	route := device.Endpoint
	route.Address = "100.64.0.1:22"
	updated, err = updated.WithRoute(route)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}
	var restored SavedDevice
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated, restored) || len(restored.Endpoints()) != 2 ||
		restored.Name != "Studio Mac" || restored.Icon != app.DeviceIconServer {
		t.Fatalf("appearance is independent of the active route: %+v", restored)
	}
	reset, err := restored.WithAppearance("", app.DeviceIconAuto)
	if err != nil || reset.Name != "" || reset.Icon != app.DeviceIconAuto || !reflect.DeepEqual(reset.Endpoints(), restored.Endpoints()) {
		t.Fatalf("clearing the name and icon must restore the automatic appearance: %+v, %v", reset, err)
	}
}

func TestSavedDeviceAppearanceRejectsInvalidEdits(t *testing.T) {
	device := SavedDevice{Endpoint: Endpoint{DeviceID: "worker-a"}}
	for _, input := range []struct {
		name string
		icon app.DeviceIcon
	}{{"a\nb", app.DeviceIconAuto}, {strings.Repeat("設", 81), app.DeviceIconAuto}, {"Mac", "unknown"}} {
		got, err := device.WithAppearance(input.name, input.icon)
		if err == nil || !reflect.DeepEqual(got, device) {
			t.Fatalf("invalid appearance updated the device: %+v, %v", got, err)
		}
	}
	if _, err := (SavedDevice{}).WithAppearance("Mac", app.DeviceIconLaptop); err == nil {
		t.Fatal("cannot set the appearance of an unbound connection")
	}
}
