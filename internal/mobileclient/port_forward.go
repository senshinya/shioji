package mobileclient

import (
	"context"
	"errors"
	"net"
	"slices"
	"time"

	"golang.org/x/crypto/ssh"
	"shioji/internal/portforward"
	"shioji/internal/protocol"
)

// OpenDeviceTCP tries the enrolled routes for one device. OpenTCP checks the
// worker's identity before sending a request to open the target.
func OpenDeviceTCP(ctx context.Context, device SavedDevice, preferred Endpoint, signer ssh.Signer, route RouteDial, target portforward.Target) (net.Conn, error) {
	id := device.Endpoint.DeviceID
	if id == "" {
		return nil, errors.New("执行设备尚未验证")
	}
	endpoints := slices.Clone(device.Endpoints())
	if i := slices.Index(endpoints, preferred); i > 0 {
		endpoints[0], endpoints[i] = endpoints[i], endpoints[0]
	}
	lastErr := errors.New("没有已验证的连接路径")
	for _, endpoint := range endpoints {
		if endpoint.DeviceID != id {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		stream, err := protocol.OpenTCP(attempt, func(call context.Context) (net.Conn, error) {
			return endpoint.DialRoute(call, signer, route)
		}, id, target)
		cancel()
		if err == nil {
			return stream, nil
		}
		var rejection *protocol.RemoteError
		if errors.As(err, &rejection) {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
	}
	return nil, lastErr
}
