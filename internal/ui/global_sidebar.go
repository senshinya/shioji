package ui

import (
	"fmt"
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/tailnet"
)

// The three dock symbols share 2…18 ink extents in a 20-unit viewBox,
// the common 1.5 stroke, and a fixed 16 DIP slot inside a 28 DIP target.
var quickDeviceIcon = svg("M2 2h16v12H2zM6 18h8M10 14v4")
var quickNetworkIcon = svg("M7 2h6v6H7zM2 14h4v4H2zM14 14h4v4h-4zM10 8v3M4 14v-3h12v3")
var quickForwardIcon = svg("M2 6h16m-4-4 4 4-4 4M18 14H2m4-4-4 4 4 4")
var quickExternalIcon = svg("M3 17 17 3M3 3h14v14")

const quickFooterHeight float32 = 34

type quickSidebar struct {
	selected, last, focus string
	reset                 bool
	devices, peers, ports n.ListState
}

func (v *View) quickUI() *quickSidebar {
	if v.globalSidebar == nil {
		v.globalSidebar = &quickSidebar{last: "devices"}
	}
	return v.globalSidebar
}
func (v *View) selectQuick(kind string) {
	s := v.quickUI()
	if s.selected == kind {
		s.selected = ""
		return
	}
	s.reset = s.selected == ""
	s.selected, s.last = kind, kind
}
func quickIndex(kind string) int {
	switch kind {
	case "tailnet":
		return 1
	case "ports":
		return 2
	}
	return 0
}
func (v *View) quickFooter(c *n.Context, t *n.Theme) {
	s := v.quickUI()
	const targetSize = 28
	const leadingInset = sidebarLeadingInset - (targetSize-sidebarIconColumnWidth)/2
	n.Row(c.Key("global-footer")).Height(quickFooterHeight).Shrink(0).FillWidth().Padding(2, sidebarTrailingInset, 2, leadingInset).Gap(4).AlignItems(n.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Label("全局快捷栏").Children(func() {
		for _, item := range []struct {
			kind, label string
			icon        *n.SVG
		}{{"devices", "设备", quickDeviceIcon}, {"tailnet", "Tailnet", quickNetworkIcon}, {"ports", "端口转发", quickForwardIcon}} {
			button := control(c.Key(item.kind)).Size(targetSize, targetSize).Label(item.label).Selected(s.selected == item.kind).TextColor(t.TextMuted)
			if s.selected == item.kind {
				button.Background(t.SurfacePressed).TextColor(t.Text)
			}
			button.Children(func() { n.Icon(c, item.icon).Size(16, 16).Label(item.label + "图标").Role(n.RoleNone) })
			if s.focus == item.kind {
				button.Focus()
				s.focus = ""
			}
			if button.Clicked() {
				v.selectQuick(item.kind)
			}
			target := float32(0)
			if s.selected != item.kind && (button.Hovered() || button.FocusVisible()) {
				target = 1
			}
			shown := button.AnimateTo("hint", target, motionAnimation(c, 180*time.Millisecond))
			if s.selected == item.kind {
				shown = button.Jump("hint", 0)
			}
			if shown > 0 {
				n.Overlay(c.Key("hint:"+item.kind), func() {
					n.Column(c).Absolute().AttachTo(button, n.AnchorTopStart, n.AnchorBottomStart).Margin(0, 0, 8, 0).Height(28 * shown).Clip().PassThrough().Role(n.RoleTooltip).Children(func() {
						n.Text(c, item.label).FontSize(12).FixedLineHeight(18).Padding(4, 8).Radius(5).Background(raised(t)).Border(1, t.Border).Transform(n.Translation(0, 4*(1-shown)))
					})
				})
			}
		}
		n.Spacer(c).Grow(1)
		if iconButton(c, settingsIcon, "设置").Clicked() && v.OpenSettings != nil {
			v.OpenSettings()
		}
	})
}
func (v *View) quickPanelHeight(kind string) float32 {
	switch kind {
	case "devices":
		return 49 + 48*float32(1+len(v.Model.Connections))
	case "tailnet":
		if v.Model.Tailnet.State != "Running" {
			return 97
		}
		return 121 + max(32, 48*float32(len(v.Model.Tailnet.Peers)))
	case "ports":
		return 49 + max(32, 44*float32(len(v.persistenceModel().PortForwards)))
	}
	return 0
}
func (v *View) quickPanel(c *n.Context, t *n.Theme, color n.Color, available float32) {
	s := v.quickUI()
	host := n.Column(c.Key("global-quick-panel")).Absolute().Left(0).Right(0).Bottom(quickFooterHeight).Clip().Label("全局快捷面板")
	animation := motionAnimation(c, 260*time.Millisecond)
	target := min(v.quickPanelHeight(s.last), min(360, max(0, available-quickFooterHeight-40)))
	height := host.AnimateTo("height", target, animation)
	index := host.AnimateTo("index", float32(quickIndex(s.last)), animation)
	if s.reset {
		height = host.Jump("height", target)
		index = host.Jump("index", float32(quickIndex(s.last)))
		s.reset = false
	}
	open := float32(0)
	if s.selected != "" {
		open = 1
	}
	reveal := host.AnimateTo("reveal", open, animation)
	host.Height(height)
	n.Inert(host, s.selected == "")
	if reveal <= 0 {
		return
	}
	host.Children(func() {
		n.Column(c).Fill().Background(color).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Transform(n.Translation(0, height*(1-reveal))).Children(func() {
			width := host.LayoutBounds().W
			if width == 0 {
				width = host.Bounds().W
			}
			for i, kind := range []string{"devices", "tailnet", "ports"} {
				pane := n.Column(c.Key(kind)).Absolute().Left((float32(i) - index) * width).Top(0).Bottom(0).Width(width).MinHeight(0).Clip()
				n.Inert(pane, s.selected != kind)
				pane.Children(func() {
					v.quickHeading(c, t, kind)
					switch kind {
					case "devices":
						v.quickDevices(c, t)
					case "tailnet":
						v.quickTailnet(c, t)
					case "ports":
						v.quickPorts(c, t)
					}
				})
			}
		})
	})
}
func (v *View) quickHeading(c *n.Context, t *n.Theme, kind string) {
	title := "设备"
	if kind == "tailnet" {
		title = "Tailnet"
	}
	if kind == "ports" {
		title = "端口转发"
	}
	n.Row(c).Height(40).Shrink(0).FillWidth().Padding(6, 10, 6, 12).Gap(2).AlignItems(n.Baseline).Children(func() {
		font := n.Font{Family: t.Font, Size: 13, Weight: 500}
		n.Text(c, title).FontSize(13).FontWeight(500).FixedLineHeight(28).Label(title + " 概览标题")
		if kind != "ports" {
			label := "打开设备页"
			if kind == "tailnet" {
				label = "打开 Tailnet 配置"
			}
			action := quickHeadingAction(c.Key("manage"), t, quickExternalIcon, label, title, font)
			if action.Clicked() {
				v.closeSchedulePage()
				v.quickUI().selected = ""
				v.skillReturn = nil
				v.deviceTab = "settings"
				v.deviceList = true
				v.devices = true
				if kind == "tailnet" {
					v.deviceSelected = "tailnet"
					v.deviceList = false
				}
			}
		}
		n.Spacer(c).Grow(1)
		if kind == "ports" && quickHeadingAction(c.Key("add"), t, plus, "创建端口转发", title, font).Clicked() {
			v.beginPortForward()
		}
		if quickHeadingAction(c.Key("collapse"), t, disclosureDown, "收起"+title+"面板", title, font).Clicked() {
			v.quickUI().selected = ""
			v.quickUI().focus = kind
		}
	})
}

func quickHeadingAction(c *n.Context, t *n.Theme, icon *n.SVG, label, title string, font n.Font) n.Element {
	// A quiet 10 DIP glyph sits inside the standard 16 DIP slot and 28 DIP
	// hit target. Every action uses the title's actual text baseline.
	return control(c).Size(28, 28).Label(label).Tooltip(label).Children(func() {
		inlineSymbol(c, icon, title, font, 10, t.TextMuted).Padding(0, 3).FixedLineHeight(28)
	})
}
func quickRow(c *n.Context, t *n.Theme, label, name, secondary, value string, online bool) {
	font := n.Font{Family: t.Font, Size: 12, Weight: 400}
	color := t.TextMuted
	state := "离线"
	if online {
		color = t.Success
		state = "在线"
	}
	n.Column(c).Height(48).FillWidth().Padding(6, 12).Label(label + "，" + state).Tooltip(state).Children(func() {
		n.Row(c).Height(18).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
			statusDotSize(c, name, font, color, 4, false).FixedLineHeight(18).Label("连接状态点").Role(n.RoleNone)
			n.Text(c, name).FontSize(12).FixedLineHeight(18).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).TooltipWhenTruncated(name)
			n.Text(c, value).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Shrink(0)
		})
		n.Text(c, secondary).FontSize(11).FixedLineHeight(16).PaddingStart(12).FillWidth().SingleLine().Ellipsis("…").TextColor(t.TextMuted).TooltipWhenTruncated(secondary)
	})
}
func (v *View) quickDevices(c *n.Context, t *n.Theme) {
	s := v.quickUI()
	m := v.Model
	s.devices.Key = func(i int) any {
		if i == 0 {
			return "local"
		}
		return m.Connections[i-1].ID
	}
	n.List(c.Key("device-list"), &s.devices, 1+len(m.Connections), func(i int) {
		key, name, method := "local", m.LocalName(), "本机"
		if i > 0 {
			conn := m.Connections[i-1]
			key, name = conn.ID, conn.Name
			route := conn.ActiveSSH()
			if state := m.DeviceStates[key]; state != nil {
				for _, p := range state.Paths {
					if p.Phase == app.PathActive {
						for _, r := range conn.Normalized().Routes {
							if r.ID == p.ID {
								route = r.SSH
							}
						}
					}
				}
			}
			method = "SSH"
			if route.TailnetPeer != "" {
				method = "Tailnet"
			}
		}
		state := m.DeviceStates[key]
		online := state != nil && state.Connected
		latency := "—"
		if key != "local" && online {
			latency = quickDeviceLatency(state, c.Now())
		}
		quickRow(c, t, "设备概览："+name, name, method, latency, online)
	}).FillWidth().Grow(1).MinHeight(0).Label("设备概览列表")
}
func quickDeviceLatency(s *app.DeviceState, now time.Time) string {
	if s == nil || !s.Connected {
		return "—"
	}
	sample := s.Timing
	for _, p := range s.Paths {
		if p.Phase == app.PathActive {
			sample = p.Timing
			break
		}
	}
	if sample == nil || sample.Milliseconds <= 0 || sample.ObservedAt.IsZero() || sample.ObservedAt.After(now) || now.Sub(sample.ObservedAt) > 30*time.Second {
		return "—"
	}
	return fmt.Sprintf("%g ms", math.Round(sample.Milliseconds*10)/10)
}
func quickPeerLatency(p tailnet.Peer, now time.Time) string {
	m := p.Measurement
	if !p.Online || m == nil || m.Milliseconds <= 0 || m.ObservedAt.IsZero() || m.ObservedAt.After(now) || now.Sub(m.ObservedAt) > 30*time.Second {
		return "—"
	}
	return fmt.Sprintf("%g ms", math.Round(m.Milliseconds*10)/10)
}
func (v *View) quickTailnet(c *n.Context, t *n.Theme) {
	s := v.Model.Tailnet
	name, detail := s.Hostname, s.Address
	online := s.State == "Running"
	if name == "" {
		name = "尚无节点"
	}
	if !online {
		switch {
		case s.Error != "":
			detail = "Tailnet 连接失败"
		case s.Authorizing || s.State == "NeedsLogin":
			detail = "等待浏览器授权"
		case s.State == "NeedsMachineAuth":
			detail = "等待网络管理员批准"
		case s.State == "Stopped":
			detail = "Tailnet 已暂停"
		case s.Hostname == "":
			detail = "未加入 Tailnet"
		default:
			detail = "未连接 Tailnet"
		}
	}
	quickRow(c, t, "Tailnet 本机节点", name, detail, "本机", online)
	if !online {
		return
	}
	n.Text(c, "对端").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).Padding(4, 12).Height(24).Shrink(0)
	if len(s.Peers) == 0 {
		n.Text(c, "没有可见对端").FontSize(11).TextColor(t.TextMuted).Padding(6, 12).Height(32)
		return
	}
	v.quickUI().peers.Key = func(i int) any { return s.Peers[i].ID }
	n.List(c.Key("peer-list"), &v.quickUI().peers, len(s.Peers), func(i int) {
		p := s.Peers[i]
		name := p.Name
		if name == "" {
			name = p.Address
		}
		quickRow(c, t, "Tailnet 对端："+name, name, p.Address, quickPeerLatency(p, c.Now()), p.Online)
	}).Grow(1).FillWidth().MinHeight(0).Label("Tailnet 对端列表")
}
