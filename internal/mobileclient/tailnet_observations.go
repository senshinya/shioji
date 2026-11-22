package mobileclient

import (
	"fmt"
	"net"
	"time"

	"shioji/internal/tailnet"
)

type TailnetObservation struct {
	Path, Latency string
	Measured      bool
	Current       bool
}

func TailnetPeerObservation(peer tailnet.Peer, connected bool, now time.Time) TailnetObservation {
	result := TailnetObservation{Path: "尚未确认", Latency: "—"}
	sample := peer.Measurement
	if sample == nil || sample.Milliseconds <= 0 || sample.ObservedAt.IsZero() || sample.ObservedAt.After(now) {
		return result
	}
	switch sample.Path {
	case "direct":
		result.Path = "直连"
	case "derp":
		result.Path = "DERP 中继"
		if sample.RegionName != "" && sample.Region != "" {
			result.Path += " · " + sample.RegionName + " (" + sample.Region + ")"
		} else if sample.RegionName != "" {
			result.Path += " · " + sample.RegionName
		} else if sample.Region != "" {
			result.Path += " · " + sample.Region
		}
	case "peer-relay":
		result.Path = "设备中继"
	}
	result.Latency = fmt.Sprintf("%.0f ms", sample.Milliseconds)
	if sample.Milliseconds < 1 {
		result.Latency = "<1 ms"
	}
	result.Measured = true
	result.Current = connected && peer.Online && now.Sub(sample.ObservedAt) <= 30*time.Second
	if !result.Current {
		result.Path, result.Latency = "上次 · "+result.Path, "上次 "+result.Latency
	}
	return result
}

// TailnetRouteObservation reads the exact peer and address already bound to a
// saved route. Last observations are display data and never permit a dial.
func TailnetRouteObservation(endpoint Endpoint, state tailnet.Snapshot, last []tailnet.Peer, active bool, now time.Time) TailnetObservation {
	if endpoint.TailnetPeerID != "" {
		address, _, err := net.SplitHostPort(endpoint.Address)
		if err == nil {
			for _, peer := range state.Peers {
				if peer.ID == endpoint.TailnetPeerID && peer.Address == address {
					return TailnetPeerObservation(peer, active && state.State == "Running", now)
				}
			}
			if state.State != "Running" {
				for _, peer := range last {
					if peer.ID == endpoint.TailnetPeerID && peer.Address == address {
						return TailnetPeerObservation(peer, false, now)
					}
				}
			}
		}
	}
	return TailnetPeerObservation(tailnet.Peer{}, false, now)
}
