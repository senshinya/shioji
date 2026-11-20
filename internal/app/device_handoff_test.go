package app

import (
	"context"
	"encoding/json"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"sync/atomic"
	"testing"
	"time"
)

func handoffPipe(ctx context.Context, path string, observe ...func(protocol.Frame)) net.Conn {
	return handoffBuildPipe(ctx, path, domain.WorkerBuild{}, observe...)
}
func handoffBuildPipe(ctx context.Context, path string, build domain.WorkerBuild, observe ...func(protocol.Frame)) net.Conn {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		stop := context.AfterFunc(ctx, func() { server.Close() })
		defer stop()
		codec, _, err := protocol.Welcome(server, "worker", build)
		if err != nil {
			return
		}
		for {
			var f protocol.Frame
			if codec.Read(&f) != nil {
				return
			}
			for _, fn := range observe {
				fn(f)
			}
			switch f.Method {
			case "device.ping":
				result(codec, f, map[string]string{"deviceId": "worker"})
			case "snapshot":
				result(codec, f, domain.Snapshot{DeviceID: "worker", Generation: "generation"})
			case "readiness":
				result(codec, f, domain.Readiness{State: "ready"})
			case "events.watch":
				<-ctx.Done()
				return
			case "events":
				result(codec, f, protocol.EventPage{Generation: "generation"})
			case "probe":
				result(codec, f, path)
			case "session.submit":
				return // Accepted, but its receipt was lost with the old route.
			case "operation":
				result(codec, f, domain.Operation{ID: "op", SessionID: "confirmed-session", State: domain.OperationConfirmed})
			case "device.directories":
				if path == "lan" {
					return
				}
				result(codec, f, map[string]string{"path": "/tmp"})
			}
		}
	}()
	return client
}

func TestDeviceHandoffRefreshesAdmittedWorkerBuild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testDevicePaths()
	p.record(p.config.Routes[0].ID, 4*time.Millisecond, time.Now(), nil)
	p.open = func(_ context.Context, s transport.SSH) (net.Conn, error) {
		build := "old"
		if s.Target == "mesh" {
			build = "updated"
		}
		return handoffBuildPipe(ctx, s.Target, domain.WorkerBuild{BuildID: build, SafeUpdate: true}), nil
	}
	c := NewController("")
	c.ExpectedDeviceID = "worker"
	c.paths = p
	c.Dial = p.dial
	go c.Run(ctx)
	m := NewModel()
	deadline := time.After(3 * time.Second)
	for !m.Connected {
		select {
		case u := <-c.Updates:
			u(m)
		case <-deadline:
			t.Fatal("did not connect")
		}
	}
	if m.WorkerService == nil || m.WorkerService.Build.BuildID != "old" {
		t.Fatal("missing initial build")
	}
	now := time.Now()
	a, b := p.config.Routes[0].ID, p.config.Routes[1].ID
	for i := 0; i < 5; i++ {
		at := now.Add(time.Duration(i-4) * time.Second)
		p.record(a, 40*time.Millisecond, at, nil)
		p.record(b, 5*time.Millisecond, at, nil)
		p.preferred(at)
	}
	for m.WorkerService.Build.BuildID != "updated" {
		select {
		case u := <-c.Updates:
			u(m)
		case <-deadline:
			t.Fatal("new worker was adopted without refreshing service version", m.WorkerService)
		}
	}
}

func TestDeviceHandoffResumesReadOnlyDirectoryQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testDevicePaths()
	p.record(p.config.Routes[0].ID, 4*time.Millisecond, time.Now(), nil)
	var reads atomic.Int32
	p.open = func(_ context.Context, s transport.SSH) (net.Conn, error) {
		return handoffPipe(ctx, s.Target, func(f protocol.Frame) {
			if f.Method == "device.directories" {
				reads.Add(1)
			}
		}), nil
	}
	c := NewController("")
	c.ExpectedDeviceID = "worker"
	c.paths = p
	c.Dial = p.dial
	go c.Run(ctx)
	m := NewModel()
	done := false
	deadline := time.After(3 * time.Second)
	for !m.Connected {
		select {
		case u := <-c.Updates:
			u(m)
		case <-deadline:
			t.Fatal("device did not connect")
		}
	}
	epoch := m.ConnectionEpoch
	var received string
	var receivedErr error
	c.Do(Job{ConnectionEpoch: epoch, Method: "device.directories", Input: protocol.DirectoryQuery{Path: "/tmp"}, Apply: func(_ *Model, b json.RawMessage, err error) {
		receivedErr = err
		var page struct {
			Path string `json:"path"`
		}
		json.Unmarshal(b, &page)
		received = page.Path
		done = true
	}})
	for !done {
		select {
		case u := <-c.Updates:
			u(m)
		case <-deadline:
			t.Fatal("directory query did not finish")
		}
	}
	if receivedErr != nil || received != "/tmp" || reads.Load() != 2 || !m.Connected || m.ConnectionEpoch != epoch {
		t.Fatal("read-only directory query failed instead of following the same worker", received, receivedErr, reads.Load())
	}
}

func TestDeviceHandoffDoesNotReplayUnknownSubmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testDevicePaths()
	p.record(p.config.Routes[0].ID, 4*time.Millisecond, time.Now(), nil)
	var submitted, queried atomic.Int32
	p.open = func(_ context.Context, s transport.SSH) (net.Conn, error) {
		return handoffPipe(ctx, s.Target, func(f protocol.Frame) {
			if f.Method == "session.submit" {
				submitted.Add(1)
			}
			if f.Method == "operation" {
				queried.Add(1)
			}
		}), nil
	}
	c := NewController("")
	c.ExpectedDeviceID = "worker"
	c.paths = p
	c.Dial = p.dial
	go c.Run(ctx)
	m := NewModel()
	track, offline := false, false
	waitFor := func(fn func() bool) {
		t.Helper()
		timeout := time.After(4 * time.Second)
		for !fn() {
			select {
			case u := <-c.Updates:
				u(m)
				if track && !m.Connected {
					offline = true
				}
			case <-timeout:
				t.Fatal("recovery did not complete")
			}
		}
	}
	waitFor(func() bool { return m.Connected })
	track = true
	epoch := m.ConnectionEpoch
	lost, finished := false, false
	var path string
	in := domain.Submission{OperationID: "op"}
	m.Pending[in.OperationID] = in
	c.Do(Job{ConnectionEpoch: epoch, Method: "session.submit", Input: in, Apply: func(_ *Model, _ json.RawMessage, err error) { lost = err != nil }})
	// Unrelated commands now run in parallel. Observe the handoff before
	// requiring a subsequent command to use the replacement route.
	waitFor(func() bool { return lost && m.Resolved[in.OperationID] && p.active() == p.config.Routes[1].ID })
	c.Do(Job{ConnectionEpoch: epoch, Method: "probe", Apply: func(_ *Model, b json.RawMessage, err error) {
		if err != nil {
			t.Error(err)
		}
		json.Unmarshal(b, &path)
		finished = true
	}})
	waitFor(func() bool { return lost && finished && m.Resolved[in.OperationID] })
	if submitted.Load() != 1 || queried.Load() == 0 || path != "mesh" {
		t.Fatal("unknown input replayed, not reconciled, or unsent job lost", submitted.Load(), queried.Load(), path)
	}
	if m.ConnectionEpoch != epoch {
		t.Fatal("same-generation fault lost the queued logical epoch")
	}
	if offline {
		t.Fatal("verified standby was available, but fault handoff reset the entire UI connection")
	}
}

func TestDeviceHandoffOwnsTransportBeyondPreparationTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testDevicePaths()
	closed := make(chan struct{}, 4)
	p.open = func(openCtx context.Context, s transport.SSH) (net.Conn, error) {
		conn := handoffPipe(ctx, s.Target)
		context.AfterFunc(openCtx, func() { conn.Close(); closed <- struct{}{} })
		return conn, nil
	}
	c := NewController("")
	c.ExpectedDeviceID = "worker"
	c.paths = p
	next, _, _, _, err := c.preparePath(ctx, p.config.Routes[1].ID, "generation", 0, protocol.ReadinessQuery{})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	select {
	case <-closed:
		t.Fatal("preparation completion cancelled admitted transport")
	case <-time.After(100 * time.Millisecond):
	}
	var reply string
	if err := next.Call(ctx, "probe", nil, &reply); err != nil || reply != "mesh" {
		t.Fatal("admitted path did not survive preparation", reply, err)
	}
}

func TestDeviceHandoffRejectsChangedWorkerGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testDevicePaths()
	p.open = func(_ context.Context, s transport.SSH) (net.Conn, error) { return handoffPipe(ctx, s.Target), nil }
	c := NewController("")
	c.ExpectedDeviceID = "worker"
	c.paths = p
	active := p.active()
	next, _, _, _, err := c.preparePath(ctx, p.config.Routes[1].ID, "older-generation", 0, protocol.ReadinessQuery{})
	if err == nil || next != nil || p.active() != active {
		t.Fatal("performance switch crossed a worker generation or changed active path on failure")
	}
}

func TestDeviceHandoffAddingRouteKeepsControllerAndEpoch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection := DeviceConnection{ID: "air", DeviceID: "worker", Name: "Air", SSH: transport.SSH{Target: "lan", Worker: "/worker"}}.Normalized()
	root := NewDeviceController(t.TempDir(), "local", []DeviceConnection{connection}, "", nil)
	child := root.fleet.sources["air"].controller
	child.paths.open = func(_ context.Context, s transport.SSH) (net.Conn, error) { return handoffPipe(ctx, s.Target), nil }
	go root.Run(ctx)
	m := NewModel()
	m.LocalDeviceID = "local"
	m.Connections = []DeviceConnection{connection}
	drain := func(fn func() bool) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for !fn() {
			select {
			case u := <-root.Updates:
				u(m)
			case <-deadline:
				t.Fatal("device fleet did not acknowledge configuration")
			}
		}
	}
	drain(func() bool { return m.DeviceStates["air"] != nil && m.DeviceStates["air"].Connected })
	epoch := m.DeviceStates["air"].ConnectionEpoch
	next := m.BindDeviceConnection(DeviceConnection{ID: "other", DeviceID: "worker", SSH: transport.SSH{Target: "mesh", Worker: "/worker"}})
	finished := false
	root.Do(Job{Method: "$device.configure", Input: ConfigureDevice{Connection: next}, Apply: func(_ *Model, _ json.RawMessage, err error) {
		if err != nil {
			t.Error(err)
		}
		finished = true
	}})
	drain(func() bool { return finished })
	if root.fleet.sources["air"].controller != child || m.DeviceStates["air"].ConnectionEpoch != epoch || !m.DeviceStates["air"].Connected {
		t.Fatal("adding path restarted the logical device")
	}
}

func TestDeviceHandoffKeepsLogicalEpochAndUsesFasterPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testDevicePaths()
	p.open = func(_ context.Context, s transport.SSH) (net.Conn, error) { return handoffPipe(ctx, s.Target), nil }
	c := NewController("")
	c.ExpectedDeviceID = "worker"
	c.paths = p
	c.Dial = p.dial
	go c.Run(ctx)
	m := NewModel()
	drain := func(until func() bool) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for !until() {
			select {
			case u := <-c.Updates:
				u(m)
			case <-deadline:
				t.Fatal("controller condition timed out", m.Connected, m.ConnectionEpoch)
			}
		}
	}
	drain(func() bool { return m.Connected })
	epoch := m.ConnectionEpoch
	a, b := p.config.Routes[0].ID, p.config.Routes[1].ID
	now := time.Now()
	for i := 0; i < 5; i++ {
		at := now.Add(time.Duration(i-4) * time.Second)
		p.record(a, 40*time.Millisecond, at, nil)
		p.record(b, 5*time.Millisecond, at, nil)
		p.preferred(at)
	}
	var route string
	for route != "mesh" {
		finished := false
		if !c.Do(Job{ConnectionEpoch: epoch, Method: "probe", Apply: func(_ *Model, body json.RawMessage, err error) {
			if err != nil {
				t.Error(err)
			}
			json.Unmarshal(body, &route)
			finished = true
		}}) {
			t.Fatal("job rejected")
		}
		drain(func() bool { return finished })
		if time.Since(now) > 2*time.Second {
			t.Fatal("faster route never became business path", route)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if m.ConnectionEpoch != epoch || !m.Connected {
		t.Fatal("path handoff reset the logical connection")
	}
}
