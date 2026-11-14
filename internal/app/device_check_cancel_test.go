package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"shioji/internal/transport"
)

// A TCP peer that accepts SSH but never sends its banner exercises the actual
// OpenSSH subprocess and its cancellation, without contacting another device.
func silentSSHPeer(t *testing.T) (transport.SSH, <-chan net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	accepted := make(chan net.Conn, 32)
	go func() {
		defer close(accepted)
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	p, _ := strconv.Atoi(port)
	return transport.SSH{Target: "127.0.0.1", Port: p}, accepted
}

func TestDeviceCheckCancelClosesSSHAndAllowsImmediateRetry(t *testing.T) {
	ssh, accepted := silentSSHPeer(t)
	c := NewController("")
	c.fleet = &deviceFleet{sources: map[string]*deviceSource{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	model := NewModel()
	receipts := make(chan error, 32)
	check := func() {
		t.Helper()
		if !c.Do(Job{Method: "$device.check", Input: CheckSSHDevice{SSH: ssh}, Apply: func(_ *Model, _ json.RawMessage, err error) { receipts <- err }}) {
			t.Fatal("check queue full")
		}
	}
	waitPeer := func() net.Conn {
		t.Helper()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case conn := <-accepted:
				t.Cleanup(func() { conn.Close() })
				return conn
			case update := <-c.Updates:
				update(model)
			case err := <-receipts:
				t.Fatalf("retry rejected before opening SSH: %v", err)
			case <-timer.C:
				t.Fatal("new check still waits for the canceled attempt")
			}
		}
	}
	check()
	first := waitPeer()
	c.Do(Job{Method: "$device.cancel"})
	check()
	second := waitPeer()
	first.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.Copy(io.Discard, first); err != nil {
		t.Fatal("cancel left its SSH process connected", err)
	}
	// A late receipt from the first process must not release the second one.
	until := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case update := <-c.Updates:
			update(model)
		case <-until:
			break drain
		}
	}
	c.Do(Job{Method: "$device.cancel"})
	second.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.Copy(io.Discard, second); err != nil {
		t.Fatal("late completion orphaned the current SSH process", err)
	}
}

func TestDeviceCheckUnreachablePeerReturnsWithinTenSeconds(t *testing.T) {
	ssh, accepted := silentSSHPeer(t)
	c := NewController("")
	c.fleet = &deviceFleet{sources: map[string]*deviceSource{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	var received bool
	c.Do(Job{Method: "$device.check", Input: CheckSSHDevice{SSH: ssh}, Apply: func(_ *Model, _ json.RawMessage, err error) {
		if err == nil {
			t.Error("silent SSH peer was accepted")
		}
		received = true
	}})
	deadline := time.After(10500 * time.Millisecond)
	for !received {
		select {
		case conn := <-accepted:
			t.Cleanup(func() { conn.Close() })
		case u := <-c.Updates:
			u(NewModel())
		case <-deadline:
			t.Fatal("SSH check exceeded the ten-second connection deadline")
		}
	}
}
