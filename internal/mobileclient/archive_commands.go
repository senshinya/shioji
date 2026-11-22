package mobileclient

import (
	"context"
	"encoding/json"
	"errors"

	"golang.org/x/crypto/ssh"

	"shioji/internal/protocol"
)

// CallArchiveDevice checks the enrolled worker and its generation. After the
// first command is sent, its result stays with that attempt even on link loss.
func CallArchiveDevice(ctx context.Context, saved SavedDevice, device ArchiveDevice, signer ssh.Signer, route RouteDial,
	method string, input any) (json.RawMessage, error) {
	if device.ID == "" || device.ID != saved.Endpoint.DeviceID || device.Generation == "" || signer == nil {
		return nil, errors.New("需要连接归档所属的设备")
	}
	type result struct {
		data json.RawMessage
		err  error
	}
	out, err := readVerifiedDevice(ctx, saved, saved.Endpoint, signer, route,
		func(ctx context.Context, client *protocol.Client, _ string) (result, error) {
			if client.Generation != device.Generation {
				return result{err: errors.New("执行服务已变化，请重新读取归档")}, nil
			}
			minor := 5
			switch method {
			case "session.archive", "workspace.archive":
			case "archive.configure":
				minor = 40
			case "session.delete", "workspace.delete":
				minor = 40
				deletion, ok := input.(protocol.ArchiveDeleteInput)
				if !ok {
					return result{err: errors.New("无效的归档删除请求")}, nil
				}
				deletion.CloseRuntime = client.Minor >= 44
				input = deletion
			default:
				return result{err: errors.New("无效的归档操作")}, nil
			}
			if client.Minor < minor {
				return result{err: errors.New("请更新所属设备的执行服务")}, nil
			}
			var data json.RawMessage
			callErr := client.Call(ctx, method, input, &data)
			return result{data: data, err: callErr}, nil
		})
	if err != nil {
		return nil, err
	}
	return out.data, out.err
}
