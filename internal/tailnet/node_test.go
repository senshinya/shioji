package tailnet

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
	"tailscale.com/types/key"
	"testing"
	"time"
)

func TestOutboundPeerRemovalClosesOnlyItsStreams(t *testing.T) {
	n := NewOutboundNode(t.TempDir(), nil)
	n.server = &tsnet.Server{}
	peers := []Peer{{ID: "mac", Address: "100.64.0.2", Online: true}, {ID: "other", Address: "100.64.0.3", Online: true}}
	n.publish(Snapshot{State: "Running", Peers: peers}, func(Snapshot) {})
	makeStream := func(peer Peer) (*outboundStream, net.Conn) {
		left, right := net.Pipe()
		t.Cleanup(func() { left.Close(); right.Close() })
		return n.trackOutboundLocked(left, routeKey{peer: peer.ID, address: peer.Address, port: 22}), right
	}
	_, retired := makeStream(peers[0])
	other, live := makeStream(peers[1])
	n.publish(Snapshot{State: "Running", Peers: peers[1:]}, func(Snapshot) {})
	retired.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := retired.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("revoked stream did not close: %v", err)
	}
	live.SetReadDeadline(time.Now().Add(time.Second))
	go other.Write([]byte{'x'})
	var b [1]byte
	if _, err := io.ReadFull(live, b[:]); err != nil || b[0] != 'x' {
		t.Fatalf("unrelated connection was dropped: %q %v", b, err)
	}
	other.Close()
	if len(n.outbound) != 0 {
		t.Fatal("closed connections still retained")
	}
}

func TestTailnetPeerChangingAddressDropsOldMeasurement(t *testing.T) {
	st := &ipnstate.Status{BackendState: "Running", Peer: map[key.NodePublic]*ipnstate.PeerStatus{key.NewNode().Public(): {
		ID: "same", Online: true, InNetworkMap: true, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.3")},
	}}}
	s := snapshotFromStatus(st, map[string]peerMeasurement{"same": {address: "100.64.0.2", sample: &Measurement{Milliseconds: 19}}})
	if len(s.Peers) != 1 || s.Peers[0].Measurement != nil {
		t.Fatalf("the prior address's sample was attributed to a new route: %+v", s.Peers)
	}
}

func TestOutboundPeerDialDoesNotStartANodeOrUseSystemRouting(t *testing.T) {
	n := NewOutboundNode(t.TempDir(), nil)
	// A published peer can outlive the node's netstack during handoff.
	// Dial must fail at the netstack boundary without invoking Server.Start.
	n.server = &tsnet.Server{Dir: n.directory, Hostname: "test-mobile", ControlURL: "http://127.0.0.1:1"}
	defer n.server.Close()
	n.status = Snapshot{State: "Running", Peers: []Peer{{ID: "mac", Address: "100.64.0.2", Online: true}}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	for _, tc := range []struct{ peer, address, want string }{
		{"mac", "100.64.0.2:22", "内嵌网络尚未就绪"},
		{"mac", "100.64.0.2:65536", "Tailnet SSH 地址无效"},
		{"mac", "100.64.0.2:ssh", "Tailnet SSH 地址无效"},
		{"other", "100.64.0.2:22", "Tailnet 设备不可达"},
		{"mac", "100.64.0.3:22", "Tailnet 设备不可达"},
	} {
		_, err := n.DialPeer(ctx, tc.peer, tc.address)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("DialPeer(%q, %q) = %v, want %q", tc.peer, tc.address, err, tc.want)
		}
	}
	if n.server.Sys() != nil {
		t.Fatal("peer dial started a new node")
	}
}

func TestPeerCatalogDoesNotInventRTTOrUseHomeRelayAsPath(t *testing.T) {
	st := &ipnstate.Status{BackendState: "Running", Peer: map[key.NodePublic]*ipnstate.PeerStatus{key.NewNode().Public(): {ID: "peer", HostName: "Air", OS: "macOS", Online: true, InNetworkMap: true, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}, Relay: "tok"}}}
	s := snapshotFromStatus(st, nil)
	if len(s.Peers) != 1 {
		t.Fatal(s)
	}
	p := s.Peers[0]
	if p.Measurement != nil {
		t.Fatal("unknown latency treated as measured", p)
	}
	m := measurementFromPing(&ipnstate.PingResult{LatencySeconds: .04, DERPRegionID: 2, DERPRegionCode: "tok"}, time.Now())
	if m.Path != "derp" || m.Region != "tok" || m.Milliseconds != 40 {
		t.Fatal(m)
	}
	if measurementFromPing(&ipnstate.PingResult{Err: "offline"}, time.Now()) != nil {
		t.Fatal("failed ping treated as zero RTT")
	}
}

func TestPeersOutsideCurrentNetworkMapAreNotDiscoverable(t *testing.T) {
	s := snapshotFromStatus(&ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{key.NewNode().Public(): {ID: "removed", HostName: "Former peer", Online: true, TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.2")}, InNetworkMap: false}}}, nil)
	if len(s.Peers) != 0 {
		t.Fatal("stale peer was offered as a current target")
	}
}
