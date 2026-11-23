package main

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

// Composer menus open into the conversation, clear of the software keyboard.
func (a *mobileApp) composerRuntimeStatus(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, state domain.ReadinessState, active bool) {
	p := palette(c)
	title, ink := "当前 Agent 尚未就绪", p.warning
	if active {
		title = "下一轮 Agent 尚未就绪"
	}
	refreshing := state == domain.RuntimeRefreshing
	if refreshing {
		title, ink = "正在刷新运行配置…", p.secondary
	}
	mobileInlinePanel(c).PaddingX(panelInset).Shrink(0).Children(func() {
		row := ui.Row(c).MinHeight(mobileTapSize(c)).AlignItems(ui.Center)
		if !refreshing {
			row.Wrap()
		}
		row.Children(func() {
			label(c, title, captionTextSize, ink).Grow(1).Role(ui.RoleStatus)
			if !refreshing {
				action(c.Key("device"), "设备详情", func() {
					if editor.current(a, parent) {
						a.openDevice(parent)
					}
				}).PaddingX(6)
			}
		})
	})
}

func (a *mobileApp) runControls(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, ready domain.Readiness, config domain.RunConfig, unavailable, home, right bool) {
	m, target := a.model, editor.target
	current := func() bool {
		return editor.target == target && editor.current(a, parent)
	}
	setConfig := func(next domain.RunConfig) {
		if current() {
			m.SetConfig(target.key, next)
			if m.Error == domain.ModelUnavailableMessage {
				m.Error = ""
			}
			a.save()
			a.refresh()
		}
	}

	var selected domain.Model
	for _, model := range ready.Models {
		if model.ID == config.Model {
			selected = model
			break
		}
	}
	modelName := config.Model
	if selected.ID != "" {
		modelName = mobileModelName(selected)
	}
	name := modelName
	if name == "" {
		name = "运行配置"
	}
	detail := ""
	if effort := config.Effort; effort != "" {
		detail = composerEffortName(effort)
	}

	composerPermissionControl(c, ready.PermissionChoices, config, right, m.PermissionChangePending(target.session), func(permission string) {
		if current() {
			m.SelectPermission(target.key, ready, config, permission, func(job app.Job) bool {
				return a.controller != nil && a.controller.Do(job)
			})
			a.save()
			a.refresh()
		}
	})
	ui.Spacer(c).PassThrough()
	title := "运行配置"
	if right {
		title = "右侧运行配置"
	}
	control := c.Key(target)
	p := palette(c)
	font := ui.Font{Size: controlTextSize * c.Preferences().TextScale, Weight: 400}
	modelControl := mobileButton(control, title, nil).AlignItems(ui.Center).Justify(ui.End).PaddingX(5).Gap(6).
		MaxWidth(max(mobileTapSize(c), 160)).Shrink(1)
	if home {
		modelControl.PaddingX(0)
	}
	color := p.ink
	if unavailable {
		color = p.diffRemoved
	}
	ink := modelControl.AnimateColor("model-availability", color, ui.Animation{Duration: mobilePanelTransition.Duration, Ease: ui.EaseOut})
	modelControl.Children(func() {
		if home {
			alignedSymbol(control, terminalSymbol, name, font, inlineIconSize, p.ink)
		}
		oneLine(control, name, controlTextSize, ink).Shrink(1).PassThrough()
		alignedSymbol(control, downSymbol, name, font, 10, p.secondary)
		if detail != "" && !home {
			oneLine(control, detail, captionTextSize, p.secondary).Margin(0, 0, 0, 3).Shrink(1).PassThrough()
		}
	})
	if unavailable {
		modelControl.Description(domain.ModelUnavailableMessage).Tooltip(domain.ModelUnavailableMessage)
	}
	agents := m.Readiness.Harnesses
	canChooseAgent := home && len(agents) > 1
	modelControl.Margin(0, mobileclient.ComposerModelSendGap, 0, 0).Value(modelName).KeepFocus().Menu(func(menu *ui.Menu) {
		if len(agents) > 1 {
			agentName := ready.HarnessID
			for _, info := range agents {
				if info.ID == target.harness && info.Name != "" {
					agentName = info.Name
					break
				}
			}
			if canChooseAgent {
				// Keep the choices inside the popover on compact screens.
				menu.Item("Agent").Disabled(true)
				locked := m.HasPendingInputForSession(target.session) || editor.pending()
				for _, info := range agents {
					label := info.Name
					if label == "" {
						label = info.ID
					}
					if !menu.Item(label).Checked(info.ID == target.harness).Disabled(locked).Chosen() ||
						!current() || editor.pending() {
						continue
					}
					editor.Publish(m, target.key)
					if err := m.SelectHarness(info.ID); err != nil {
						m.Error = err.Error()
					} else if info.ID != target.harness {
						// Preserve the native document and discard queries for the previous Agent.
						editor.target = a.draftTarget()
						editor.query, editor.dismissed = mobileclient.ReferenceQuery{}, mobileclient.ReferenceQuery{}
						editor.search, editor.insertion, editor.choices = nil, nil, nil
					}
					a.save()
					a.refresh()
				}
			} else {
				menu.Item("Agent").Trailing(agentName).Disabled(true)
			}
			menu.Separator()
		}
		models := m.ModelsForMenu(target.device, ready.HarnessID, ready.Models)
		if len(models) == 0 {
			menu.Item("没有显示的模型").Disabled(true)
		}
		for _, model := range models {
			if !menu.Item(mobileModelName(model)).Checked(model.ID == config.Model).
				Disabled(ready.State != domain.RuntimeReady).Chosen() || !current() {
				continue
			}
			catalog := editor.readiness()
			index := slices.IndexFunc(catalog.Models, func(candidate domain.Model) bool { return candidate.ID == model.ID })
			if catalog.State == domain.RuntimeReady && index >= 0 {
				next := config
				next.Model, next.Effort = model.ID, catalog.Models[index].DefaultEffort
				setConfig(next)
			}
		}
		if len(selected.Efforts) > 0 {
			menu.Separator()
			menu.Submenu("推理强度", func(menu *ui.Menu) {
				for _, effort := range selected.Efforts {
					menuChoice(menu, composerEffortName(effort), effort == config.Effort, func() {
						next := config
						next.Effort = effort
						setConfig(next)
					})
				}
			})
		}
		if selected.Fast {
			menuChoice(menu, "Fast", config.Fast, func() {
				next := config
				next.Fast = !next.Fast
				setConfig(next)
			})
		}
		if ready.Plan {
			menuChoice(menu, "Plan", config.Plan, func() {
				next := config
				next.Plan = !next.Plan
				setConfig(next)
			})
		}
	}).Disabled(len(ready.Models) == 0 && !canChooseAgent && !unavailable)
}

func composerEffortName(effort string) string {
	switch effort {
	case "low":
		return "低"
	case "medium":
		return "中"
	case "high":
		return "高"
	case "xhigh":
		return "极高"
	default:
		return effort
	}
}

func composerPermissionControl(c *ui.Context, choices []domain.OptionChoice, config domain.RunConfig, right, changing bool, choose func(string)) {
	name := "权限"
	for _, choice := range choices {
		if config.Permission == choice.ID {
			name = choice.Name
			break
		}
	}
	title := "权限"
	if right {
		title = "右侧权限"
	}
	control := iconButton(c, title, permissionSymbol, inlineIconSize, nil)
	control.Value(name).KeepFocus().Menu(func(menu *ui.Menu) {
		for _, choice := range choices {
			if menu.Item(choice.Name).Subtitle(choice.Description).Checked(config.Permission == choice.ID).Chosen() {
				choose(choice.ID)
			}
		}
	}).Disabled(len(choices) == 0 || changing)
}
