package mobileclient

import (
	"testing"
	"time"

	"shioji/internal/tailnet"
)

func TestTailnetObservationDistinguishesCurrentLastAndUnknown(t *testing.T) {
	now := time.Now()
	peer := tailnet.Peer{ID: "peer", Address: "100.64.0.3", Online: true,
		Measurement: &tailnet.Measurement{Path: "derp", Region: "tpe", RegionName: "Taipei", Milliseconds: 19, ObservedAt: now}}
	endpoint := Endpoint{TailnetPeerID: peer.ID, Address: "100.64.0.3:22"}
	state := tailnet.Snapshot{State: "Running", Peers: []tailnet.Peer{peer}}
	if got := TailnetRouteObservation(endpoint, state, nil, true, now); !got.Current || got.Path != "DERP 中继 · Taipei (tpe)" || got.Latency != "19 ms" {
		t.Fatalf("current route: %+v", got)
	}
	if got := TailnetRouteObservation(endpoint, state, nil, true, now.Add(31*time.Second)); got.Current || got.Latency != "上次 19 ms" {
		t.Fatalf("expired sample: %+v", got)
	}
	if got := TailnetRouteObservation(endpoint, tailnet.Snapshot{State: "Stopped"}, state.Peers, false, now); got.Current || !got.Measured || got.Latency != "上次 19 ms" {
		t.Fatalf("paused route: %+v", got)
	}
	endpoint.Address = "100.64.0.4:22"
	if got := TailnetRouteObservation(endpoint, state, nil, true, now); got.Measured || got.Latency != "—" {
		t.Fatalf("different address used the old sample: %+v", got)
	}
	if got := TailnetPeerObservation(peer, true, now.Add(-time.Second)); got.Measured || got.Latency != "—" {
		t.Fatalf("future sample was accepted: %+v", got)
	}
}
