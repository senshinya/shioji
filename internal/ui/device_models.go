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

func (v *View) deviceModels(c *n.Context, t *n.Theme, key string, info domain.HarnessInfo) {
	device := v.Model.ConnectionDeviceID(key)
	pref := v.Model.ModelPreference(device, info.ID)
	// Management retains manual order; only the send menu groups favorites.
	ordering := pref
	models := ordering.Ordered(info.Models, false)
	n.Row(c).FillWidth().Height(36).AlignItems(n.Start).Children(func() {
		n.Text(c, "模型").FontSize(14).FontWeight(500).FixedLineHeight(28).Grow(1)
		if deviceAction(c, "刷新").Disabled(v.Controller == nil || !v.deviceState(key).Connected).Clicked() {
			v.recheckAgent(key, info.ID)
		}
	})
	list := n.Column(c.Key("model-list")).FillWidth().Label("模型列表")
	dragged, over := n.DragOver[deviceModelDrag](list)
	dropped, released := n.Drop[deviceModelDrag](list)
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
			row := n.Row(c.Key(model.ID)).FillWidth().Height(40).Gap(8).AlignItems(n.Center).Radius(5).Label("模型：" + modelName(model)).Transition(n.ElementTransition{Position: true, Duration: motionAnimation(c, 160*time.Millisecond).Duration, Ease: n.EaseOut})
			if row.Hovered() || row.FocusWithin() {
				row.Background(t.Text.Alpha(.035))
			}
			row.Children(func() {
				starred := slices.Contains(pref.Favorites, model.ID)
				icon := modelStar
				if starred {
					icon = modelStarFilled
				}
				if iconButton(c, icon, "收藏 "+modelName(model)).TextColor(func() n.Color {
					if starred {
						return t.Text
					}
					return t.TextMuted
				}()).Clicked() {
					pref.Favorites = toggleModelValue(pref.Favorites, model.ID)
					v.Model.SetModelPreferences(device, info.ID, pref)
				}
				n.Text(c, modelName(model)).FontSize(12).FixedLineHeight(20).SingleLine().Grow(1).Tooltip(model.ID).TextColor(func() n.Color {
					if slices.Contains(pref.Hidden, model.ID) {
						return t.TextMuted
					}
					return t.Text
				}())
				grip := iconButton(c, modelGrip, "调整 "+modelName(model)+" 顺序").Cursor(n.CursorGrab).Drag(deviceModelDrag{device, info.ID, model.ID}).Tooltip("拖动排序，或使用上下方向键")
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
				if settingsSwitch(c, &shown).Label("在模型菜单显示 " + modelName(model)).Tooltip("在模型菜单显示").Changed() {
					pref.Hidden = toggleModelValue(pref.Hidden, model.ID)
					v.Model.SetModelPreferences(device, info.ID, pref)
				}
			})
		}
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
