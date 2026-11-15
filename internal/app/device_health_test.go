package app

import (
	"context"
	"encoding/json"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"sync"
	"testing"
	"time"
)

func TestHealthPollingKeepsTransportUntilSubscriptionEnds(t *testing.T) {
	for _, routed := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "routed"}[routed], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := NewController("")
			dial := func(lifetime context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				stop := context.AfterFunc(lifetime, func() { client.Close() })
				go func() {
					defer stop()
					defer server.Close()
					codec, _, err := protocol.Welcome(server, "device")
					if err != nil {
						return
					}
					for revision := int64(1); ; revision++ {
						var frame protocol.Frame
						if codec.Read(&frame) != nil {
							return
						}
						result(codec, frame, domain.DeviceHealth{DeviceID: "device", ObservedAt: time.Now(), KeepAwake: domain.KeepAwakeState{Revision: revision}})
					}
				}()
				return client, nil
			}
			c.Dial = dial
			if routed {
				c.paths = newDevicePaths(DeviceConnection{Routes: []DeviceRoute{{ID: "route", SSH: transport.SSH{Target: "host"}}}, ActiveRouteID: "route"}, func(ctx context.Context, _ transport.SSH) (net.Conn, error) { return dial(ctx) })
			}
			stop := c.observeDeviceHealth(ctx, "device", "epoch")
			defer func() { stop(); c.networkWG.Wait() }()
			m := NewModel()
			m.Connected, m.ConnectionEpoch = true, "epoch"
			deadline := time.After(3 * time.Second)
			for m.DeviceHealth == nil || m.DeviceHealth.KeepAwake.Revision < 2 {
				select {
				case update := <-c.Updates:
					update(m)
				case <-deadline:
					t.Fatal("health subscription lost its transport between polls")
				}
			}
		})
	}
}

func TestDeviceHealthFollowsOwningWorkspaceWithoutCrossDeviceLeak(t *testing.T) {
	m := NewModel()
	m.LocalDeviceID = "A"
	m.Connections = []DeviceConnection{{ID: "remote", DeviceID: "B", Name: "Studio"}}
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.Snapshot = domain.Snapshot{DeviceID: "A", Workspaces: []domain.Workspace{{ID: "wa", DeviceID: "A"}}}
		p.DeviceHealth = &domain.DeviceHealth{DeviceID: "A", ObservedAt: time.Now(), KeepAwake: domain.KeepAwakeState{Enabled: false}}
	})
	m.ApplyDeviceUpdate("remote", func(p *Model) {
		p.Snapshot = domain.Snapshot{DeviceID: "B", Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B"}}}
		p.DeviceHealth = &domain.DeviceHealth{DeviceID: "B", ObservedAt: time.Now(), KeepAwake: domain.KeepAwakeState{Enabled: true}}
	})
	m.WorkspaceID = "wb"
	m.RefreshDevices()
	if m.DeviceHealth == nil || m.DeviceHealth.DeviceID != "B" || !m.DeviceHealth.KeepAwake.Enabled {
		t.Fatal("remote workspace shows local health")
	}
	m.WorkspaceID = "wa"
	m.RefreshDevices()
	if m.DeviceHealth == nil || m.DeviceHealth.DeviceID != "A" || m.DeviceHealth.KeepAwake.Enabled {
		t.Fatal("local workspace inherited remote assertion")
	}
}

func TestHealthHistoryUsesRelativeWorkerTimesDespiteClockSkew(t *testing.T) {
	m := NewModel()
	received := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	worker := received.Add(5 * time.Hour)
	m.SetDeviceHealth(domain.DeviceHealth{ObservedAt: worker, Samples: []domain.DeviceLoad{{At: worker.Add(-12 * time.Second)}}}, received)
	if !m.DeviceHealth.ObservedAt.Equal(received) || !m.DeviceHealth.Samples[0].At.Equal(received.Add(-12*time.Second)) {
		t.Fatal("worker clock skew broke graph freshness", m.DeviceHealth)
	}
}

func TestUnresponsiveTelemetryDoesNotBlockDeviceCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	controller := NewController("")
	controller.Dial = func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			stop := context.AfterFunc(ctx, func() { b.Close() })
			defer stop()
			codec, _, err := protocol.Welcome(b, "worker")
			if err != nil {
				return
			}
			for {
				var f protocol.Frame
				if codec.Read(&f) != nil {
					return
				}
				switch f.Method {
				case "snapshot":
					result(codec, f, domain.Snapshot{DeviceID: "worker", Generation: "g"})
				case "readiness":
					result(codec, f, domain.Readiness{State: "ready"})
				case "events.watch":
					<-ctx.Done()
					return
				case "device.health":
					once.Do(func() { close(started) })
					<-ctx.Done()
					return
				case "probe":
					result(codec, f, "accepted")
				}
			}
		}()
		return a, nil
	}
	go controller.Run(ctx)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no telemetry request")
	}
	accepted := false
	controller.Do(Job{Method: "probe", Apply: func(_ *Model, _ json.RawMessage, err error) { accepted = err == nil }})
	m := NewModel()
	deadline := time.After(150 * time.Millisecond)
	for !accepted {
		select {
		case update := <-controller.Updates:
			update(m)
		case <-deadline:
			t.Fatal("telemetry blocks execution commands")
		}
	}
}

func TestDelayedHealthPollCannotUndoNewerKeepAwakeReceipt(t *testing.T) {
	m := NewModel()
	now := time.Now()
	m.SetDeviceHealth(domain.DeviceHealth{DeviceID: "B", ObservedAt: now, KeepAwake: domain.KeepAwakeState{Available: true, Enabled: true, Revision: 8}}, now)
	m.SetDeviceHealth(domain.DeviceHealth{DeviceID: "B", ObservedAt: now.Add(-time.Second), KeepAwake: domain.KeepAwakeState{Available: true, Enabled: false, Revision: 7}}, now.Add(time.Second))
	if !m.DeviceHealth.KeepAwake.Enabled || m.DeviceHealth.KeepAwake.Revision != 8 {
		t.Fatal("delayed telemetry undid authoritative mutation")
	}
}
