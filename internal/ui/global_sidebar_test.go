package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
)

func TestGlobalSidebarPanelsFitContentCapHeightAndPreserveFooter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			m := deviceUIModel()
			m.Tailnet = tailnet.Snapshot{State: "Running", Hostname: "shioji-mac", Address: "100.80.0.2", Peers: []tailnet.Peer{{ID: "one", Name: "Mac mini", Address: "100.80.1.3", Online: true}, {ID: "two", Name: "离线构建机", Address: "100.80.1.4"}}}
			for i := range 24 {
				key := fmt.Sprint("extra-", i)
				m.Connections = append(m.Connections, app.DeviceConnection{ID: key, DeviceID: key, Name: "开发 Mac " + fmt.Sprint(i), SSH: transport.SSH{Target: "dev@mac", Worker: "/worker"}})
			}
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			footer, ok := ui.Find("全局快捷栏")
			if !ok {
				t.Fatal("global footer missing")
			}
			devices, _ := ui.Find("设备")
			ports, _ := ui.Find("端口转发")
			if err := ui.Click("设备"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			panel, ok := ui.Find("全局快捷面板")
			if !ok || panel.H > 360 || panel.Y+panel.H != footer.Y || v.devices {
				t.Fatal("panel height/position/navigation", panel, footer, v.devices)
			}
			if r, _ := ui.Find("设备"); r != devices {
				t.Fatal("launcher moved", r, devices)
			}
			if err := ui.Click("Tailnet"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			small, _ := ui.Find("全局快捷面板")
			if small.H >= panel.H || !ui.HasText("离线构建机") || !ui.HasText("100.80.0.2") {
				t.Fatal("tailnet did not fit content/all peers", small, ui.Texts())
			}
			if err := ui.Click("端口转发"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			empty, _ := ui.Find("全局快捷面板")
			if empty.H >= small.H || !ui.HasText("暂无端口转发") {
				t.Fatal("empty panel retained tall height", empty, small)
			}
			if r, _ := ui.Find("端口转发"); r != ports {
				t.Fatal("port launcher moved", r, ports)
			}
			ui.Key(0, n.KeyEscape)
			ui.Advance(time.Second)
			if r, _ := ui.Find("全局快捷栏"); r != footer {
				t.Fatal("footer moved on close", r, footer)
			}
		})
	}
}

func TestGlobalFooterKeepsOnlyThreeDIPAroundUnchangedTargets(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := New(deviceUIModel(), nil, "")
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		footer, _ := ui.Find("全局快捷栏")
		if footer.H != 34 {
			t.Error("footer retains excessive vertical space", footer)
		}
		for _, label := range []string{"设备", "Tailnet", "端口转发", "额度", "设置"} {
			button, ok := ui.Find(label)
			if !ok || math.Abs(float64(button.W-28)) > .01 || button.H != 28 {
				t.Fatal("click target changed", label, button)
			}
			top, bottom := button.Y-footer.Y, footer.Y+footer.H-button.Y-button.H
			if top > 3.5 || bottom > 3.5 || top < 2.5 || bottom < 2.5 {
				t.Error("unequal or excessive vertical inset", label, top, bottom)
			}
		}
		ui.Click("Tailnet")
		ui.Advance(time.Second)
		panel, _ := ui.Find("全局快捷面板")
		if panel.Y+panel.H != footer.Y {
			t.Fatal("compact footer detached the panel", panel, footer)
		}
	}
}

func TestQuickRowsExposeConnectionStateWithoutVisibleStatusLabels(t *testing.T) {
	ui := n.NewTester(func(c *n.Context) {
		quickRow(c, c.Theme(), "设备概览：开发机", "开发机", "SSH", "12 ms", true)
		quickRow(c, c.Theme(), "Tailnet 对端：构建机", "构建机", "100.80.0.2", "—", false)
	}, 300, 200)
	for _, label := range []string{"设备概览：开发机，在线", "Tailnet 对端：构建机，离线"} {
		if _, ok := ui.Find(label); !ok {
			t.Error("connection state missing from accessible row", label)
		}
	}
	for _, state := range []string{"在线", "离线"} {
		if _, ok := ui.Find(state); ok {
			t.Fatal("status text must only appear on hover", state)
		}
	}
}
func TestGlobalTailnetScrollKeepsNodeAndHeadingFixed(t *testing.T) {
	m := deviceUIModel()
	m.Tailnet = tailnet.Snapshot{State: "Running", Hostname: "shioji-mac", Address: "100.80.0.2"}
	for i := range 30 {
		m.Tailnet.Peers = append(m.Tailnet.Peers, tailnet.Peer{ID: fmt.Sprint(i), Name: fmt.Sprint("peer-", i), Address: fmt.Sprint("100.80.1.", i+1), Online: i%2 == 0})
	}
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 920, 640)
	ui.Advance(time.Second)
	ui.Click("Tailnet")
	ui.Advance(time.Second)
	node, _ := ui.Find("shioji-mac")
	heading, _ := ui.Find("对端")
	list, ok := ui.Find("Tailnet 对端列表")
	if !ok {
		t.Fatal("peer scrollport missing")
	}
	ui.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 400)
	ui.Advance(time.Second)
	if after, _ := ui.Find("shioji-mac"); after != node {
		t.Fatal("node scrolled", node, after)
	}
	if after, _ := ui.Find("对端"); after != heading {
		t.Fatal("heading scrolled", heading, after)
	}
	panel, _ := ui.Find("全局快捷面板")
	if panel.H > 360 || panel.Y < 40 {
		t.Fatal("height cap", panel)
	}
}
func TestQuickSidebarLatencyNeverUsesStaleOrUnknownAsCurrent(t *testing.T) {
	now := time.Now()
	p := tailnet.Peer{Online: true, Measurement: &tailnet.Measurement{Milliseconds: 12, ObservedAt: now}}
	if got := quickPeerLatency(p, now); got != "12 ms" {
		t.Fatal(got)
	}
	p.Measurement.ObservedAt = now.Add(-time.Minute)
	if got := quickPeerLatency(p, now); got != "—" {
		t.Fatal("stale RTT", got)
	}
	p.Measurement.ObservedAt = now
	p.Online = false
	if got := quickPeerLatency(p, now); got != "—" {
		t.Fatal("offline RTT", got)
	}
}
func TestQuickPanelClosedReopenHasNoResidualHorizontalMotion(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("设备")
	ui.Advance(time.Second)
	ui.Click("设备")
	ui.Advance(time.Second)
	ui.Click("Tailnet")
	ui.Advance(80 * time.Millisecond)
	panel, ok := ui.Find("全局快捷面板")
	title, found := ui.Find("Tailnet 概览标题")
	if !ok || !found || math.Abs(float64(title.X-panel.X-12)) > 1 {
		t.Fatal("closed reopen slides horizontally", panel, title)
	}
}

func TestGlobalDockIconsHaveEqualVisibleExtentAndOpticalCenter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			icons := []*n.SVG{quickDeviceIcon, quickNetworkIcon, quickForwardIcon, quickQuotaIcon, settingsIcon}
			ui := n.NewTester(func(c *n.Context) {
				n.Row(c).Fill().Padding(12).Gap(8).Background(c.Theme().Surface).AlignItems(n.Start).Children(func() {
					for i, icon := range icons {
						n.Column(c).Size(28, 28).AlignItems(n.Center).Justify(n.Center).Children(func() { n.Icon(c, icon).Size(16, 16).TextColor(c.Theme().TextMuted).Label(fmt.Sprint("glyph", i)) })
					}
				})
			}, 220, 60)
			ui.SetDark(dark)
			ui.SetScale(scale)
			var firstCenter, firstHeight float64
			for i := range len(icons) {
				box, _ := ui.Find(fmt.Sprint("glyph", i))
				lo, hi := nativeQuickInkY(t, ui, box, scale)
				center := float64(lo+hi) / (2 * float64(scale))
				height := float64(hi-lo+1) / float64(scale)
				if i == 0 {
					firstCenter, firstHeight = center, height
				} else if math.Abs(center-firstCenter) > 1 || math.Abs(height-firstHeight) > 1 {
					t.Fatal("dock icon ink mismatch", dark, scale, i, center, height, firstCenter, firstHeight)
				}
			}
		}
	}
}

func TestQuickHeadingActionsStayCompactAndAlignToTitleInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, kind := range []string{"devices", "tailnet", "ports", "quota"} {
				t.Run(fmt.Sprintf("%v/%g/%s", dark, scale, kind), func(t *testing.T) {
					v := New(deviceUIModel(), nil, "")
					ui := n.NewTester(func(c *n.Context) {
						n.Column(c).Fill().Background(c.Theme().Surface).Children(func() { v.quickHeading(c, c.Theme(), kind) })
					}, 260, 50)
					ui.SetDark(dark)
					ui.SetScale(scale)
					title, actions := "设备", []string{"打开设备页", "收起设备面板"}
					if kind == "tailnet" {
						title, actions = "Tailnet", []string{"打开 Tailnet 配置", "收起Tailnet面板"}
					}
					if kind == "ports" {
						title, actions = "端口转发", []string{"创建端口转发", "收起端口转发面板"}
					}
					if kind == "quota" {
						title, actions = "额度", []string{"管理展示的 Profile", "刷新额度", "收起额度面板"}
					}
					caption, _ := ui.Find(title + " 概览标题")
					a, b := nativeQuickInkY(t, ui, caption, scale)
					for _, label := range actions {
						box, ok := ui.Find(label)
						if !ok {
							t.Fatal("missing heading action", label)
						}
						if math.Abs(float64(box.W-28)) > .01 || math.Abs(float64(box.H-28)) > .01 {
							t.Error("compact glyph lost its click target", label, box)
						}
						lo, hi := nativeQuickInkY(t, ui, box, scale)
						if float32(hi-lo+1)/scale > 9 {
							t.Error("heading glyph is oversized", label, float32(hi-lo+1)/scale)
						}
						if math.Abs(float64(lo+hi-a-b)/(2*float64(scale))) > 1 {
							t.Error("heading action is off the title ink center", label, lo, hi, a, b)
						}
					}
				})
			}
		}
	}
}
func TestQuickStatusDotsAlignToFirstLineChineseAndLatinInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, name := range []string{"HHH", "国国国", "国 H 国"} {
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Background(c.Theme().Surface).Children(func() { quickRow(c, c.Theme(), "row", name, "100.80.0.2", "12 ms", true) })
			}, 240, 60)
			ui.SetDark(dark)
			dot, _ := ui.Find("连接状态点")
			text, _ := ui.Find(name)
			lo, hi := nativeQuickInkY(t, ui, dot, 1)
			a, b := nativeQuickInkY(t, ui, text, 1)
			if math.Abs(float64(lo+hi-a-b)/2) > 1 {
				t.Fatal("status dot misses first-line ink", dark, name, dot, text, lo, hi, a, b)
			}
		}
	}
}
func nativeQuickInkY(t *testing.T, ui *n.Tester, box n.Rect, scale float32) (int, int) {
	t.Helper()
	im := ui.Image()
	bg := im.RGBAAt(0, 0)
	lo, hi := im.Bounds().Max.Y, -1
	for y := int(math.Floor(float64(box.Y * scale))); y < int(math.Ceil(float64((box.Y+box.H)*scale))); y++ {
		for x := int(math.Floor(float64(box.X * scale))); x < int(math.Ceil(float64((box.X+box.W)*scale))); x++ {
			p := im.RGBAAt(x, y)
			if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
				lo = min(lo, y)
				hi = max(hi, y)
			}
		}
	}
	if hi < 0 {
		t.Fatal("native ink missing", box)
	}
	return lo, hi
}
