package app

import (
	"context"
	"errors"
	"net"
	"shioji/internal/domain"
	"shioji/internal/transport"
)

type StorageTarget struct {
	Directory, DeviceID string
	SSH                 *transport.SSH
	dial                func(context.Context) (net.Conn, error)
}

func RemoteStorageTarget(device string, dial func(context.Context) (net.Conn, error)) StorageTarget {
	return StorageTarget{DeviceID: device, dial: dial}
}

func (m *Model) StorageTarget(directory, key string) (StorageTarget, error) {
	state := m.DeviceStates[key]
	if state == nil || !state.Connected {
		return StorageTarget{}, errors.New("设备未连接")
	}
	t := StorageTarget{Directory: directory, DeviceID: state.Snapshot.DeviceID}
	if key != "local" {
		t.dial = state.dialStream
		for _, c := range m.Connections {
			if c.ID == key {
				if t.dial == nil {
					ssh := c.ActiveSSH()
					t.SSH = &ssh
				}
				t.DeviceID = c.DeviceID
				return t, nil
			}
		}
		return t, errors.New("设备连接不存在")
	}
	if t.DeviceID == "" {
		t.DeviceID = m.LocalDeviceID
	}
	return t, nil
}

// Dedicated authenticated stream: scanning and cleanup never queue in front of
// Stop, answers or the main event subscription. The device ID is authenticated;
// connection epochs belong to individual streams and are checked by the UI's
// result owner, never compared between this stream and its primary connection.
func (t StorageTarget) Read(ctx context.Context, clean bool, refresh ...bool) (domain.StorageReport, error) {
	var out domain.StorageReport
	c, err := (AttachmentTarget{Directory: t.Directory, DeviceID: t.DeviceID, SSH: t.SSH, dial: t.dial}).connect(ctx)
	if err != nil {
		return out, err
	}
	defer c.Close()
	if c.Minor < 31 {
		return out, errors.New("更新执行服务后可管理存储")
	}
	method := "storage.usage"
	if clean {
		method = "storage.clean"
	}
	force := len(refresh) > 0 && refresh[0]
	err = c.Call(ctx, method, struct {
		Refresh bool `json:"refresh"`
	}{force}, &out)
	return out, err
}
