package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/tailnet"
	"testing"
	"time"
)

func TestTailnetNetworkValueSharesLabelInkCenter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := New(welcomeModel(), app.NewController(""), "")
		v.beginTailnet()
		ui := n.NewTester(v.View, 1280, 800)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		ink := func(text string) float64 {
			r, ok := ui.Find(text)
			if !ok {
				t.Fatal(text)
			}
			lo, hi := 10000, -1
			im := ui.Image()
			for y := int(r.Y); y < int(r.Y+r.H)+1; y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					p := im.RGBAAt(x, y)
					hit := p.R < 190
					if dark {
						hit = p.R > 100
					}
					if hit {
						if y < lo {
							lo = y
						}
						if y > hi {
							hi = y
						}
					}
				}
			}
			if hi < 0 {
				t.Fatal("no ink", text, r)
			}
			return float64(lo+hi) / 2
		}
		if a, b := ink("网络服务"), ink("Tailscale"); math.Abs(a-b) > 1 {
			t.Fatal("network value ink center", dark, a, b)
		}
	}
}

func TestWelcomeEntryRowsShareHoverInsets(t *testing.T) {
	v := New(welcomeModel(), app.NewController(""), "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	content, _ := ui.Find("欢迎内容")
	for _, name := range []string{"这台 Mac", "通过 SSH 连接", "从 Tailnet 添加"} {
		row, ok := ui.Find(name)
		if !ok || math.Abs(float64(row.X-content.X+8)) > 1 || math.Abs(float64(row.W-content.W-16)) > 1 {
			t.Fatal("entry hover geometry differs by route", name, content, row)
		}
	}
}

func TestTailnetValuesAlignWithControlOuterEdgeAndCompactHeading(t *testing.T) {
	for _, context := range []string{"welcome", "add", "network"} {
		m := welcomeModel()
		m.FirstRun = context == "welcome"
		v := New(m, app.NewController(""), "")
		v.devices = context != "welcome"
		v.deviceSelected = "local"
		v.beginTailnet()
		if context == "network" {
			v.deviceSelected = "tailnet"
			v.sshCheck = sshClosed
			v.welcome = nil
		}
		ui := n.NewTester(v.View, 1280, 800)
		ui.Advance(time.Second)
		input, _ := ui.Find("Tailnet 节点名称")
		for _, name := range []string{"Tailscale", "浏览器授权"} {
			r, ok := ui.Find(name)
			if !ok || math.Abs(float64(r.X-input.X)) > 1 {
				t.Errorf("%s: %s left edge=%v, field=%v", context, name, r, input)
			}
		}
		if context != "network" {
			heading, _ := ui.Find("连接 Tailnet")
			label, _ := ui.Find("网络服务")
			if gap := label.Y - (heading.Y + heading.H); gap > 22 {
				t.Errorf("%s excessive heading gap %v", context, gap)
			}
		}
	}
}

func TestTailnetAuthorizationSharesLabelBaseline(t *testing.T) {
	for _, context := range []string{"welcome", "add", "network"} {
		for _, dark := range []bool{false, true} {
			for _, pending := range []bool{false, true} {
				m := welcomeModel()
				m.FirstRun = context == "welcome"
				v := New(m, app.NewController(""), "")
				v.devices = context != "welcome"
				v.deviceSelected = "local"
				v.beginTailnet()
				if context == "network" {
					v.deviceSelected = "tailnet"
					v.sshCheck = sshClosed
					v.welcome = nil
				}
				caption := "浏览器授权"
				if pending {
					caption = "等待浏览器授权"
					v.networkUI().joining = true
					v.networkUI().joinID = "join"
					m.Tailnet = tailnet.Snapshot{State: "NeedsLogin", Authorizing: true, OperationID: "join", AuthURL: "https://login.tailscale.com/a/invalid-fixture"}
				}
				family := ""
				ui := n.NewTester(func(c *n.Context) { family = c.Theme().Font; v.View(c) }, 1280, 800)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				baseline := func(text string, size float32) float32 {
					r, ok := ui.Find(text)
					if !ok {
						t.Fatal(text)
					}
					line := n.ShapeRichTextWith([]n.Span{{Text: text}}, n.Font{Family: family, Size: size, Weight: 400}, n.TextShape{LineHeight: 20}).Lines()[0]
					return r.Y + (r.H-20)/2 + line.Baseline
				}
				left, right := baseline("授权方式", 13), baseline(caption, 12)
				if math.Abs(float64(left-right)) > .01 {
					t.Fatalf("context=%s dark=%v pending=%v: label baseline=%v caption baseline=%v", context, dark, pending, left, right)
				}
			}
		}
	}
}

func TestTailnetAddressAndInboundRowsShareRhythm(t *testing.T) {
	for _, state := range []string{"Running", "Stopped"} {
		m := deviceUIModel()
		m.Tailnet = tailnet.Snapshot{State: state, InboundEnabled: true, Network: "example"}
		v := New(m, app.NewController(""), "")
		v.devices = true
		v.deviceSelected = "tailnet"
		ui := n.NewTester(v.View, 1280, 800)
		ui.Advance(time.Second)
		network, ok := ui.Find("Tailnet 地址")
		if !ok {
			t.Fatal("network row missing")
		}
		inbound, ok := ui.Find("入站转发")
		if !ok {
			t.Fatal("inbound row missing")
		}
		if math.Abs(float64(inbound.Y-network.Y-40)) > 1 {
			t.Fatal("network settings separated instead of adjacent rows", network, inbound)
		}
	}

}
