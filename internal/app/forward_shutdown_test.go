package app

import (
	"context"
	"net"
	"testing"
	"time"

	"shioji/internal/portforward"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

func TestForwardFallbackDoesNotActivateTheRPCPath(t *testing.T) {
	p := testDevicePaths()
	a, b := p.config.Routes[0].ID, p.config.Routes[1].ID
	p.activate(a, time.Now())
	p.record(b, time.Millisecond, time.Now(), nil)
	p.open = func(_ context.Context, s transport.SSH) (net.Conn, error) {
		if s.Target != "mesh" {
			t.Errorf("stream did not select healthy fallback: %s", s.Target)
		}
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			codec, epoch, err := protocol.Welcome(server, "worker")
			if err != nil {
				return
			}
			var f protocol.Frame
			if codec.Read(&f) != nil {
				return
			}
			codec.Write(protocol.Frame{Type: "result", ID: f.ID, DeviceID: "worker", Epoch: epoch})
			var buf [1]byte
			server.Read(buf[:])
		}()
		return client, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, child := NewController(""), NewController("")
	child.ExpectedDeviceID, child.paths = "worker", p
	root.fleet = &deviceFleet{sources: map[string]*deviceSource{"remote": {ctx: ctx, controller: child}}}
	root.initPortForwards(ctx)
	defer root.forwards.Close()
	root.runForwardJob(ctx, Job{Method: "$forward.create", ConnectionID: "remote", Input: ForwardInput{Request: portforward.Request{DeviceID: "worker", Target: portforward.Target{Host: "localhost", Port: 8080}}}})
	deadline := time.After(time.Second)
	for {
		items, _ := root.ForwardSnapshot()
		if len(items) == 1 && items[0].State == "available" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("forward did not become available", items)
		case <-time.After(time.Millisecond):
		}
	}
	if p.active() != a {
		t.Fatal("forward stream replaced the live RPC path without an RPC handoff", p.active(), a)
	}
}

func TestStoppedControllerRejectsJobsAndUnblocksUndrawnUpdates(t *testing.T) {
	c := NewController("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Run(ctx)
	if c.Do(Job{Method: "$select"}) {
		t.Error("stopped controller accepted a job")
	}
	for len(c.Updates) < cap(c.Updates) {
		c.Updates <- func(*Model) {}
	}
	done := make(chan struct{})
	go func() { c.post(func(*Model) {}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		<-c.Updates // Release the pre-fix blocked goroutine.
		t.Fatal("stopped controller waited for a closed window to drain updates")
	}
}

func TestDeviceControllerShutdownWaitsForChildTransportCleanup(t *testing.T) {
	c := NewDeviceController("/unused", "local-device", nil, "", nil)
	child := c.fleet.sources["local"].controller
	child.WorkerAutoUpdate = false
	started, release := make(chan struct{}), make(chan struct{})
	child.Dial = func(ctx context.Context) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		<-release // Model transport.Close waiting for its owned process group.
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		close(release)
		t.Fatal("child never started")
	}
	cancel()
	select {
	case <-done:
		close(release)
		t.Fatal("root exited before child transport cleanup")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("root did not finish after child cleanup")
	}
}
