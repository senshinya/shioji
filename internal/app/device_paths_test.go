package app

import (
	"context"
	"errors"
	"net"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"sync/atomic"
	"testing"
	"time"
)

func TestDevicePathsReportEachSSHLoginEvenWhenOffline(t *testing.T) {
	c := DeviceConnection{DeviceID: "worker", SSH: transport.SSH{Target: "alice@192.0.2.10", Port: 2222}, OtherSSH: []transport.SSH{{Target: "bob@100.64.0.10", Port: 2200, TailnetPeer: "peer"}}}.Normalized()
	p := newDevicePaths(c, func(context.Context, transport.SSH) (net.Conn, error) { return nil, net.ErrClosed })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		states := p.states(time.Now())
		if states[0].Endpoint != nil && states[1].Endpoint != nil {
			if states[0].Endpoint.User != "alice" || states[0].Endpoint.Port != 2222 || states[1].Endpoint.User != "bob" || states[1].Endpoint.Port != 2200 {
				t.Fatal("login metadata crossed routes", states)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("offline routes lost configured login metadata", states)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDevicePathsProbeIdentityAndCancellation(t *testing.T) {
	p := testDevicePaths()
	var live atomic.Int32
	p.open = func(ctx context.Context, s transport.SSH) (net.Conn, error) {
		client, server := net.Pipe()
		live.Add(1)
		go func() {
			defer live.Add(-1)
			defer server.Close()
			identity := "worker"
			if s.Target == "mesh" {
				identity = "wrong-worker"
			}
			codec, epoch, err := protocol.Welcome(server, identity)
			if err != nil {
				return
			}
			for {
				var frame protocol.Frame
				if codec.Read(&frame) != nil {
					return
				}
				body := []byte(`{"deviceId":"worker"}`)
				if frame.Method != "device.ping" {
					t.Errorf("probe invoked business method %s", frame.Method)
					return
				}
				if codec.Write(protocol.Frame{Type: "result", ID: frame.ID, DeviceID: identity, Epoch: epoch, Body: body}) != nil {
					return
				}
			}
		}()
		return client, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := p.states(time.Now())
		if s[0].Timing != nil && s[1].Phase == PathUnavailable {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("probe accepted wrong identity or never completed", s)
		}
		time.Sleep(time.Millisecond)
	}
	if p.preferred(time.Now()) != p.config.Routes[0].ID {
		t.Fatal("foreign worker selected")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("probe channels did not close")
	}
	deadline = time.Now().Add(time.Second)
	for live.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("probe transport leaked")
		}
		time.Sleep(time.Millisecond)
	}
}

func testDevicePaths() *devicePaths {
	c := DeviceConnection{DeviceID: "worker", SSH: transport.SSH{Target: "lan", Worker: "/worker"}, OtherSSH: []transport.SSH{{Target: "mesh", Worker: "/worker"}}}.Normalized()
	return newDevicePaths(c, nil)
}

func TestDevicePathSelectionRejectsJitterAndStaleSamples(t *testing.T) {
	p := testDevicePaths()
	a, b := p.config.Routes[0].ID, p.config.Routes[1].ID
	start := time.Unix(100, 0)
	for i := 0; i < 5; i++ {
		now := start.Add(time.Duration(i) * time.Second)
		p.record(a, 10*time.Millisecond, now, nil)
		p.record(b, 9*time.Millisecond, now, nil)
		if p.preferred(now) != a {
			t.Fatal("insignificant jitter changed route")
		}
	}
	for i := 0; i < 5; i++ {
		now := start.Add(time.Duration(i+5) * time.Second)
		p.record(a, 40*time.Millisecond, now, nil)
		p.record(b, 8*time.Millisecond, now, nil)
		p.preferred(now)
	}
	now := start.Add(10 * time.Second)
	p.record(a, 40*time.Millisecond, now, nil)
	p.record(b, 8*time.Millisecond, now, nil)
	if p.preferred(now) != b {
		t.Fatal("consistently faster route was not selected")
	}
	p.activate(b, now)
	for i := 0; i < 6; i++ {
		at := now.Add(time.Duration(i+1) * time.Second)
		p.record(a, time.Millisecond, at, nil)
		p.record(b, 30*time.Millisecond, at, nil)
		if p.preferred(at) != b {
			t.Fatal("cooldown did not prevent oscillation")
		}
	}
	p.record(b, 0, now.Add(7*time.Second), errors.New("link lost"))
	if p.preferred(now.Add(7*time.Second)) != a {
		t.Fatal("failed current route did not bypass cooldown")
	}
	p.activate(a, now.Add(7*time.Second))
	for _, s := range p.states(now.Add(time.Minute)) {
		if s.Timing != nil {
			t.Fatal("stale latency displayed as current")
		}
	}
}

func TestDevicePathNeedsNewObservationsBeforeSwitch(t *testing.T) {
	p := testDevicePaths()
	a, b := p.config.Routes[0].ID, p.config.Routes[1].ID
	now := time.Unix(100, 0)
	for i := 0; i < 3; i++ {
		p.record(a, 40*time.Millisecond, now, nil)
		p.record(b, 5*time.Millisecond, now, nil)
	}
	for i := 0; i < 20; i++ {
		if p.preferred(now) != a {
			t.Fatal("UI ticks counted as independent observations")
		}
	}
}

func TestDevicePathRemovingActiveWaitsForHandoff(t *testing.T) {
	p := testDevicePaths()
	a, b := p.config.Routes[0], p.config.Routes[1]
	p.record(a.ID, time.Millisecond, time.Now(), nil)
	p.record(b.ID, 4*time.Millisecond, time.Now(), nil)
	config := p.config
	config.Routes = []DeviceRoute{b}
	p.configure(config)
	if p.active() != a.ID {
		t.Fatal("removing a route silently changed the business transport")
	}
	if _, ok := p.ssh(a.ID); !ok {
		t.Fatal("old route unavailable before admitted handoff")
	}
	if p.preferred(time.Now()) != b.ID {
		t.Fatal("removed route still wins routing")
	}
	p.activate(b.ID, time.Now())
	if _, ok := p.ssh(a.ID); ok {
		t.Fatal("retired route retained after handoff")
	}
}

func TestDevicePathsKeepProbeTransportBeyondSampleTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testDevicePaths()
	closed := make(chan struct{}, 4)
	p.open = func(openCtx context.Context, s transport.SSH) (net.Conn, error) {
		conn := handoffPipe(ctx, s.Target)
		context.AfterFunc(openCtx, func() { conn.Close(); closed <- struct{}{} })
		return conn, nil
	}
	done := make(chan struct{})
	go func() { p.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(time.Second)
	for {
		ready := 0
		for _, s := range p.states(time.Now()) {
			if s.Timing != nil {
				ready++
			}
		}
		if ready == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probes did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-closed:
		t.Fatal("successful sample cancelled persistent probe connection")
	case <-time.After(100 * time.Millisecond):
	}
}
