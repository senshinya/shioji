package ui

import (
	"encoding/json"
	"errors"
	"strings"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/ui/assets"
)

const customProfileChoice = "custom-profile"

func profileChoice(id string) string {
	if domain.IsCustomAgent(id) {
		return customProfileChoice
	}
	return id
}

func customRuntimeName(id string) string {
	if id == domain.AgentAnthropic {
		return "Claude Code"
	}
	return "Codex"
}

func (p *deviceProfileEditor) runtimeInfo(e *deviceAgentEditor) *domain.RuntimeInstallation {
	if p.info.Installation != nil {
		return p.info.Installation
	}
	for _, info := range e.catalog {
		if info.ID == p.draft.HarnessID {
			return info.Installation
		}
	}
	return nil
}

func (p *deviceProfileEditor) needsRuntime(e *deviceAgentEditor) bool {
	install := p.runtimeInfo(e)
	return install == nil || install.Issue == domain.RuntimeMissing || install.Issue == domain.RuntimeIncompatible
}

func (v *View) customProfileFields(c *n.Context, t *n.Theme, state *app.DeviceState, e *deviceAgentEditor, p *deviceProfileEditor, disabled bool) (submit bool) {
	deviceProfileRow(c, "API 地址", func() {
		placeholder := "https://api.example.com/v1"
		if p.draft.HarnessID == domain.AgentAnthropic {
			placeholder = "https://api.anthropic.com"
		}
		field := setupField(c, "输入 API 地址", &p.auth.apiEndpoint, placeholder, disabled)
		submit = field.Submitted() && !field.Composing()
	})
	submit = agentAPIKeyField(c, deviceProfileRow, &p.auth, disabled, true) || submit
	deviceProfileRow(c, "模型 ID", func() {
		n.Row(c).FillWidth().Gap(4).AlignItems(n.Center).Children(func() {
			field := configurationTextField(c.Key("api-model"), &p.auth.apiModel).
				Grow(1).MinWidth(0).Label("模型 ID").Placeholder("输入模型 ID").Disabled(disabled)
			submit = field.Submitted() && !field.Composing() || submit
			label := "获取模型"
			if p.loadingModels {
				label = "读取中…"
			}
			trigger := configurationAction(c.Key("discover"), label).
				Disabled(disabled || p.loadingModels || strings.TrimSpace(p.auth.apiEndpoint) == "" || strings.TrimSpace(p.auth.apiCredential) == "")
			if v.menuOpen == nil {
				v.menuOpen = map[string]bool{}
			}
			key := "profile-api-models:" + p.key
			if trigger.Clicked() {
				if v.menuOpen[key] {
					v.menuOpen[key] = false
				} else {
					v.loadProfileModels(state, p)
				}
			}
			valid := p.modelEndpoint == p.auth.apiEndpoint && p.modelCredential == p.auth.apiCredential
			if !valid {
				v.menuOpen[key] = false
			}
			open := v.menuOpen[key]
			popover(c.Key(key), trigger, &open, func(panel n.Element) {
				compactMenuSurface(panel, t).Width(280).Role(n.RoleMenu).Children(func() {
					n.Scroll(c).FillWidth().MaxHeight(280).Children(func() {
						n.Column(c).FillWidth().FocusGroup(n.Vertical).Children(func() {
							for i, model := range p.models {
								row := control(c.Key(model.ID), compactMenuRowRadius).FillWidth().MinHeight(compactMenuRowHeight).
									PaddingX(compactMenuRowInset).Label(model.ID).Role(n.RoleMenuItemRadio).Checked(p.auth.apiModel == model.ID)
								if i == 0 {
									row.AutoFocus()
								}
								row.Children(func() {
									n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
										detail := model.ID
										if model.Name == model.ID {
											detail = ""
										}
										compactMenuChoiceText(c, t, model.Name, detail)
									})
								})
								if row.Clicked() {
									p.auth.apiModel = model.ID
									open = false
									field.Focus()
								}
							}
						})
					})
				})
			})
			v.menuOpen[key] = open
		})
	})
	if p.modelError != "" && p.modelEndpoint == p.auth.apiEndpoint && p.modelCredential == p.auth.apiCredential {
		n.Text(c, p.modelError).FontSize(configurationValueSize).TextColor(t.TextMuted).Padding(4, 0, 4)
	}
	deviceProfileRow(c, "运行环境", func() {
		status := " · 已就绪"
		if p.needsRuntime(e) {
			status = " · 未安装"
		}
		deviceProfileValue(c, customRuntimeName(p.draft.HarnessID)+status)
	})
	return submit
}

func (v *View) loadProfileModels(state *app.DeviceState, p *deviceProfileEditor) {
	p.modelRequest++
	request, epoch := p.modelRequest, state.ConnectionEpoch
	id := p.draft.HarnessID
	endpoint, key := p.auth.apiEndpoint, p.auth.apiCredential
	p.modelEndpoint, p.modelCredential = endpoint, key
	p.loadingModels, p.modelError = true, ""
	p.models = nil
	in := protocol.ProfileModelInput{HarnessID: id, API: domain.APIConfiguration{Endpoint: endpoint, Credential: key}}
	if !v.do(app.Job{ConnectionID: p.key, Method: "runtime.profile.models", Input: in, Apply: func(m *app.Model, body json.RawMessage, err error) {
		if p.modelRequest != request || v.deviceProfile != p {
			return
		}
		p.loadingModels = false
		current := m.DeviceStates[p.key]
		if current == nil || epoch != current.ConnectionEpoch || p.draft.HarnessID != id || p.auth.apiEndpoint != endpoint || p.auth.apiCredential != key {
			return
		}
		if err == nil {
			err = json.Unmarshal(body, &p.models)
		}
		if err != nil || len(p.models) == 0 {
			p.modelError = "无法读取模型列表，可直接输入模型 ID。"
			if err != nil {
				var remote *protocol.RemoteError
				if errors.As(err, &remote) {
					p.modelError = remote.Message
				}
			}
			return
		}
		v.menuOpen["profile-api-models:"+p.key] = true
	}}) {
		p.loadingModels = false
		p.modelError = "设备操作繁忙，可直接输入模型 ID。"
	}
}

func (v *View) profileIconPicker(c *n.Context, t *n.Theme, key, selected string, disabled bool, choose func(string)) {
	if v.menuOpen == nil {
		v.menuOpen = map[string]bool{}
	}
	menuKey := "profile-icon:" + key
	n.Row(c).FillWidth().Gap(8).PaddingX(configurationFieldInset).AlignItems(n.Center).Children(func() {
		profileIconView(c.Key("preview"), selected, 16, t.TextMuted)
		trigger := configurationAction(c.Key("pick"), "选择图标").Disabled(disabled)
		open := v.menuOpen[menuKey]
		if trigger.Clicked() {
			open = !open
		}
		popover(c.Key(menuKey), trigger, &open, func(panel n.Element) {
			compactMenuSurface(panel, t).Padding(4).Width(128).Role(n.RoleMenu).Children(func() {
				n.Row(c).Gap(2).FocusGroup(n.Horizontal).Children(func() {
					for _, item := range assets.ProfileIconChoices {
						checked := selected == item.ID
						button := control(c.Key(item.ID), 5).Size(28, 28).Label(item.Name).Tooltip(item.Name).
							Role(n.RoleMenuItemRadio).Checked(checked)
						if checked {
							button.Background(menuSelectionSurface(t)).AutoFocus()
						}
						button.Children(func() {
							size := float32(16)
							// The Anthropic artwork fills its viewBox. The other marks have inset ink.
							if item.ID == "claude" {
								size = 11
							}
							profileIconView(c, item.ID, size, t.TextMuted)
						})
						if button.Clicked() {
							choose(item.ID)
							open = false
							trigger.Focus()
						}
					}
				})
			})
		})
		v.menuOpen[menuKey] = open
	})
}

func profileIconView(c *n.Context, value string, size float32, color n.Color) n.Element {
	if picture := app.AgentProfileIconImage(value); picture != nil {
		return n.Image(c, n.NewBitmap(picture)).Size(size, size).Fit(n.Contain)
	}
	return n.Icon(c, harnessIcon(domain.HarnessDescriptor{Icon: value})).Size(size, size).TextColor(color)
}

func (v *View) saveDeviceProfileIcon(key string, e *deviceAgentEditor, info domain.HarnessInfo, icon string) {
	current := v.Model.DeviceStates[key]
	if current == nil || info.Profile == nil || e.busy || !current.Connected {
		return
	}
	epoch := current.ConnectionEpoch
	e.busy, e.error = true, ""
	in := protocol.ProfileIconInput{ID: info.ID, Previous: info.Profile.Icon, Icon: icon}
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.profile.icon", Input: in, Apply: func(m *app.Model, raw json.RawMessage, err error) {
		e.busy = false
		state := m.DeviceStates[key]
		if state == nil || state.ConnectionEpoch != epoch {
			return
		}
		var out protocol.AgentSetupResult
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		if err != nil {
			e.error = err.Error()
			return
		}
		if !out.Ready {
			e.error = out.Error
			return
		}
		e.applyDeviceProfileCatalog(m, state, out)
	}}) {
		e.busy, e.error = false, "设备操作繁忙，请重试"
	}
}
