package app

import (
	"path/filepath"
	"testing"

	"shioji/internal/transport"
)

func TestDeviceIconsPersistIndependentlyForLocalAndRemoteDevices(t *testing.T) {
	m := NewModel()
	m.LocalDeviceID = "local-device"
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "remote-device", Name: "工作电脑", SSH: transport.SSH{Target: "user@192.0.2.1"}}}
	if err := m.SetDeviceIcon("local", DeviceIconLaptop); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDeviceIcon("air", DeviceIconTablet); err != nil {
		t.Fatal(err)
	}
	data, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "client.json")
	if err = WriteState(path, data); err != nil {
		t.Fatal(err)
	}
	loaded := NewModel()
	if err = loaded.Load(path); err != nil {
		t.Fatal(err)
	}
	if loaded.DeviceIconChoice("local") != DeviceIconLaptop || loaded.DeviceIconChoice("air") != DeviceIconTablet {
		t.Fatal("device icon choices lost")
	}
	if loaded.Connections[0].Name != "工作电脑" || loaded.Connections[0].DeviceID != "remote-device" {
		t.Fatal("icon changed device identity or alias")
	}
	if err = loaded.SetDeviceIcon("air", DeviceIconAuto); err != nil {
		t.Fatal(err)
	}
	if loaded.DeviceIconChoice("air") != DeviceIconAuto {
		t.Fatal("automatic icon cannot be restored")
	}
}

func TestAddingConnectionPreservesExistingDeviceIconAndAlias(t *testing.T) {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "saved", DeviceID: "device", Name: "我的 Air", Icon: DeviceIconLaptop, SSH: transport.SSH{Target: "user@192.0.2.1"}}}
	bound := m.BindDeviceConnection(DeviceConnection{ID: "incoming", DeviceID: "device", Name: "tailscale-host", Icon: DeviceIconPhone, SSH: transport.SSH{Target: "user@100.64.0.1", TailnetPeer: "peer"}})
	if len(m.Connections) != 1 || bound.ID != "saved" || bound.Name != "我的 Air" || bound.Icon != DeviceIconLaptop {
		t.Fatal("route merge overwrote presentation identity", bound)
	}
	if len(bound.Routes) != 2 {
		t.Fatal("icon changed route merge", bound)
	}
}

func TestDeviceIconDefaultsUseReportedSystem(t *testing.T) {
	for _, tc := range []struct {
		os   string
		want DeviceIcon
	}{{"macOS", DeviceIconMonitor}, {"darwin", DeviceIconMonitor}, {"windows", DeviceIconMonitor}, {"iOS", DeviceIconPhone}, {"Android", DeviceIconPhone}, {"iPadOS", DeviceIconTablet}, {"linux", DeviceIconServer}, {"", DeviceIconMonitor}} {
		if got := DefaultDeviceIcon(tc.os); got != tc.want {
			t.Errorf("%s: %s want %s", tc.os, got, tc.want)
		}
	}
}
