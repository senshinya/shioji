package main

import (
	"net"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"shioji/internal/mobileclient"
	"shioji/internal/tailnet"
)

func tailnetPeerByID(peers []tailnet.Peer, id string) (tailnet.Peer, bool) {
	index := slices.IndexFunc(peers, func(peer tailnet.Peer) bool { return peer.ID == id })
	if index < 0 {
		return tailnet.Peer{}, false
	}
	return peers[index], true
}

func (a *mobileApp) currentTailnetPeer(id string) (tailnet.Peer, bool) {
	peer, visible := tailnetPeerByID(a.tailnetStatus.Peers, id)
	return peer, visible && a.tailnetStatus.State == "Running" && peer.Online && peer.Address != ""
}

func (a *mobileApp) openTailnetPeer(parent *mygo.Window, id string) {
	var w *mygo.Window
	w = a.page("Tailnet 设备", func(c *ui.Context) {
		n := a.tailnetStatus
		peer, visible := tailnetPeerByID(n.Peers, id)
		if !visible && n.State != "Running" {
			peer, visible = tailnetPeerByID(a.tailnetLastPeers, id)
		}
		if !visible {
			mobilePageBody(c, func() { mobileMessage(c, "此设备已离开当前可见范围。") })
			return
		}
		active := n.State == "Running"
		status := "状态未知"
		if active {
			status = "离线"
			if peer.Online {
				status = "在线"
			}
		}
		seen := mobileclient.TailnetPeerObservation(peer, active, c.Now())
		var footer func()
		if _, available := a.currentTailnetPeer(id); available {
			footer = func() {
				primaryAction(c.Key("pair-tailnet-peer"), "添加设备", func() {
					if current, allowed := a.currentTailnetPeer(id); allowed {
						a.openPair(w, mobileclient.Endpoint{Address: net.JoinHostPort(current.Address, "22"), TailnetPeerID: current.ID})
					}
				})
			}
		}
		mobilePageWithFooter(c, func() {
			mobilePageContext(c, peer.Name, status)
			mobileMetadataField(c.Key("peer-address"), "Tailnet 地址", peer.Address, "复制 Tailnet 设备地址")
			if peer.DNSName != "" && peer.DNSName != peer.Name {
				mobileMetadataField(c.Key("dns-name"), "Tailnet 名称", peer.DNSName, "")
			}
			mobileMetadataField(c.Key("path"), "连接路径", seen.Path, "")
			mobileMetadataField(c.Key("latency"), "往返延迟", seen.Latency, "")
			mobileMetadataField(c.Key("system"), "系统", peer.OS, "")
		}, footer)
	})
	a.pushNative(parent, w)
}
