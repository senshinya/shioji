package tailnet

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"tailscale.com/envknob"
	"tailscale.com/logtail"
	"tailscale.com/net/tsdial"
)

// tsnet's internal logger precedes its user callback. Disable both buffering
// and uploads before constructing the node; discarding callbacks alone leaks URLs.
func configurePrivacy() { envknob.SetNoLogsNoSupport(); logtail.Disable() }

// Server.Dial can fall back to host routing if PeerForIP changes. Use only the
// node's installed netstack function. Sys is version-sensitive, pinned to 1.104.4.
func dialNetstack(ctx context.Context, d *tsdial.Dialer, dst netip.AddrPort) (net.Conn, error) {
	if d == nil || d.NetstackDialTCP == nil {
		return nil, errors.New("内嵌网络尚未就绪")
	}
	return d.NetstackDialTCP(ctx, dst)
}
