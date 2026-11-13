package tailnet

import (
	"net/netip"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
	"testing"
	"time"
)

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
