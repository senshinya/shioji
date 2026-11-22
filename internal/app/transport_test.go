package app

import (
	"context"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestControllerUsesSameTransportForCommandsAndWatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	c := NewController("/does/not/exist")
	c.Dial = func(ctx context.Context) (net.Conn, error) {
		calls.Add(1)
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			stop := context.AfterFunc(ctx, func() { b.Close() })
			defer stop()
			codec, _, err := protocol.Welcome(b, "remote")
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
					result(codec, f, domain.Snapshot{DeviceID: "remote", Generation: "g"})
				case "readiness":
					result(codec, f, domain.Readiness{State: "ready"})
				case "events.watch":
					<-ctx.Done()
					return
				}
			}
		}()
		return a, nil
	}
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	m := NewModel()
	deadline := time.After(2 * time.Second)
	for !m.Connected || calls.Load() < 2 {
		select {
		case update := <-c.Updates:
			update(m)
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatal("controller does not use injected transport for command and watch", calls.Load())
		}
	}
	if m.KnownDeviceID != "remote" {
		t.Fatal("wrong remote identity", m.KnownDeviceID)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("remote controller did not stop")
	}
}

func TestControllerRejectsSnapshotWithDifferentWorkerIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewController("/unused")
	c.Dial = func(ctx context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			stop := context.AfterFunc(ctx, func() { b.Close() })
			defer stop()
			codec, _, err := protocol.Welcome(b, "verified-A")
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
					result(codec, f, domain.Snapshot{DeviceID: "different-B", Generation: "g"})
				case "readiness":
					result(codec, f, domain.Readiness{State: "ready"})
				default:
					return
				}
			}
		}()
		return a, nil
	}
	go c.Run(ctx)
	m := NewModel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-c.Updates:
			update(m)
			if m.Connected {
				t.Fatal("snapshot silently rebound verified worker identity", m.KnownDeviceID)
			}
			if strings.Contains(m.ConnectionError, "identity") {
				if m.Error != "" || m.Snapshot.DeviceID != "" {
					t.Fatalf("identity failure changed operation feedback or the device projection: error=%q, device=%q", m.Error, m.Snapshot.DeviceID)
				}
				return
			}
		case <-deadline:
			t.Fatal("missing worker identity mismatch")
		}
	}
}
