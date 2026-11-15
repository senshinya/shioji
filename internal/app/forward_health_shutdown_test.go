package app

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type healthShutdownConn struct {
	net.Conn
	health  atomic.Bool
	once    sync.Once
	closing chan struct{}
	release <-chan struct{}
}

func (c *healthShutdownConn) Close() error {
	if c.health.Load() {
		c.once.Do(func() { close(c.closing) })
		<-c.release
	}
	return c.Conn.Close()
}

func TestControllerShutdownWaitsForMergedHealthTransportCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	started, closing := make(chan struct{}), make(chan struct{})
	c := NewController("")
	c.Dial = func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		conn := &healthShutdownConn{Conn: a, closing: closing, release: release}
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
					conn.health.Store(true)
					close(started)
					result(codec, f, domain.DeviceHealth{DeviceID: "worker"})
				}
			}
		}()
		return conn, nil
	}
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("health subscription did not start")
	}
	cancel()
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("health transport did not start cleanup")
	}
	select {
	case <-done:
		t.Fatal("controller returned before the health transport finished cleanup")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("controller did not finish after health cleanup")
	}
}
