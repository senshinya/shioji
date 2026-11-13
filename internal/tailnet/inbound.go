package tailnet

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"tailscale.com/tsnet"
	"time"
)

// A fallback accepts only this embedded node's destination addresses and maps
// the same TCP port to loopback. Tailnet policy and each service's own auth apply.
// It never routes to LAN peers, changes Remote Login, or exposes Unix sockets.
type inboundForwarder struct {
	relay      *bridge
	unregister func()
	once       sync.Once
}

func newInbound(register func(tsnet.FallbackTCPHandler) func(), ips func() (netip.Addr, netip.Addr)) *inboundForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	f := &inboundForwarder{relay: &bridge{ctx: ctx, cancel: cancel, streams: map[net.Conn]bool{}, slots: make(chan struct{}, 64)}}
	f.unregister = register(func(src, dst netip.AddrPort) (func(net.Conn), bool) {
		v4, v6 := ips()
		if dst.Port() == 0 || !dst.Addr().IsValid() || (dst.Addr() != v4 && dst.Addr() != v6) {
			return nil, false
		}
		host := "127.0.0.1"
		if dst.Addr().Is6() {
			host = "::1"
		}
		target := net.JoinHostPort(host, strconv.Itoa(int(dst.Port())))
		alternate := "::1"
		if host == "::1" {
			alternate = "127.0.0.1"
		}
		other := net.JoinHostPort(alternate, strconv.Itoa(int(dst.Port())))
		return func(c net.Conn) {
			select {
			case f.relay.slots <- struct{}{}:
				f.relay.serve(c, func(ctx context.Context) (net.Conn, error) {
					dialer := &net.Dialer{Timeout: 3 * time.Second}
					c, err := dialer.DialContext(ctx, "tcp", target)
					if err == nil {
						return c, nil
					}
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					return dialer.DialContext(ctx, "tcp", other)
				})
			default:
				c.Close()
			}
		}, true
	})
	return f
}
func (f *inboundForwarder) Close() {
	f.once.Do(func() {
		if f.unregister != nil {
			f.unregister()
		}
		f.relay.Close()
	})
}
