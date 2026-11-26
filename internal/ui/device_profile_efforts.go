package ui

import (
	"encoding/json"
	"errors"
	"slices"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (v *View) deviceProfileEfforts(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo, disabled bool, minor int) {
	if minor < protocol.ProfileEffortsMinor || info.Profile == nil || !info.Profile.Authorized || !info.Profile.SupportsEffortOverrides() {
		return
	}
	index := slices.IndexFunc(info.Models, func(model domain.Model) bool { return model.ID == e.effortModel })
	if index < 0 {
		index = max(0, slices.IndexFunc(info.Models, func(model domain.Model) bool { return model.Default }))
	}
	model := info.Models[index]
	disabled = disabled || info.State != domain.RuntimeReady || v.Controller == nil
	if e.effortDrafts == nil {
		e.effortDrafts = make(map[string]*app.ProfileEffortDraft)
	}
	draft := e.effortDrafts[model.ID]
	if draft == nil {
		draft = &app.ProfileEffortDraft{}
		e.effortDrafts[model.ID] = draft
	}
	draft.Sync(info, model)
	n.Column(c).FillWidth().Margin(12, 0, 0).Padding(8, 0, 0).BorderWidth(.5, 0, 0, 0).BorderColor(t.Border).Children(func() {
		if len(info.Models) > 1 {
			deviceConfigRow(c, "配置模型", func() {
				trigger := configurationChoiceField(c, "选择配置模型", modelName(model), disabled)
				choices := make([]menuChoice, 0, len(info.Models))
				for _, entry := range info.Models {
					choices = append(choices, menuChoice{id: entry.ID, name: modelName(entry), checked: entry.ID == model.ID, choose: func() {
						e.effortModel, e.effortError = entry.ID, ""
					}})
				}
				v.choiceMenu(c, t, "profile-effort-model:"+key+":"+info.ID, trigger, choices)
			})
		}
		deviceConfigRow(c, "推理档位", func() {
			n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
				field := configurationTextField(c.Key(model.ID), &draft.Text).Grow(1).MinWidth(0).Label("推理档位").
					Placeholder("用逗号分隔").Disabled(disabled)
				save := configurationAction(c.Key("save"), "保存").Label("保存推理档位").Disabled(disabled || draft.Manual() && !draft.Changed())
				if !disabled && (save.Clicked() || field.Submitted() && !field.Composing() && draft.Changed()) {
					v.saveProfileEffort(key, e, draft.Input(info.ID, model.ID, false))
				}
			})
		})
		deviceConfigRow(c, "", func() {
			n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
				mode := "自动"
				if draft.Manual() {
					mode = "手工配置"
				}
				n.Text(c, mode).PaddingX(configurationFieldInset).TextColor(t.TextMuted).FontSize(configurationValueSize).Grow(1)
				if draft.Changed() && configurationAction(c.Key("discard"), "撤销").Label("撤销推理档位修改").Disabled(disabled).Clicked() {
					*draft = app.ProfileEffortDraft{}
					e.effortError = ""
				}
				if draft.Manual() && configurationAction(c.Key("automatic"), "恢复自动").Disabled(disabled).Clicked() {
					v.saveProfileEffort(key, e, draft.Input(info.ID, model.ID, true))
				}
			})
		})
		if e.effortError != "" {
			n.Text(c, e.effortError).FontSize(configurationValueSize).TextColor(t.Danger)
		}
	})
}

func (v *View) saveProfileEffort(key string, e *deviceAgentEditor, in protocol.ProfileEffortInput) {
	state := v.deviceState(key)
	epoch, device := state.ConnectionEpoch, v.Model.ConnectionDeviceID(key)
	e.busy, e.effortError = true, ""
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.profile.effort", Input: in, Apply: func(m *app.Model, raw json.RawMessage, err error) {
		e.busy = false
		state := m.DeviceStates[key]
		if state == nil || state.ConnectionEpoch != epoch || m.ConnectionDeviceID(key) != device {
			if e.openID == in.ID {
				e.effortError = "连接已变化，请刷新模型以确认保存结果"
			}
			return
		}
		var out protocol.AgentSetupResult
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		if err == nil && !out.Ready {
			err = errors.New(out.Error)
		}
		if err != nil {
			if e.openID == in.ID {
				e.effortError = err.Error()
			}
			return
		}
		if e.openID == in.ID {
			delete(e.effortDrafts, in.Model)
		}
		e.applyDeviceProfileCatalog(m, state, out)
	}}) {
		e.busy, e.effortError = false, "设备操作繁忙，请重试"
	}
}
