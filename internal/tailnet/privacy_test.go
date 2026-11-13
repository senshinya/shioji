package tailnet

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"tailscale.com/logtail"
	"tailscale.com/net/tsdial"
	"testing"
)

func TestProductionLoggerDropsAuthorizationBeforeBuffering(t *testing.T) {
	configurePrivacy()
	buf := logtail.NewMemoryBuffer(8)
	logger := logtail.NewLogger(logtail.Config{Buffer: buf, Stderr: io.Discard}, func(string, ...any) {})
	defer func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); logger.Shutdown(ctx) }()
	logger.Logf("auth https://login.example/invalid-secret")
	if raw, _ := buf.TryReadLine(); len(raw) != 0 {
		t.Fatal("production logger buffered authorization", string(raw))
	}
}
func TestNetstackDialNeverFallsThroughWhenPeerLeaves(t *testing.T) {
	expected := errors.New("peer no longer in network")
	calls := 0
	d := &tsdial.Dialer{UseNetstackForIP: func(netip.Addr) bool { return false }, NetstackDialTCP: func(context.Context, netip.AddrPort) (net.Conn, error) { calls++; return nil, expected }}
	_, err := dialNetstack(context.Background(), d, netip.MustParseAddrPort("100.64.0.1:22"))
	if !errors.Is(err, expected) || calls != 1 {
		t.Fatal("netstack-only boundary was bypassed", calls, err)
	}
}
