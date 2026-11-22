package app

import (
	"context"
	"math"
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
	c.networkWG.Add(1)
	go func() {
		defer c.networkWG.Done()
		var client *protocol.Client
		disconnect := func() {}
		defer func() {
			if client != nil {
				client.Close()
			}
			disconnect()
		}()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			call, done := context.WithTimeout(ctx, 2*time.Second)
			if client == nil {
				life, closeTransport := context.WithCancel(ctx)
				stopOpening := context.AfterFunc(call, closeTransport)
				conn, err := dial(life)
				if err == nil {
					candidate, err := protocol.Connect(call, conn, device)
					if err != nil {
						conn.Close()
					} else {
						client = candidate
					}
				}
				stopOpening()
				if client == nil {
					closeTransport()
				} else {
					disconnect = closeTransport
				}
			}
			if client != nil {
				var report domain.DeviceHealth
				err := client.Call(call, "device.health", nil, &report)
				if err != nil {
					client.Close()
					disconnect()
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

// DeviceLoadPercent returns the measured CPU or physical memory percentage.
func DeviceLoadPercent(sample domain.DeviceLoad, memory bool) *float64 {
	if !memory {
		return sample.CPU
	}
	if sample.Memory == nil || sample.Memory.TotalBytes == 0 || sample.Memory.UsedBytes > sample.Memory.TotalBytes {
		return nil
	}
	percent := 100 * float64(sample.Memory.UsedBytes) / float64(sample.Memory.TotalBytes)
	return &percent
}

type DeviceLoadPoint struct{ X, Y float32 }

// DeviceLoadSegments returns points normalized to a fixed 60-second / 0–100% plot. Missing values and
// sampling pauses break the line, rather than inventing zeroes or interpolation.
func DeviceLoadSegments(samples []domain.DeviceLoad, now time.Time, memory bool) [][]DeviceLoadPoint {
	var lines [][]DeviceLoadPoint
	var line []DeviceLoadPoint
	var previous time.Time
	flush := func() {
		if len(line) > 0 {
			lines = append(lines, line)
			line = nil
		}
	}
	for _, sample := range samples {
		elapsed := now.Sub(sample.At).Seconds()
		percent := DeviceLoadPercent(sample, memory)
		if elapsed < 0 || elapsed > 60 || percent == nil || math.IsNaN(*percent) || math.IsInf(*percent, 0) || *percent < 0 || *percent > 100 {
			flush()
			previous = time.Time{}
			continue
		}
		if !previous.IsZero() && (sample.At.Sub(previous) > 5*time.Second || !sample.At.After(previous)) {
			flush()
		}
		line = append(line, DeviceLoadPoint{X: float32(1 - elapsed/60), Y: float32(1 - *percent/100)})
		previous = sample.At
	}
	flush()
	return lines
}
