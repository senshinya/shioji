package app

import (
	"context"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"time"
)

// Telemetry has a separate authenticated, read-only connection. A slow device
// sample cannot occupy the command connection or delay dispatch/path handoff.
func (c *Controller) observeDeviceHealth(ctx context.Context, device, epoch string) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	dial := c.dial
	if c.paths != nil {
		id := c.paths.active()
		dial = func(ctx context.Context) (net.Conn, error) { return c.paths.dialRoute(ctx, id) }
	}
	go func() {
		var client *protocol.Client
		defer func() {
			if client != nil {
				client.Close()
			}
		}()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			call, done := context.WithTimeout(ctx, 2*time.Second)
			if client == nil {
				conn, err := dial(call)
				if err == nil {
					candidate, err := protocol.Connect(call, conn, device)
					if err != nil {
						conn.Close()
					} else {
						client = candidate
					}
				}
			}
			if client != nil {
				var report domain.DeviceHealth
				err := client.Call(call, "device.health", nil, &report)
				if err != nil {
					client.Close()
					client = nil
				} else if report.DeviceID == device {
					received := time.Now()
					update := func(m *Model) {
						if m.Connected && m.ConnectionEpoch == epoch {
							m.SetDeviceHealth(report, received)
						}
					}
					select {
					case c.Updates <- update:
						c.mu.Lock()
						invalidate := c.invalidate
						c.mu.Unlock()
						if invalidate != nil {
							invalidate()
						}
					case <-ctx.Done():
						done()
						return
					}
				}
			}
			done()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return cancel
}
