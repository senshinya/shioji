package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/tailnet"
	"testing"
	"time"
)

// These are the real native UI renderer with synthetic network status, not proof
// of a live Tailnet login, RTT or peer connection.
func TestTailnetNativeAppearance(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, context := range []string{"welcome", "add", "network"} {
			m := welcomeModel()
			if context != "welcome" {
				m.FirstRun = false
			}
			v := New(m, app.NewController(""), "")
			v.devices = context != "welcome"
			v.deviceSelected = "tailnet"
			if context == "add" {
				v.deviceSelected = "local"
			}
			if context == "welcome" {
				entry := n.NewTester(v.View, 1280, 800)
				entry.SetDark(dark)
				entry.Advance(time.Second)
				theme := "light"
				if dark {
					theme = "dark"
				}
				saveNativeSkillImage(t, entry, "tailnet-welcome-entry-"+theme+".png")
			}
			v.beginTailnet()
			if context == "network" {
				v.sshCheck = sshClosed
				v.welcome = nil
			}
			ui := n.NewTester(v.View, 1280, 800)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			theme := "light"
			if dark {
				theme = "dark"
			}
			saveNativeSkillImage(t, ui, "tailnet-"+context+"-join-"+theme+".png")
			v.networkUI().joining = true
			v.networkUI().joinID = "fixture-join"
			m.Tailnet = tailnet.Snapshot{State: "NeedsLogin", Authorizing: true, OperationID: "fixture-join", AuthURL: "https://login.tailscale.com/a/invalid-fixture"}
			ui.Frame()
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "tailnet-"+context+"-auth-"+theme+".png")
			if context == "network" {
				m.Tailnet = tailnet.Snapshot{State: "Running", InboundEnabled: true, OperationID: "fixture-join", Hostname: "shioji-mac", Network: "senshinya.github", Address: "100.76.14.28"}
				ui.Frame()
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, "tailnet-network-connected-"+theme+".png")
				continue
			}
			m.Tailnet = tailnet.Snapshot{State: "Running", InboundEnabled: true, OperationID: "fixture-join", Network: "senshinya.github", Peers: []tailnet.Peer{{ID: "one", Name: "MacBook Air", Address: "100.87.24.16", OS: "macOS", Online: true}, {ID: "two", Name: "Mac Studio", Address: "100.91.6.42", OS: "macOS", Online: true}, {ID: "three", Name: "Mac mini", Address: "100.74.18.9", OS: "macOS", Online: false}, {ID: "four", Name: "MacBook Pro", Address: "100.81.12.34", OS: "macOS", Online: false}}}
			ui.Frame()
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "tailnet-"+context+"-peers-"+theme+".png")
			ui.Click("选择 Tailnet 设备：MacBook Air")
			if context == "welcome" {
				ui.Click("继续")
			} else {
				ui.Click("连接设备")
			}
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "tailnet-"+context+"-ssh-"+theme+".png")
		}
	}
}
