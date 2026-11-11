package app

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/transport"
)

type StorageTarget struct {
	Directory, DeviceID string
	SSH                 *transport.SSH
}

func (m *Model) StorageTarget(directory, key string) (StorageTarget, error) {
	state := m.DeviceStates[key]
	if state == nil || !state.Connected {
		return StorageTarget{}, errors.New("设备未连接")
	}
	t := StorageTarget{Directory: directory, DeviceID: state.Snapshot.DeviceID}
	if key != "local" {
		for _, c := range m.Connections {
			if c.ID == key {
				ssh := c.SSH
				t.SSH = &ssh
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
func (t StorageTarget) Read(ctx context.Context, clean bool) (domain.StorageReport, error) {
	var out domain.StorageReport
	c, err := (AttachmentTarget{Directory: t.Directory, DeviceID: t.DeviceID, SSH: t.SSH}).connect(ctx)
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
	err = c.Call(ctx, method, nil, &out)
	return out, err
}
