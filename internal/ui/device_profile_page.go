package ui

import (
	"slices"
	"strings"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func deviceProfileRow(c *n.Context, label string, build func()) {
	configurationFormRow(c, label, configurationProfileForm, build)
}

func deviceProfileValue(c *n.Context, value string) {
	n.Text(c, value).FillWidth().PaddingX(configurationFieldInset).FontSize(configurationValueSize).
		FixedLineHeight(configurationTextLine).SingleLine().TooltipWhenTruncated(value)
}

func (v *View) deviceProfilePage(c *n.Context, t *n.Theme, key, device string, state *app.DeviceState, e *deviceAgentEditor, p *deviceProfileEditor) {
	op := v.agentSignIn(key, p.info.ID)
	api := p.draft.Mode == domain.AgentProfileAPI
	models := p.info.Profile != nil && p.info.Profile.Authorized
	active := !models && op != nil && op.status.Phase.Active()
	waiting := active && !api && op.status.Phase == domain.SignInWaiting
	disabled := !state.Connected || e.loading || v.Controller == nil
	busy := p.working || active
	leaveDisabled := busy && state.Connected

	title := "新增 Profile"
	if p.info.ID != "" {
		title = p.info.Name
	}
	width, _ := c.Size()
	top := float32(mainPageTopInset)
	if width < 900 {
		top = mainWindowTopInset
	}
	n.Column(c.Key("profile-heading")).FillWidth().Padding(top, configurationInset, 0).DragWindow().Children(func() {
		back := configurationBackAction(c.Key("back"), "Agent", "返回 Agent 列表").Disabled(leaveDisabled)
		if width < 900 {
			back.MarginStart(60)
		}
		if back.Clicked() {
			v.returnFromDeviceProfile(e, p)
			c.Services().Invalidate()
		}
		n.Text(c, title).Padding(8, 0, 0).FontSize(configurationTitleSize).FontWeight(500).
			FixedLineHeight(configurationTitleLine).SingleLine().TooltipWhenTruncated(title)
		n.Text(c, device).FontSize(configurationValueSize).TextColor(t.TextMuted).
			FixedLineHeight(configurationTextLine).SingleLine().TooltipWhenTruncated(device)
	})

	n.Scroll(c.Key("profile-scroll")).FillWidth().Grow(1).Children(func() {
		n.Column(c).FillWidth().Padding(configurationSubpageHeaderGap, configurationInset, configurationSectionGap).Children(func() {
			n.Column(c.Key("profile-form")).FillWidth().MaxWidth(configurationProfileWidth).Label("Profile 配置").Children(func() {
				deviceDivider(c)
				agents := deviceProfileProviders(e)
				index := slices.IndexFunc(agents, func(info domain.HarnessInfo) bool { return info.ID == p.draft.HarnessID })
				agent := domain.HarnessInfo{Name: p.draft.HarnessID}
				if index >= 0 {
					agent = agents[index]
				} else if source := slices.IndexFunc(e.catalog, func(info domain.HarnessInfo) bool { return info.ID == p.draft.HarnessID }); source >= 0 {
					agent = e.catalog[source]
				}
				locked := p.info.ID != "" || !p.working && p.request.ID != ""
				submit := false
				deviceProfileRow(c, "Agent", func() {
					if locked || len(agents) <= 1 {
						if agent.Name == "" {
							agent.Name = "不可用"
						}
						n.Row(c).FillWidth().PaddingX(configurationFieldInset).AlignItems(n.Baseline).Children(func() {
							icon := harnessIcon(domain.HarnessDescriptor{ID: agent.ID, Name: agent.Name, Icon: agent.Icon})
							inlineIconText(c, icon, agent.Name, n.Font{Family: t.Font, Size: configurationValueSize}, 16, 6, t.Text).
								Grow(1).MinWidth(0).SingleLine().TooltipWhenTruncated(agent.Name)
						})
						return
					}
					trigger := configurationChoiceField(c.Key("agent"), "选择 Agent", agent.Name, disabled || busy)
					var choices []menuChoice
					for _, item := range agents {
						icon := harnessIcon(domain.HarnessDescriptor{ID: item.ID, Name: item.Name, Icon: item.Icon})
						choices = append(choices, menuChoice{id: item.ID, name: item.Name, icon: icon, checked: item.ID == p.draft.HarnessID, choose: func() {
							p.draft.HarnessID = item.ID
							if !slices.Contains(item.ProfileModes, p.draft.Mode) {
								p.draft.Mode = item.ProfileModes[0]
							}
							p.auth.apiReveal = false
							p.method, p.error = "", ""
						}})
					}
					v.choiceMenu(c, t, "profile-agent:"+key, trigger, choices)
				})
				deviceProfileRow(c, "名称", func() {
					if locked {
						deviceProfileValue(c, p.draft.Name)
						return
					}
					field := configurationTextField(c.Key("name"), &p.draft.Name).Label("Profile 名称").
						Placeholder("例如 " + agent.Name + " 工作").Disabled(disabled || busy)
					if p.focusName && !disabled {
						field.Focus()
						p.focusName = false
					}
					submit = field.Submitted() && !field.Composing()
				})
				deviceProfileRow(c, "接入方式", func() {
					if locked || len(agent.ProfileModes) <= 1 {
						deviceProfileValue(c, profileModeLabel(p.draft.Mode))
						return
					}
					var options []configurationTabItem
					for _, mode := range agent.ProfileModes {
						options = append(options, configurationTabItem{ID: string(mode), Label: profileModeLabel(mode)})
					}
					next := domain.AgentProfileMode(configurationTabs(c.Key("mode"), string(p.draft.Mode), options, configurationSegmented, disabled || busy))
					if next != p.draft.Mode {
						p.auth.apiReveal = false
						p.draft.Mode, p.error = next, ""
					}
				})
				api = p.draft.Mode == domain.AgentProfileAPI
				if !models {
					n.Column(c.Key("profile-access")).FillWidth().Margin(configurationHeadingGap, 0, 0).Children(func() {
						modeIndex := 0
						if api {
							modeIndex = 1
						}
						configurationTabPanel(c.Key("method"), string(p.draft.Mode), modeIndex, configurationPanelFit, func() {
							deviceDivider(c)
							if api {
								submit = agentAPIFields(c, deviceProfileRow, &p.auth, disabled || busy) || submit
							} else {
								if p.info.ID != "" {
									agent = p.info
								}
								v.profileAuthorizationMethod(c, t, key, p, agent, disabled || busy)
								if !busy && p.info.ID == "" && len(profileSignInMethods(agent)) != 0 {
									n.Text(c, "将在浏览器中打开授权页。").Padding(8, 0, 0).FontSize(configurationValueSize).
										FixedLineHeight(configurationTextLine).TextColor(t.TextMuted)
								}
							}
							v.profileSignInStatus(c, t, p, op, disabled, state.Connected)
						}, false)
					})
				} else {
					v.profileSignInStatus(c, t, p, op, disabled, state.Connected)
				}
				label := deviceProfileAction(p, op)
				n.Row(c.Key("actions")).FillWidth().Padding(16, 0, 0).Gap(8).Justify(n.End).AlignItems(n.Center).Children(func() {
					cancel := "取消"
					cancelDisabled := leaveDisabled
					cancelingAuth := waiting && state.Connected
					if cancelingAuth {
						cancel, cancelDisabled = "取消授权", disabled || op.pending
					} else if p.info.ID != "" {
						cancel = "稍后设置"
					}
					cancelAction := configurationAction(c.Key("cancel"), cancel).Disabled(cancelDisabled)
					if cancelAction.Clicked() || !cancelDisabled && c.Shortcut(0, n.KeyEscape) {
						if cancelingAuth {
							v.sendSignIn(op, "cancel", nil)
						} else {
							v.returnFromDeviceProfile(e, p)
							c.Services().Invalidate()
						}
						return
					}
					cannotSubmit := disabled || p.working || active && (!waiting || op.status.Challenge.URL == "") ||
						p.info.ID == "" && (index < 0 || strings.TrimSpace(p.draft.Name) == "") ||
						api && !models && strings.TrimSpace(p.auth.apiEndpoint) == ""
					fill, ink := t.Text, t.Background
					if cannotSubmit {
						fill, ink = t.SurfacePressed, t.TextMuted
					}
					primary := controlFeedback(c, n.ButtonBase(c.Key("primary")), configurationFieldRadius, true).
						Height(configurationControlHeight).PaddingX(12).Radius(configurationFieldRadius).
						Background(fill).TextColor(ink).FontSize(configurationValueSize).Label(label).Disabled(cannotSubmit)
					primary.Children(func() { n.Text(c, label).FontSize(configurationValueSize).FontWeight(500) })
					if !cannotSubmit && (primary.Clicked() || submit) {
						if waiting {
							if err := openSignInLink(op.status.Challenge.URL); err != nil {
								p.error = "无法打开授权页，请重试"
							}
							return
						}
						v.submitDeviceProfile(key, state, e, p)
					}
				})
			})
		})
	})
}

func (v *View) profileAuthorizationMethod(c *n.Context, t *n.Theme, key string, p *deviceProfileEditor, agent domain.HarnessInfo, disabled bool) {
	methods := profileSignInMethods(agent)
	method := agentSignInMethod(key, &deviceAgentEditor{signInMethod: p.method}, methods)
	deviceProfileRow(c, "授权方式", func() {
		if len(methods) <= 1 {
			label := "不可用"
			if method != "" {
				label = signInMethodLabel(method)
			}
			deviceProfileValue(c, label)
			return
		}
		trigger := configurationChoiceField(c.Key("auth-method"), "选择授权方式", signInMethodLabel(method), disabled)
		var choices []menuChoice
		for _, item := range methods {
			choices = append(choices, menuChoice{id: string(item), name: signInMethodLabel(item), checked: method == item, choose: func() {
				p.method = item
				p.error = ""
			}})
		}
		v.choiceMenu(c, t, "profile-method:"+key, trigger, choices)
	})
}

func deviceProfileAction(p *deviceProfileEditor, op *agentSignInUI) string {
	models := p.info.Profile != nil && p.info.Profile.Authorized
	if p.working {
		if models {
			return "正在读取模型…"
		}
		return "正在创建…"
	}
	if models {
		return "重试读取模型"
	}
	api := p.draft.Mode == domain.AgentProfileAPI
	if op != nil && op.status.Phase.Active() {
		if api {
			return "正在连接…"
		}
		if op.status.Phase == domain.SignInWaiting {
			return "打开授权页"
		}
		return "正在准备…"
	}
	if p.info.ID != "" {
		if api {
			return "重试连接"
		}
		return "继续授权"
	}
	if api {
		return "创建并连接"
	}
	return "创建并登录"
}

func (v *View) profileSignInStatus(c *n.Context, t *n.Theme, p *deviceProfileEditor, op *agentSignInUI, disabled, connected bool) {
	message := p.error
	hint := ""
	color := t.Danger
	switch {
	case message != "":
	case !connected:
		message = "设备未连接，恢复后可继续。"
		color = t.TextMuted
	case p.info.Profile != nil && p.info.Profile.Authorized:
		if !p.working {
			message = p.info.Error
			if message == "" {
				message = "模型目录暂不可用"
			}
			hint = "Profile 已接入，可重试读取模型。"
		}
	case op != nil:
		message = op.status.Message
		switch op.status.Phase {
		case domain.SignInCanceled:
			if message == "" {
				message = "已取消授权"
			}
			color = t.TextMuted
		case domain.SignInWaiting:
			color = t.TextMuted
			if p.draft.Mode == domain.AgentProfileAccount && message == "" {
				message = "等待账号授权"
				hint = "在浏览器中完成授权后，回到 Shioji。"
			}
		case domain.SignInStarting:
			color = t.TextMuted
		}
	}
	if p.auth.apiCredential != "" {
		message = strings.ReplaceAll(message, p.auth.apiCredential, "••••")
	}
	if hint == "" && message != "" && color == t.Danger && p.info.Profile != nil && !p.info.Profile.Authorized {
		hint = "Profile 已创建，可重试或稍后从 Agent 列表继续。"
	}
	if message != "" {
		n.Column(c.Key("profile-status")).FillWidth().Padding(12, 0, 0).Children(func() {
			n.Text(c, message).FillWidth().FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).TextColor(color)
			if hint != "" {
				n.Text(c, hint).FillWidth().FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted)
			}
		})
	}
	if op != nil && op.status.Phase == domain.SignInWaiting {
		if p.info.Profile != nil && p.info.Profile.Authorized {
			return
		}
		challenge := op.status.Challenge
		if challenge.Code != "" {
			n.Row(c.Key("profile-auth-code")).FillWidth().Padding(4, 0, 0).Gap(8).AlignItems(n.Center).Children(func() {
				n.Text(c, challenge.Code).Grow(1).Font(v.Model.Preferences.CodeFont).FontSize(13).Selectable()
				if configurationAction(c.Key("copy-code"), "复制验证码").Disabled(disabled).Clicked() {
					c.WriteClipboard(challenge.Code)
				}
			})
		}
		v.agentManualAuthorization(c, op, disabled)
	}
}
