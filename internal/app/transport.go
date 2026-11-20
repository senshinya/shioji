package app

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (c *Controller) dial(ctx context.Context) (net.Conn, error) {
	if c.Dial != nil {
		return c.Dial(ctx)
	}
	return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", filepath.Join(c.Directory, "worker.sock"))
}

func (c *Controller) activeWorkerDial() func(context.Context) (net.Conn, error) {
	if c.paths == nil {
		return c.dial
	}
	route := c.paths.active()
	return func(ctx context.Context) (net.Conn, error) { return c.paths.dialRoute(ctx, route) }
}

func connectWorkerGeneration(ctx context.Context, dial func(context.Context) (net.Conn, error), device, generation string) (*protocol.Client, error) {
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// A remote SSH process must outlive the handshake deadline.
	peer, err := protocol.Dial(call, func(context.Context) (net.Conn, error) { return dial(ctx) }, device)
	if err != nil {
		return nil, err
	}
	observed := peer.Generation
	if observed == "" {
		var snapshot domain.Snapshot
		snapshot, err = peer.ReadSnapshot(call)
		if err == nil && snapshot.DeviceID == peer.DeviceID {
			observed = snapshot.Generation
		}
	}
	if err == nil && observed != "" && observed == generation {
		return peer, nil
	}
	peer.Close()
	if err != nil {
		return nil, err
	}
	return nil, errors.New("执行服务身份或运行代次已改变")
}
