package protocol

import (
	"context"
	"errors"
	"net"

	"shioji/internal/portforward"
)

const TCPStreamVersion = 1
const TCPStreamMethod = "connection.tcp.open"

// OpenTCP upgrades a fresh authenticated device connection after a successful
// owning-worker dial. The returned stream carries bytes, never RPC frames.
func OpenTCP(ctx context.Context, dial func(context.Context) (net.Conn, error), deviceID string, target portforward.Target) (net.Conn, error) {
	if deviceID == "" {
		return nil, errors.New("端口转发需要已确认的设备身份")
	}
	var err error
	target, err = target.Normalize()
	if err != nil {
		return nil, err
	}
	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			conn.Close()
		}
	}()
	client, err := Connect(ctx, conn, deviceID)
	if err != nil {
		return nil, err
	}
	if client.TCPStreamVersion != TCPStreamVersion {
		return nil, errors.New("更新设备执行服务后才能使用端口转发")
	}
	if err = client.Call(ctx, TCPStreamMethod, target, nil); err != nil {
		return nil, err
	}
	failed = false
	return conn, nil
}
