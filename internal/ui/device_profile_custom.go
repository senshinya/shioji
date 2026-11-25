package ui

import (
	"encoding/json"
	"errors"
	"strings"

	mygo "github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
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

var profileIconChoices = []struct{ id, name string }{
	{"profile-sliders", "调节"}, {"profile-terminal", "终端"}, {"profile-code", "代码"},
	{"profile-cube", "方块"}, {"profile-spark", "星光"}, {"profile-bolt", "闪电"},
	{"profile-orbit", "轨道"}, {"claude", "Claude"},
}

func (v *View) profileIconPicker(c *n.Context, t *n.Theme, key, selected string, disabled bool, choose func(string), report func(error)) {
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
			compactMenuSurface(panel, t).Padding(4).Width(244).Children(func() {
				n.Row(c).Gap(2).FocusGroup(n.Horizontal).Children(func() {
					for _, item := range profileIconChoices {
						button := control(c.Key(item.id), 5).Size(27, 28).Label(item.name)
						if selected == item.id {
							button.Background(menuSelectionSurface(t)).AutoFocus()
						}
						button.Children(func() { profileIconView(c, item.id, 16, t.TextMuted) })
						if button.Clicked() {
							choose(item.id)
							open = false
							trigger.Focus()
						}
					}
				})
				deviceDivider(c)
				if configurationAction(c, "从文件选取…").FillWidth().AlignSelf(n.Start).Clicked() {
					open = false
					chooseFile := func() app.Update {
						paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: v.nativeWindow, Title: "选择 Profile 图标", Message: "PNG、JPEG 或 WebP 图片"})
						icon := ""
						if err == nil && len(paths) == 1 {
							icon, err = app.ReadAgentProfileIcon(paths[0])
						}
						return func(_ *app.Model) {
							if err != nil {
								report(err)
							} else if icon != "" {
								choose(icon)
							}
						}
					}
					if v.Controller != nil {
						v.Controller.AttachmentTask(chooseFile)
					}
				}
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
