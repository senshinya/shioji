package tailnet

import (
	"io"
	"net"
	"net/netip"
	"tailscale.com/tsnet"
	"testing"
	"time"
)

func TestInboundDefaultsOnAndPreservesExplicitOff(t *testing.T) {
	dir := t.TempDir()
	in, err := readIntent(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !in.forwardingEnabled() {
		t.Fatal("inbound not enabled by default")
	}
	off := false
	in.Forwarding = &off
	writeIntent(dir, in)
	restored, err := readIntent(dir)
	if err != nil || restored.forwardingEnabled() {
		t.Fatal("explicit off lost", err)
	}
}
func TestInboundForwardsAnyTCPPortOnlyToLocalAddress(t *testing.T) {
	service, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	port := uint16(service.Addr().(*net.TCPAddr).Port)
	go func() {
		c, e := service.Accept()
		if e == nil {
			defer c.Close()
			io.Copy(c, c)
		}
	}()
	var cb tsnet.FallbackTCPHandler
	removed := false
	f := newInbound(func(h tsnet.FallbackTCPHandler) func() { cb = h; return func() { removed = true } }, func() (netip.Addr, netip.Addr) {
		return netip.MustParseAddr("100.64.0.1"), netip.MustParseAddr("fd7a:115c:a1e0::1")
	})
	defer f.Close()
	if h, ok := cb(netip.AddrPort{}, netip.MustParseAddrPort("192.168.7.119:22")); ok || h != nil {
		t.Fatal("forwarding to another host")
	}
	handler, ok := cb(netip.AddrPort{}, netip.AddrPortFrom(netip.MustParseAddr("100.64.0.1"), port))
	if !ok || handler == nil {
		t.Fatal("non SSH port rejected")
	}
	a, b := net.Pipe()
	go handler(b)
	defer a.Close()
	a.SetDeadline(time.Now().Add(time.Second))
	a.Write([]byte("echo"))
	got := make([]byte, 4)
	if _, err = io.ReadFull(a, got); err != nil || string(got) != "echo" {
		t.Fatal(string(got), err)
	}
	f.Close()
	if !removed {
		t.Fatal("fallback handler remains registered")
	}
	if _, err = a.Read(got); err == nil {
		t.Fatal("active stream survives disable")
	}
}

func TestInboundIPv6NodeCanReachIPv4LoopbackService(t *testing.T) {
	service, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	go func() {
		c, e := service.Accept()
		if e == nil {
			defer c.Close()
			c.Write([]byte("ok"))
		}
	}()
	var cb tsnet.FallbackTCPHandler
	f := newInbound(func(h tsnet.FallbackTCPHandler) func() { cb = h; return func() {} }, func() (netip.Addr, netip.Addr) {
		return netip.MustParseAddr("100.64.0.1"), netip.MustParseAddr("fd7a:115c:a1e0::1")
	})
	defer f.Close()
	handler, ok := cb(netip.AddrPort{}, netip.AddrPortFrom(netip.MustParseAddr("fd7a:115c:a1e0::1"), uint16(service.Addr().(*net.TCPAddr).Port)))
	if !ok {
		t.Fatal("IPv6 node address rejected")
	}
	a, b := net.Pipe()
	defer a.Close()
	go handler(b)
	a.SetDeadline(time.Now().Add(time.Second))
	got := make([]byte, 2)
	if _, err = io.ReadFull(a, got); err != nil || string(got) != "ok" {
		t.Fatal("IPv4-only local service unreachable through node IPv6", err)
	}
}

func TestInboundPreservesHalfCloseWhileClientStillSends(t *testing.T) {
	service, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	received := make(chan string, 1)
	go func() {
		c, e := service.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.Write([]byte("ready"))
		c.(*net.TCPConn).CloseWrite()
		body, _ := io.ReadAll(c)
		received <- string(body)
	}()
	var cb tsnet.FallbackTCPHandler
	f := newInbound(func(h tsnet.FallbackTCPHandler) func() { cb = h; return func() {} }, func() (netip.Addr, netip.Addr) { return netip.MustParseAddr("100.64.0.1"), netip.Addr{} })
	defer f.Close()
	inlet, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer inlet.Close()
	handler, _ := cb(netip.AddrPort{}, netip.AddrPortFrom(netip.MustParseAddr("100.64.0.1"), uint16(service.Addr().(*net.TCPAddr).Port)))
	go func() {
		c, e := inlet.Accept()
		if e == nil {
			handler(c)
		}
	}()
	client, e := net.Dial("tcp", inlet.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	body, e := io.ReadAll(client)
	if e != nil || string(body) != "ready" {
		t.Fatal(string(body), e)
	}
	client.Write([]byte("late body"))
	client.(*net.TCPConn).CloseWrite()
	select {
	case got := <-received:
		if got != "late body" {
			t.Fatal("half-close discarded client body", got)
		}
	case <-time.After(time.Second):
		t.Fatal("half-close stalled")
	}
}
