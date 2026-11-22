package main

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

// Composer menus open into the conversation, clear of the software keyboard.
var composerMenuAbove = ui.PopoverMenuButtonOptions{At: ui.AnchorTopStart, Self: ui.AnchorBottomStart}

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

func (a *mobileApp) runControls(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, ready domain.Readiness, config domain.RunConfig, progress, modelSendSpacing float32, home, right bool) {
	m, target := a.model, editor.target
	unavailable := ready.ModelUnavailable(config.Model)
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
		if title := map[string]string{"low": "低", "medium": "中", "high": "高", "xhigh": "极高"}[effort]; title != "" {
			effort = title
		}
		detail = " · " + effort
	}

	permissionProgress := progress
	if home {
		permissionProgress = 0
	}
	composerPermissionControl(c, permissionProgress, ready.PermissionChoices, config, right, setConfig)
	ui.Spacer(c).PassThrough()
	title := "运行配置"
	if right {
		title = "右侧运行配置"
	}
	var modelControl ui.Element
	control := c.Key(target)
	if progress < .62 {
		modelControl = iconButton(control, title, terminalSymbol, inlineIconSize, nil)
	} else {
		p := palette(c)
		font := ui.Font{Size: controlTextSize * c.Preferences().TextScale, Weight: 400}
		modelControl = mobileButton(control, title, nil).AlignItems(ui.Center).Justify(ui.End).PaddingX(5).Gap(7).
			MaxWidth(max(mobileTapSize(c), 44+116*progress)).Shrink(1)
		if home {
			modelControl.PaddingX(0).Gap(6)
		}
		color := p.ink
		if unavailable {
			color = p.diffRemoved
		}
		ink := modelControl.AnimateColor("model-availability", color, ui.Animation{Duration: mobilePanelTransition.Duration, Ease: ui.EaseOut})
		modelControl.Children(func() {
			alignedSymbol(control, terminalSymbol, name+detail, font, inlineIconSize, p.ink)
			ui.RichText(control, ui.Span{Text: name, Color: ink}, ui.Span{Text: detail}).
				FontSize(font.Size).FontWeight(font.Weight).TextColor(p.ink).MinWidth(0).Shrink(1).MaxLines(1).Ellipsis("…").PassThrough()
		})
	}
	if unavailable {
		modelControl.Description(domain.ModelUnavailableMessage).Tooltip(domain.ModelUnavailableMessage)
	}
	agents := m.Readiness.Harnesses
	canChooseAgent := home && len(agents) > 1
	modelControl.Margin(0, modelSendSpacing, 0, 0).Value(modelName).KeepFocus().PopoverMenuButton(func(menu *ui.Menu) {
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
		for _, effort := range selected.Efforts {
			menuChoice(menu, effort, effort == config.Effort, func() {
				next := config
				next.Effort = effort
				setConfig(next)
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
	}, composerMenuAbove).Disabled(len(ready.Models) == 0 && !canChooseAgent && !unavailable)
}

func composerPermissionControl(c *ui.Context, progress float32, choices []domain.OptionChoice, config domain.RunConfig, right bool, setConfig func(domain.RunConfig)) {
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
	var control ui.Element
	if progress < .62 {
		control = iconButton(c, title, permissionSymbol, inlineIconSize, nil)
	} else {
		label := title
		if !right && name != title {
			label += "，" + name
		}
		control = iconAction(c, name, label, permissionSymbol, inlineIconSize, nil, 0, 13, 600, nil).PaddingX(2).Shrink(1).MaxWidthPercent(36)
	}
	control.Value(name).KeepFocus().PopoverMenuButton(func(menu *ui.Menu) {
		for _, choice := range choices {
			if menu.Item(choice.Name).Subtitle(choice.Description).Checked(config.Permission == choice.ID).Chosen() {
				next := config
				next.Permission = choice.ID
				setConfig(next)
			}
		}
	}, composerMenuAbove).Disabled(len(choices) == 0)
}
