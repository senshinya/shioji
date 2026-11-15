package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"time"
)

var modelStar = svg("m10 2 2.4 4.9 5.4.8-3.9 3.8.9 5.4L10 14.3l-4.8 2.6.9-5.4L2.2 7.7l5.4-.8z")
var modelStarFilled = n.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><path fill="currentColor" d="m10 2 2.4 4.9 5.4.8-3.9 3.8.9 5.4L10 14.3l-4.8 2.6.9-5.4L2.2 7.7l5.4-.8z"/></svg>`))
var modelGrip = svg("M7 5h.1M13 5h.1M7 10h.1M13 10h.1M7 15h.1M13 15h.1")

type deviceModelDrag struct{ device, harness, id string }

func toggleModelValue(values []string, id string) []string {
	out := slices.Clone(values)
	if i := slices.Index(out, id); i >= 0 {
		return slices.Delete(out, i, i+1)
	}
	return append(out, id)
}
func modelOrderAt(models []domain.Model, from string, index int) []string {
	order := make([]string, 0, len(models))
	for _, m := range models {
		if m.ID != from {
			order = append(order, m.ID)
		}
	}
	index = max(0, min(index, len(order)))
	return slices.Insert(order, index, from)
}
func modelName(m domain.Model) string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

func (v *View) deviceModels(c *n.Context, t *n.Theme, key string, info domain.HarnessInfo, setupDevice ...string) {
	device := v.Model.ConnectionDeviceID(key)
	if len(setupDevice) > 0 {
		device = setupDevice[0]
	}
	pref := v.Model.ModelPreference(device, info.ID)
	stale := info.State == domain.RuntimeUnavailable || info.State == domain.RuntimeRefreshing
	// Management retains manual order; only the send menu groups favorites.
	ordering := pref
	models := ordering.Ordered(info.Models, false)
	header := n.Row(c).FillWidth().Height(configurationSectionHeader).AlignItems(n.Start)
	if len(setupDevice) > 0 {
		header.Height(28).AlignItems(n.Baseline)
	}
	header.Children(func() {
		size, line, weight := float32(14), float32(28), 500
		if len(setupDevice) > 0 {
			size, line, weight = 13, 20, 400
		}
		n.Text(c, "模型").FontSize(size).FontWeight(weight).FixedLineHeight(line).Grow(1)
		if stale {
			n.Text(c, "上次读取").FontSize(12).FixedLineHeight(line).TextColor(t.TextMuted).MarginEnd(8)
		}
		connected := v.deviceState(key).Connected
		if len(setupDevice) > 0 && key == "" {
			connected = v.sshCheck == sshSelecting || v.sshCheck == sshSaving
		}
		if configurationAction(c, "刷新").Disabled(v.Controller == nil || !connected).Clicked() {
			if len(setupDevice) > 0 {
				v.recheckWelcomeAgent(key, info.ID)
			} else {
				v.recheckAgent(key, info.ID)
			}
		}
	})
	var viewport n.Element
	height := float32(len(models)) * configurationRowHeight
	if len(setupDevice) > 0 {
		viewport = n.Scroll(c.Key("model-list-scroll")).FillWidth().Label("模型列表滚动区")
		height = min(float32(configurationListHeight), height)
	} else {
		viewport = n.Column(c.Key("model-list-scroll")).FillWidth().Shrink(0).Clip().Label("模型列表区域")
	}
	if !c.Preferences().ReduceMotion {
		height = viewport.AnimateTo("catalog-height", height, motionAnimation(c, 180*time.Millisecond))
	}
	viewport.Height(height).Children(func() {
		list := n.Column(c.Key("model-list")).FillWidth().Label("模型列表")
		dragged, over := n.DragOver[deviceModelDrag](list)
		dropped, released := n.Drop[deviceModelDrag](list)
		if stale {
			over = false
			released = false
		}
		preview := n.Local(list, "preview", func() []string { return nil })
		valid := over && dragged.device == device && dragged.harness == info.ID
		if valid {
			_, y, _ := list.PointerPosition()
			*preview = modelOrderAt(models, dragged.id, int(y/40))
			models = (app.ModelMenuPreferences{Order: *preview}).Ordered(info.Models, false)
		} else if released && dropped.device == device && dropped.harness == info.ID {
			_, y, _ := list.PointerPosition()
			pref.Order = modelOrderAt(models, dropped.id, int(y/40))
			for _, id := range ordering.Order {
				if !slices.Contains(pref.Order, id) {
					pref.Order = append(pref.Order, id)
				}
			}
			v.Model.SetModelPreferences(device, info.ID, pref)
			*preview = nil
			c.AnimationFrame()
		} else {
			*preview = nil
		}
		list.Children(func() {
			for index, model := range models {
				row := n.Row(c.Key(model.ID)).FillWidth().PaddingX(configurationHoverInset).Height(40).Gap(8).AlignItems(n.Center).Radius(5).Label("模型：" + modelName(model)).Transition(n.ElementTransition{Position: true, Duration: motionAnimation(c, 160*time.Millisecond).Duration, Ease: n.EaseOut, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
				if row.Hovered() || row.FocusWithin() {
					row.Background(t.Text.Alpha(.035))
				}
				row.Children(func() {
					starred := slices.Contains(pref.Favorites, model.ID)
					icon := modelStar
					if starred {
						icon = modelStarFilled
					}
					if iconButton(c, icon, "收藏 "+modelName(model)).Disabled(stale).TextColor(func() n.Color {
						if starred {
							return t.Text
						}
						return t.TextMuted
					}()).Clicked() {
						pref.Favorites = toggleModelValue(pref.Favorites, model.ID)
						v.Model.SetModelPreferences(device, info.ID, pref)
					}
					name := n.Text(c, modelName(model)).FontSize(12).FixedLineHeight(20).SingleLine().Grow(1).TextColor(func() n.Color {
						if stale || slices.Contains(pref.Hidden, model.ID) {
							return t.TextMuted
						}
						return t.Text
					}())
					if model.ID != modelName(model) {
						name.Tooltip(model.ID)
					} else {
						name.TooltipWhenTruncated(model.ID)
					}
					grip := iconButton(c, modelGrip, "调整 "+modelName(model)+" 顺序").Disabled(stale).Cursor(n.CursorGrab).Drag(deviceModelDrag{device, info.ID, model.ID}).Tooltip("拖动排序，或使用上下方向键")
					if !row.Hovered() && !row.FocusWithin() && !grip.Dragging() {
						grip.Opacity(0)
					}
					target := index
					if grip.Shortcut(0, n.KeyUp) {
						target--
					}
					if grip.Shortcut(0, n.KeyDown) {
						target++
					}
					if target != index && target >= 0 && target < len(models) {
						pref.Order = modelOrderAt(models, model.ID, target)
						for _, id := range ordering.Order {
							if !slices.Contains(pref.Order, id) {
								pref.Order = append(pref.Order, id)
							}
						}
						v.Model.SetModelPreferences(device, info.ID, pref)
						c.AnimationFrame()
					}
					shown := !slices.Contains(pref.Hidden, model.ID)
					if configurationSwitch(c, &shown).Disabled(stale).Label("在模型菜单显示 " + modelName(model)).Tooltip("在模型菜单显示").Changed() {
						pref.Hidden = toggleModelValue(pref.Hidden, model.ID)
						v.Model.SetModelPreferences(device, info.ID, pref)
					}
				})
			}
		})
	})
}

func (v *View) recheckAgent(key, id string) {
	epoch := v.deviceState(key).ConnectionEpoch
	v.do(app.Job{ConnectionID: key, Method: "runtime.check", Input: protocol.RuntimeConfiguration{HarnessID: id}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		current := m.DeviceStates[key]
		if current == nil || current.ConnectionEpoch != epoch {
			return
		}
		if err != nil {
			current.Error = err.Error()
			return
		}
		var ready domain.Readiness
		if json.Unmarshal(b, &ready) == nil {
			current.Readiness = ready
			m.RefreshDevices()
		}
	}})
}
