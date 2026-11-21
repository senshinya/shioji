package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"time"

	n "github.com/egoist/mygo/ui"
)

var deviceDetailsArrow = svg("M3 10h14M12 5l5 5-5 5")
var coffeeIcon = svg("M3 7h11v6a4 4 0 0 1-4 4H7a4 4 0 0 1-4-4zM14 7h1a3 3 0 0 1 0 6h-1M6 2v2M10 2v2M14 2v2")

type devicePowerUI struct {
	epoch      string
	pending    *domain.KeepAwakeInput
	checking   bool
	checkAfter time.Time
	error      string
}

func (v *View) powerUI() *devicePowerUI {
	root := v.splitRoot()
	key := v.Model.ConnectionID()
	if root.devicePower == nil {
		root.devicePower = map[string]*devicePowerUI{}
	}
	state := root.devicePower[key]
	epoch := v.Model.ConnectionEpoch
	if state == nil || state.epoch != epoch {
		state = &devicePowerUI{epoch: epoch}
		root.devicePower[key] = state
	}
	if state.checking && v.Model.DeviceHealth != nil && v.Model.DeviceHealth.ObservedAt.After(state.checkAfter) {
		state.checking = false
	}
	if pending, health := state.pending, v.Model.DeviceHealth; pending != nil && health != nil &&
		health.KeepAwake.Available && health.KeepAwake.Revision > pending.Revision && health.KeepAwake.Enabled == pending.Enabled {
		state.pending = nil
	}
	return state
}
func deviceHealthFresh(m *app.Model, now time.Time) bool {
	h := m.DeviceHealth
	return m.Connected && m.WorkerMinor >= 38 && h != nil && !h.ObservedAt.IsZero() && !h.ObservedAt.After(now) && now.Sub(h.ObservedAt) < 8*time.Second
}
func (v *View) changeKeepAwake(enabled bool) {
	m := v.Model
	u := v.powerUI()
	if !deviceHealthFresh(m, time.Now()) || u.pending != nil || u.checking || !m.DeviceHealth.KeepAwake.Available {
		return
	}
	key, epoch := m.ConnectionID(), m.ConnectionEpoch
	input := domain.KeepAwakeInput{Enabled: enabled, Revision: m.DeviceHealth.KeepAwake.Revision}
	u.pending, u.error = &input, ""
	if !v.do(app.Job{ConnectionID: key, Method: "device.keepAwake", Input: input, Apply: func(model *app.Model, body json.RawMessage, err error) {
		currentEpoch := model.ConnectionEpoch
		if state := model.DeviceStates[key]; state != nil {
			currentEpoch = state.ConnectionEpoch
		}
		if currentEpoch != epoch || u.pending != &input {
			return
		}
		u.pending = nil
		if err != nil {
			u.error = err.Error()
			u.checking = true
			u.checkAfter = time.Now()
			return
		}
		var report domain.DeviceHealth
		if err = json.Unmarshal(body, &report); err != nil {
			u.error = "无法读取保持唤醒回执"
			u.checking = true
			u.checkAfter = time.Now()
			return
		}
		expected := model.KnownDeviceID
		if state := model.DeviceStates[key]; state != nil {
			expected = state.Snapshot.DeviceID
		}
		if expected != "" && report.DeviceID != expected {
			u.error = "设备身份已改变，请重新连接"
			u.checking = true
			u.checkAfter = time.Now()
			return
		}
		if len(model.DeviceStates) > 0 {
			model.ApplyDeviceUpdate(key, func(p *app.Model) { p.SetDeviceHealth(report, time.Now()) })
		} else {
			model.SetDeviceHealth(report, time.Now())
		}
	}}) {
		u.pending = nil
		u.error = "设备操作队列繁忙，请重试"
	}
}
func (v *View) currentDeviceControl(c *n.Context, t *n.Theme) {
	m := v.Model
	key := m.ConnectionID()
	name := m.CurrentDeviceName()
	phase := v.currentConnectionPhase()
	status := connectionPhaseLabel(phase)
	if v.agentCheckPending() {
		status = "正在检查 Agent"
	}
	u := v.powerUI()
	h := m.DeviceHealth
	fresh := deviceHealthFresh(m, c.Now())
	if fresh {
		c.After(h.ObservedAt.Add(8 * time.Second).Sub(c.Now()))
	}
	device := control(c.Key("device-trigger:" + key)).Height(toolbarActionSize).PaddingX(6).Radius(6).Label("当前设备")
	if status != "" {
		device.Description(status)
	}
	device.Children(func() {
		n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
			color := t.TextMuted
			if m.Connected && phase == app.ConnectionIdle {
				color = t.Success
			}
			font := n.Font{Family: t.Font, Size: 11, Weight: 400}
			statusDotSize(c, name, font, color, 4, false).Label("设备连接指示")
			inlineIconText(c, v.deviceGlyph(key), name, font, 14, 6, t.TextMuted)
			if status != "" && v.connectionBlocker() == "" {
				n.Text(c, status).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			}
			inlineSymbol(c, chevron, name, font, 12, t.TextMuted)
		})
	})
	open := n.Local(device, "open", func() bool { return false })
	if device.Clicked() {
		*open = !*open
	}
	known := fresh && !u.checking && h.KeepAwake.Available
	on := known && h.KeepAwake.Enabled
	pending := u.pending != nil
	if pending {
		on = !u.pending.Enabled
	}
	powerError := u.error
	if powerError == "" && h != nil {
		powerError = h.KeepAwake.Error
	}
	quick := control(c.Key("keep-awake:"+key)).Size(toolbarActionSize, toolbarActionSize).Radius(6).Label("保持设备唤醒").
		Tooltip("保持设备唤醒").TextColor(t.TextMuted).Disabled(v.Controller == nil || !known || pending || u.checking)
	quick.Children(func() {
		if pending {
			serviceSpinner(c.Key("spinner"), "正在更新", n.Font{Family: t.Font, Size: 11}, t.TextMuted)
		} else {
			n.Icon(c.Key("coffee"), coffeeIcon).Size(16, 16)
		}
	})
	if pending {
		quick.Tooltip("正在更新保持唤醒").Description("正在更新保持唤醒")
	}
	if on {
		quick.Background(t.SurfacePressed)
	}
	if on {
		quick.Draw(func(p *n.Painter, r n.Rect) {
			p.Fill(n.Rect{X: r.X + r.W/2 - 2, Y: r.Y + r.H - 4, W: 4, H: 2}, t.TextMuted, 1)
		})
	}
	if quick.Clicked() {
		v.changeKeepAwake(!on)
	}
	n.PopoverBase(c.Key("device-panel:"+key), device, open, func(panel n.Element) {
		informationPopoverSurface(panel, t).AttachTo(device, n.AnchorBottomEnd, n.AnchorTopEnd).
			Margin(6, 0, 0, 0).Width(292).Transition(popupMotion(c)).Label("当前设备状态").Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() {
				n.Text(c, name).FontSize(13).FixedLineHeight(20).FillWidth().SingleLine().Ellipsis("…")

				n.Row(c).FillWidth().Gap(16).Children(func() {
					v.deviceLoadChart(c.Key("cpu"), t, "CPU", false, fresh)
					v.deviceLoadChart(c.Key("memory"), t, "内存", true, fresh)
				})
				if !fresh && h != nil {
					label := "历史数据"
					if !m.Connected {
						label = "离线 · 历史数据"
					}
					n.Text(c, label).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
				}
				n.Column(c).FillWidth().Gap(0).Children(func() {
					if key != "local" {
						n.Column(c).FillWidth().Gap(0).Children(func() {
							connection := "直接 SSH"
							if peer, ok := v.connectionPeer(key); ok {
								connection = "Tailnet · " + tailnetPathLabel(peer, c.Now())
							}
							deviceHealthRow(c, t, "连接", connection)
							deviceHealthRow(c, t, "往返延迟", deviceLatency(&app.DeviceState{Connected: m.Connected, Timing: m.ConnectionTiming, Paths: m.DevicePaths}, c.Now()))
						})
					}

					n.Row(c).FillWidth().Height(28).Gap(6).AlignItems(n.Center).Children(func() {
						font := n.Font{Family: t.Font, Size: 12, Weight: 400}
						inlineIconText(c, coffeeIcon, "保持唤醒", font, 14, 6, t.TextMuted).Transform(n.Translation(0, inlineCenterOffset("保持唤醒", font)))
						n.Spacer(c).Grow(1)
						if pending {
							n.Text(c.Key("power-pending"), "正在更新").FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
						} else if !known || u.checking {
							n.Text(c, "未知").FontSize(11).TextColor(t.TextMuted)
						}
						value := on
						if confirmedConfigurationSwitch(c, &value, on).Label("保持唤醒开关").Disabled(v.Controller == nil || !known || pending || u.checking).Changed() {
							v.changeKeepAwake(value)
						}
					})
					if powerError != "" {
						n.Text(c, powerError).FontSize(11).FixedLineHeight(18).FillWidth().TextColor(t.Danger).Label("保持唤醒错误")
					}
					v.deviceWorkerUpdateAction(c.Key("worker-update"), t, key, open)
					action := devicePopoverAction(c).Label("设备详情").Children(func() {
						font := n.Font{Family: t.Font, Size: 12, Weight: 400}
						inlineIconText(c, v.deviceGlyph(key), "设备详情", font, 14, 6, t.TextMuted)
						n.Spacer(c).Grow(1)
						inlineSymbol(c, deviceDetailsArrow, "设备详情", font, 14, t.TextMuted)
					})
					if action.Clicked() {
						*open = false
						v.deviceSelected = key
						v.deviceList = false
						v.devices = true
					}
				})
			})
		})
	})
	v.workerUpgradeConfirmation(c, t, key, v.deviceState(key))
}
func deviceHealthRow(c *n.Context, t *n.Theme, label, value string) {
	font := n.Font{Family: t.Font, Size: 12, Weight: 400}
	n.Row(c).FillWidth().Height(28).Gap(8).AlignItems(n.Center).Children(func() {
		n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Transform(n.Translation(0, inlineCenterOffset(label, font)))
		n.Text(c, value).Grow(1).MinWidth(0).FontSize(12).FixedLineHeight(20).TextAlign(n.End).FontFeatures("tnum").TextColor(t.TextMuted).SingleLine().Ellipsis("…").Label("设备" + label + "值").Transform(n.Translation(0, inlineCenterOffset(value, font)))
	})
}

func loadPercent(sample domain.DeviceLoad, memory bool) *float64 {
	if !memory {
		return sample.CPU
	}
	if sample.Memory == nil || sample.Memory.TotalBytes == 0 || sample.Memory.UsedBytes > sample.Memory.TotalBytes {
		return nil
	}
	percent := 100 * float64(sample.Memory.UsedBytes) / float64(sample.Memory.TotalBytes)
	return &percent
}

// Points are normalized to a fixed 60-second / 0–100% plot. Missing values and
// sampling pauses break the line, rather than inventing zeroes or interpolation.
func deviceLoadSegments(samples []domain.DeviceLoad, now time.Time, memory bool) [][]n.Point {
	var lines [][]n.Point
	var line []n.Point
	var previous time.Time
	flush := func() {
		if len(line) > 0 {
			lines = append(lines, line)
			line = nil
		}
	}
	for _, sample := range samples {
		elapsed := now.Sub(sample.At).Seconds()
		percent := loadPercent(sample, memory)
		if elapsed < 0 || elapsed > 60 || percent == nil || math.IsNaN(*percent) || math.IsInf(*percent, 0) || *percent < 0 || *percent > 100 {
			flush()
			previous = time.Time{}
			continue
		}
		if !previous.IsZero() && (sample.At.Sub(previous) > 5*time.Second || !sample.At.After(previous)) {
			flush()
		}
		line = append(line, n.Point{X: float32(1 - elapsed/60), Y: float32(1 - *percent/100)})
		previous = sample.At
	}
	flush()
	return lines
}
func (v *View) deviceLoadChart(c *n.Context, t *n.Theme, label string, memory, fresh bool) {
	h := v.Model.DeviceHealth
	now := c.Now()
	var samples []domain.DeviceLoad
	if h != nil {
		samples = h.Samples
		if !fresh {
			now = h.ObservedAt
		}
	}
	value := "—"
	if len(samples) > 0 && fresh {
		sample := samples[len(samples)-1]
		if now.Sub(sample.At) >= 0 && now.Sub(sample.At) < 8*time.Second {
			last := loadPercent(sample, memory)
			if last != nil {
				value = fmt.Sprintf("%.0f%%", *last)
			}
		}
	}
	lines := deviceLoadSegments(samples, now, memory)
	n.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
		n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
			n.Text(c, label).FontSize(12).FixedLineHeight(24).TextColor(t.TextMuted)
			n.Spacer(c).Grow(1)
			n.Text(c, value).FontSize(16).FixedLineHeight(24).FontFeatures("tnum").Label(label + " 当前负载")
		})
		n.Column(c).FillWidth().Height(54).Label(label + " 负载折线图").Draw(func(p *n.Painter, r n.Rect) {
			for _, fraction := range []float32{0, .5, 1} {
				y := r.Y + 2 + (r.H-4)*fraction
				p.Line(r.X, y, r.X+r.W, y, .5, t.Border)
			}
			color := t.TextMuted
			if !fresh {
				color = color.Alpha(.45)
			}
			for _, line := range lines {
				var path n.Path
				for i, point := range line {
					x, y := r.X+2+point.X*(r.W-4), r.Y+2+point.Y*(r.H-4)
					if i == 0 {
						path.MoveTo(x, y)
					} else {
						path.LineTo(x, y)
					}
				}
				if len(line) > 1 {
					p.StrokePath(&path, 1.25, color)
				}
			}
			if len(lines) > 0 {
				line := lines[len(lines)-1]
				end := line[len(line)-1]
				var dot n.Path
				dot.Circle(r.X+2+end.X*(r.W-4), r.Y+2+end.Y*(r.H-4), 1.75)
				p.FillPath(&dot, color)
			}
		})
	})
}
