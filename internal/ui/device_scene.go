package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

type deviceSceneRoute struct {
	Route                               app.DeviceRoute
	Status                              app.DevicePathStatus
	Name, Timing, Phase, Network, Relay string
	Active, Busy                        bool
}
type deviceSceneProjection struct {
	Connection                               app.DeviceConnection
	Routes                                   []deviceSceneRoute
	Title, WorkerStatus, AgentStatus         string
	WorkerReady, AgentReady, AgentBusy, Busy bool
}
type deviceSceneUI struct {
	open                    map[string]bool
	identity                bool
	pending                 bool
	epoch, error, signature string
	request                 uint64
	changed                 time.Time
}

func deviceSceneTiming(s app.DevicePathStatus, connected bool, now time.Time) string {
	t := s.Timing
	if t == nil || t.ObservedAt.IsZero() || t.ObservedAt.After(now) || math.IsNaN(t.Milliseconds) || math.IsInf(t.Milliseconds, 0) || t.Milliseconds < 0 {
		return "—"
	}
	value := fmt.Sprintf("%.0f ms", t.Milliseconds)
	if !connected || now.Sub(t.ObservedAt) > 15*time.Second || s.Phase == app.PathUnavailable {
		return "上次 " + value
	}
	return value
}
func (v *View) deviceSceneProjection(key string, state *app.DeviceState, now time.Time) deviceSceneProjection {
	p := deviceSceneProjection{Title: "配置 Agent", WorkerStatus: "可用", AgentStatus: "待配置", WorkerReady: state.Connected}
	for _, h := range state.Readiness.Harnesses {
		if !h.Configured {
			continue
		}
		if h.State == domain.RuntimeReady {
			p.AgentReady = true
		}
		if h.State == domain.RuntimeRefreshing {
			p.AgentBusy = true
		}
		if p.AgentStatus == "待配置" {
			p.AgentStatus = "不可用"
		}
	}
	if len(state.Readiness.Harnesses) == 0 && state.Readiness.HarnessID != "" {
		p.AgentReady = state.Readiness.State == domain.RuntimeReady
		p.AgentBusy = state.Readiness.State == domain.RuntimeRefreshing
		p.AgentStatus = "不可用"
	}
	if p.AgentReady {
		p.Title, p.AgentStatus = "设备已就绪", "已配置"
	} else if p.AgentBusy {
		p.Title, p.AgentStatus = "正在确认 Agent", "检测中"
		p.Busy = true
	} else if p.AgentStatus == "不可用" {
		p.Title = "Agent 不可用"
	}
	if service := state.WorkerService; service != nil && service.Phase == app.WorkerUpdating {
		p.WorkerReady = false
		p.WorkerStatus = "更新中"
		p.Title = "执行服务正在更新"
		p.Busy = true
	}
	if !state.Connected {
		p.Title = "连接已断开"
		p.WorkerReady, p.AgentReady, p.AgentBusy, p.Busy = false, false, false, false
		p.WorkerStatus, p.AgentStatus = "状态未知", "状态未知"
	}
	for _, connection := range v.Model.Connections {
		if connection.ID == key {
			p.Connection = connection.Normalized()
			break
		}
	}
	for _, route := range p.Connection.Routes {
		status := app.DevicePathStatus{ID: route.ID, Phase: app.PathConnecting}
		for _, known := range state.Paths {
			if known.ID == route.ID {
				status = known
				break
			}
		}
		item := deviceSceneRoute{Route: route, Status: status, Name: "直连 SSH", Timing: deviceSceneTiming(status, state.Connected, now), Phase: pathPhaseLabel(status.Phase)}
		item.Active = state.Connected && status.Phase == app.PathActive
		item.Busy = status.Phase == app.PathConnecting || status.Phase == app.PathSwitching
		if !state.Connected && (status.Phase == app.PathActive || status.Phase == app.PathReady) {
			item.Phase = "未连接"
			item.Busy = false
		}
		if route.SSH.TailnetPeer != "" {
			item.Name = "Tailnet SSH"
			peer := v.tailnetPathPeer(route.SSH.TailnetPeer)
			item.Network = "路径未知"
			if m := peer.Measurement; m != nil && !m.ObservedAt.IsZero() && !m.ObservedAt.After(now) {
				switch m.Path {
				case "direct":
					item.Network = "直连"
				case "derp":
					item.Network = "DERP"
					item.Relay = "DERP"
					if m.RegionName != "" {
						item.Network += " · " + m.RegionName
					}
					if m.Region != "" {
						item.Network += " (" + m.Region + ")"
					}
				case "peer-relay":
					item.Network = "Peer Relay"
					item.Relay = "Relay"
				}
				if !state.Connected || !peer.Online || now.Sub(m.ObservedAt) > 30*time.Second {
					item.Network = "上次 · " + item.Network
				}
			}
		}
		p.Routes = append(p.Routes, item)
	}
	// Several routes of one kind need their actual endpoint to remain distinct.
	for i := range p.Routes {
		count := 0
		for _, other := range p.Routes {
			if other.Name == p.Routes[i].Name {
				count++
			}
		}
		if count > 1 {
			address := devicePathAddress(p.Routes[i].Route.SSH, p.Routes[i].Status.Endpoint)
			if p.Routes[i].Network != "" {
				address += " · " + p.Routes[i].Network
			}
			p.Routes[i].Network = address
		}
	}
	return p
}

// Every position is in the same 600-DIP-wide design coordinate system. Both
// drawing and native hit/text elements consume these coordinates.
type deviceSceneRouteGeometry struct{ Lane, Caption, Network, HitTop, HitHeight float32 }

func deviceSceneGeometry(count int) (float32, []deviceSceneRouteGeometry) {
	height := float32(210 + max(0, count-2)*64)
	if count == 0 {
		return height, nil
	}
	if count == 1 {
		return height, []deviceSceneRouteGeometry{{105, 86, 124, 74, 64}}
	}
	if count == 2 {
		return height, []deviceSceneRouteGeometry{{66, 48, 85, 34, 64}, {144, 166, 185, 132, 68}}
	}
	out := make([]deviceSceneRouteGeometry, count)
	for i := range out {
		lane := height/2 + (float32(i)-float32(count-1)/2)*64
		out[i] = deviceSceneRouteGeometry{lane, lane - 18, lane + 18, lane - 31, 62}
	}
	return height, out
}

func (v *View) deviceStatusScene(c *n.Context, t *n.Theme, key string, state *app.DeviceState) {
	projection := v.deviceSceneProjection(key, state, c.Now())
	root := n.Column(c).FillWidth().Label("设备状态区域")
	ui := n.Local(root, "state", func() deviceSceneUI { return deviceSceneUI{open: map[string]bool{}, epoch: state.ConnectionEpoch} })
	if ui.epoch != state.ConnectionEpoch {
		ui.epoch = state.ConnectionEpoch
		ui.pending = false
		ui.request++
		ui.error = ""
	}
	signature := projection.Title
	for _, r := range projection.Routes {
		signature += "|" + r.Route.ID + ":" + string(r.Status.Phase)
	}
	if ui.signature != "" && ui.signature != signature {
		ui.changed = c.Now()
	}
	ui.signature = signature
	if ui.pending {
		projection.Title = "正在检测设备"
		projection.Busy = true
	}
	icon := v.Model.DeviceIconChoice(key)
	if icon == app.DeviceIconAuto {
		icon = v.deviceDefaultIcon(key)
	}
	root.Children(func() {
		n.Row(c).FillWidth().Height(configurationControlHeight).Gap(8).AlignItems(n.Center).Children(func() {
			font := n.Font{Family: t.Font, Size: 14, Weight: 500}
			statusDot(c, projection.Title, font, t.TextMuted, projection.Busy)
			n.Text(c, projection.Title).FontSize(14).FontWeight(500).FixedLineHeight(configurationTextLine).Grow(1).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			action := "重新检测"
			if !state.Connected {
				action = "重新连接"
			}
			if ui.pending {
				action = "正在检测…"
			}
			if configurationAction(c, action).Label("重新检测设备").Disabled(v.Controller == nil || ui.pending).Clicked() {
				v.refreshDeviceScene(key, state, ui)
			}
			if key != "local" {
				v.deviceAddPath(c, t, projection.Connection)
			}
		})
		height, lanes := deviceSceneGeometry(len(projection.Routes))
		frame := n.Column(c.Key("diagram")).FillWidth().MaxWidth(deviceSceneMaxWidth).AlignSelf(n.Center).AspectRatio(600/height).Margin(8, 0, 0).Label("设备连接图")
		local := key == "local"
		reduced := c.Preferences().ReduceMotion
		frame.Draw(func(p *n.Painter, box n.Rect) {
			v.drawDeviceScene(p, box, t, icon, projection, height, lanes, local, ui.changed, reduced)
		})
		frame.Children(func() {
			target := n.Column(c).Absolute().LeftPercent(56).TopPercent(0).WidthPercent(44).HeightPercent(100).IgnoreInput(true).Role(n.RoleImage).Label("目标设备:" + string(icon)).Description("执行服务：" + projection.WorkerStatus + "；Agent：" + projection.AgentStatus)
			if local {
				target.LeftPercent(27)
			}
			extra := (height - 210) / 2
			if !local {
				deviceSceneText(c, "当前 Mac", 0, 154+extra, 100, height, 11, t.TextMuted, "", n.Center)
				for index, item := range projection.Routes {
					r, g := item, lanes[index]
					row := n.Column(c.Key("route:" + r.Route.ID)).Absolute().LeftPercent(18).TopPercent(g.HitTop / height * 100).WidthPercent(38).HeightPercent(g.HitHeight / height * 100).Label("连接路径:" + r.Route.ID)
					row.Children(func() {
						button := control(c.Key("path-hit"), 6).Fill().Label("展开连接路径:" + r.Route.ID).Description(r.Name + " · " + r.Timing + " · " + r.Phase).Tooltip(devicePathAddress(r.Route.SSH, r.Status.Endpoint) + "\n" + r.Network + "\n" + r.Status.Error)
						button.ContextMenu(func(menu *n.Menu) { v.devicePathMenu(menu, projection.Connection, r.Route, state) })
						if button.Clicked() {
							ui.open[r.Route.ID] = !ui.open[r.Route.ID]
						}
					})
					deviceSceneRouteCaption(c, r, g, height)
					if r.Relay != "" {
						deviceSceneText(c, r.Relay, 201, g.Lane, 42, height, 10, t.TextMuted, "中继节点:"+r.Route.ID, n.Center)
					}
					if r.Network != "" {
						deviceSceneText(c, r.Network, 110, g.Network, 222, height, 11, t.TextMuted, "网络路径:"+r.Route.ID, n.Center)
					}
					if timing := r.Status.Timing; timing != nil {
						until := timing.ObservedAt.Add(15 * time.Second).Sub(c.Now())
						if until > 0 && until <= 15*time.Second {
							c.After(until + time.Millisecond)
						}
					}
					if r.Route.SSH.TailnetPeer != "" {
						peer := v.tailnetPathPeer(r.Route.SSH.TailnetPeer)
						if m := peer.Measurement; m != nil {
							until := m.ObservedAt.Add(30 * time.Second).Sub(c.Now())
							if until > 0 && until <= 30*time.Second {
								c.After(until + time.Millisecond)
							}
						}
					}
				}
			}
			v.deviceSceneScreen(c, t, icon, projection, height, extra, local)
		})
		anyOpen := false
		for _, r := range projection.Routes {
			opened := ui.open[r.Route.ID]
			anyOpen = anyOpen || opened
			details := n.Column(c.Key("details:" + r.Route.ID)).FillWidth().MaxWidth(deviceSceneDetailsWidth).AlignSelf(n.Center).Clip()
			animatedContentHeight(c, details, opened, 200*time.Millisecond, func() float32 {
				n.Column(c).FillWidth().Padding(12, 0, 0).Children(func() {
					v.deviceScenePathDetails(c, t, projection.Connection, r.Route, r.Status, state, func() { ui.open[r.Route.ID] = false })
				})
				return 0
			})
		}
		if anyOpen {
			deviceDisclosure(c.Key("identity"), "设备身份", &ui.identity, func() { deviceMeta(c, "设备 ID", projection.Connection.DeviceID) })
		}
		if ui.error != "" {
			n.Text(c, ui.error).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		}
		deviceDivider(c)
	})
}

func (v *View) refreshDeviceScene(key string, state *app.DeviceState, ui *deviceSceneUI) {
	if !state.Connected {
		v.do(app.Job{ConnectionID: key, Method: "$reconnect"})
		return
	}
	ui.request++
	request, epoch := ui.request, state.ConnectionEpoch
	ui.pending = true
	ui.error = ""
	if !v.do(app.Job{ConnectionID: key, Method: "readiness", Apply: func(m *app.Model, body json.RawMessage, err error) {
		current := m.DeviceStates[key]
		if current == nil || current.ConnectionEpoch != epoch || ui.request != request {
			return
		}
		ui.pending = false
		if err != nil {
			ui.error = "无法重新检测：" + err.Error()
			return
		}
		var ready domain.Readiness
		if json.Unmarshal(body, &ready) != nil {
			ui.error = "设备返回的运行状态无效"
			return
		}
		// Device-only readiness has no requested live session profiles. Keep the
		// latest owning-device profiles; the controller refreshes those separately.
		ready.Sessions = m.Readiness.Sessions
		m.Readiness = ready
	}}) {
		ui.pending = false
		ui.error = "设备操作繁忙，请重试"
	}
}

func deviceSceneText(c *n.Context, text string, x, y, width, height, size float32, color n.Color, label string, align n.Align) {
	font := n.Font{Family: c.Theme().Font, Size: size, Weight: 400}
	box := n.Column(c).Absolute().LeftPercent(x/600*100).TopPercent(y/height*100).WidthPercent(width/600*100).Height(20).Margin(-10, 0, 0).IgnoreInput(true).Role(n.RoleNone)
	if label != "" {
		box.Label(label)
	}
	box.Children(func() {
		n.Text(c, text).FillWidth().FontSize(size).FixedLineHeight(20).TextAlign(align).TextColor(color).SingleLine().Transform(n.Translation(0, inlineCenterOffset(text, font)))
	})
}

func deviceSceneRouteCaption(c *n.Context, r deviceSceneRoute, g deviceSceneRouteGeometry, height float32) {
	t := c.Theme()
	font := n.Font{Family: t.Font, Size: 12, Weight: 400}
	color := t.TextMuted
	if r.Active {
		color = t.Text
	}
	n.Row(c).Absolute().LeftPercent(18).TopPercent(g.Caption/height*100).WidthPercent(38).Height(20).Margin(-10, 0, 0).Gap(4).Justify(n.Center).AlignItems(n.Baseline).IgnoreInput(true).Transform(n.Translation(0, inlineCenterOffset(r.Name, font))).Children(func() {
		n.Text(c, r.Name).FontSize(12).FixedLineHeight(20).SingleLine().Shrink(1).MinWidth(0).TextColor(color).Label("路径名称:" + r.Route.ID)
		n.Text(c, r.Timing).FontSize(12).FixedLineHeight(20).FontFeatures("tnum").TextColor(color).Label("路径延迟:" + r.Route.ID).ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 160*time.Millisecond)})
		n.Text(c, r.Phase).FontSize(11).FixedLineHeight(20).TextColor(color).Label("路径状态:" + r.Route.ID)
	})
}

// The native screen content shares one font baseline with inlineSymbol. The
// artwork contains no text or check marks that could drift when it scales.
func (v *View) deviceSceneScreen(c *n.Context, t *n.Theme, icon app.DeviceIcon, p deviceSceneProjection, height, extra float32, local bool) {
	shift := float32(0)
	if local {
		shift = -166
	}
	type field struct{ x, y, w float32 }
	worker, agent := field{409, 97, 139}, field{409, 124, 139}
	workerName, agentName := "shioji-worker", "Agent"
	mobile := false
	switch icon {
	case app.DeviceIconMonitor:
		worker.y, agent.y = 84, 111
	case app.DeviceIconServer:
		worker, agent = field{424, 28, 88}, field{424, 51, 88}
		workerName = "Worker"
	case app.DeviceIconPhone:
		worker, agent = field{438, 80, 64}, field{438, 119, 64}
		workerName = "Worker"
		mobile = true
	case app.DeviceIconTablet:
		worker, agent = field{405, 80, 134}, field{405, 119, 134}
		workerName = "Worker"
		mobile = true
	}
	if icon == app.DeviceIconLaptop || icon == app.DeviceIconMonitor {
		deviceSceneText(c, "运行环境", 435+shift, iconHeaderY(icon)+extra, 110, height, 11, t.TextMuted, "", n.Start)
	}
	if mobile {
		label := "已连接"
		if !p.WorkerReady {
			label = "状态未知"
		}
		if p.Busy {
			label = "正在确认"
		}
		y := float32(44)
		if icon == app.DeviceIconTablet {
			y = 51
		}
		deviceSceneText(c, label, 427+shift, y+extra, 78, height, 11, t.TextMuted, "", n.Center)
	}
	build := func(key string, f field, name, state string, ready, busy bool) {
		deviceSceneScreenRow(c.Key(key), t, key, name, state, ready, busy).Absolute().LeftPercent((f.x+shift)/600*100).TopPercent((f.y+extra)/height*100).WidthPercent(f.w/600*100).Margin(-10, 0, 0)
		if mobile {
			deviceSceneText(c, state, f.x+shift, f.y+18+extra, f.w, height, 10, t.TextMuted, "屏幕"+key+"状态", n.Start)
		}
	}
	build("Worker", worker, workerName, p.WorkerStatus, p.WorkerReady, p.Busy && !p.WorkerReady)
	build("Agent", agent, agentName, p.AgentStatus, p.AgentReady, p.AgentBusy)
}
func deviceSceneScreenRow(c *n.Context, t *n.Theme, key, name, state string, ready, busy bool) n.Element {
	font := n.Font{Family: t.Font, Size: 12, Weight: 400}
	return n.Row(c).Height(20).Gap(4).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset(name, font))).Children(func() {
		n.Text(c, name).FontSize(12).FixedLineHeight(20).SingleLine().MinWidth(0).Grow(1).TooltipWhenTruncated(name).Label("屏幕" + key + "名称")
		if ready {
			inlineSymbol(c, check, name, font, 12, t.TextMuted).Label("屏幕" + key + "就绪")
		} else if busy {
			serviceSpinner(c.Key("spinner"), name, font, t.TextMuted)
		} else {
			n.Text(c, "—").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Tooltip(state)
		}
	})
}

func iconHeaderY(icon app.DeviceIcon) float32 {
	if icon == app.DeviceIconMonitor {
		return 50
	}
	return 63
}

func (v *View) drawDeviceScene(p *n.Painter, box n.Rect, t *n.Theme, icon app.DeviceIcon, state deviceSceneProjection, height float32, lanes []deviceSceneRouteGeometry, local bool, changed time.Time, reduced bool) {
	scale := box.W / 600
	extra := (height - 210) / 2
	art := n.Rect{X: box.X, Y: box.Y + extra*scale, W: box.W, H: 210 * scale}
	theme := 0
	if t.Dark {
		theme = 1
	}
	if !local {
		p.Image(deviceSceneArtwork[theme]["controller"], art, n.Contain)
	}
	target := art
	if local {
		target.X -= 166 * scale
	}
	p.Image(deviceSceneTarget(t, icon), target, n.Contain)
	if local {
		return
	}
	now := p.Now()
	recent := !changed.IsZero() && now.Sub(changed) < 900*time.Millisecond
	for i, r := range state.Routes {
		centre, lane := height/2, lanes[i].Lane
		x := func(value float32) float32 { return box.X + value*scale }
		y := func(value float32) float32 { return box.Y + value*scale }
		var path n.Path
		path.MoveTo(x(80), y(centre)).LineTo(x(98), y(centre))
		if lane < centre {
			path.QuadTo(x(110), y(centre), x(110), y(centre-12)).LineTo(x(110), y(lane+12)).QuadTo(x(110), y(lane), x(122), y(lane))
		} else if lane > centre {
			path.QuadTo(x(110), y(centre), x(110), y(centre+12)).LineTo(x(110), y(lane-12)).QuadTo(x(110), y(lane), x(122), y(lane))
		} else {
			path.LineTo(x(122), y(lane))
		}
		path.LineTo(x(320), y(lane))
		if lane < centre {
			path.QuadTo(x(332), y(lane), x(332), y(lane+12)).LineTo(x(332), y(centre-12)).QuadTo(x(332), y(centre), x(344), y(centre))
		} else if lane > centre {
			path.QuadTo(x(332), y(lane), x(332), y(lane-12)).LineTo(x(332), y(centre+12)).QuadTo(x(332), y(centre), x(344), y(centre))
		} else {
			path.LineTo(x(344), y(centre))
		}
		path.LineTo(x(350), y(centre))
		color, width := t.Border, float32(1)
		if r.Active {
			color, width = t.TextMuted, 1.4
		}
		if r.Status.Phase == app.PathUnavailable {
			path.Dash(0, 3*scale, 5*scale)
		}
		p.StrokePath(&path, width*scale, color)
		if !reduced && (r.Busy || recent && r.Active) {
			path.Dash(float32(-math.Mod(float64(now.UnixMilli())/1000, 1.2)/1.2)*300*scale, 8*scale, 292*scale)
			p.StrokePath(&path, 1.8*scale, t.TextMuted)
			p.AnimationFrameRate(30)
		}
		if r.Relay != "" {
			rect := n.Rect{X: x(201), Y: y(lane) - 10*scale, W: 42 * scale, H: 20 * scale}
			p.Fill(rect, t.Background, 4*scale)
			p.Stroke(rect, t.Border, 4*scale, .8*scale)
			// Text is a native child at this same lane, below.
		}
	}
	if !local {
		r := n.Rect{X: box.X + 77*scale, Y: box.Y + (height/2-3)*scale, W: 6 * scale, H: 6 * scale}
		p.Fill(r, t.Background, 3*scale)
		p.Stroke(r, t.TextMuted, 3*scale, scale)
	}
}
