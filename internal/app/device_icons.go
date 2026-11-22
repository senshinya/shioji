package app

import (
	"fmt"
	"strings"
)

// Device icons are client presentation preferences, not execution identities.
type DeviceIcon string

const (
	DeviceIconAuto    DeviceIcon = ""
	DeviceIconMonitor DeviceIcon = "monitor"
	DeviceIconLaptop  DeviceIcon = "laptop"
	DeviceIconTablet  DeviceIcon = "tablet"
	DeviceIconPhone   DeviceIcon = "phone"
	DeviceIconServer  DeviceIcon = "server"
)

func DefaultDeviceIcon(system string) DeviceIcon {
	switch strings.ToLower(strings.TrimSpace(system)) {
	case "ios", "android":
		return DeviceIconPhone
	case "ipados":
		return DeviceIconTablet
	case "linux":
		return DeviceIconServer
	default:
		return DeviceIconMonitor
	}
}

func (m *Model) DeviceIconChoice(key string) DeviceIcon {
	if key == "local" {
		return m.LocalDeviceIcon
	}
	for _, connection := range m.Connections {
		if connection.ID == key {
			return connection.Icon
		}
	}
	return DeviceIconAuto
}

func (i DeviceIcon) Valid() bool {
	switch i {
	case DeviceIconAuto, DeviceIconMonitor, DeviceIconLaptop, DeviceIconTablet, DeviceIconPhone, DeviceIconServer:
		return true
	default:
		return false
	}
}

func (m *Model) SetDeviceIcon(key string, icon DeviceIcon) error {
	if !icon.Valid() {
		return fmt.Errorf("不支持的设备图标")
	}
	if key == "local" {
		m.LocalDeviceIcon = icon
		return nil
	}
	for i := range m.Connections {
		if m.Connections[i].ID == key {
			m.Connections[i].Icon = icon
			return nil
		}
	}
	return fmt.Errorf("设备不存在")
}
