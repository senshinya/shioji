package ui

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type scheduleDraft struct {
	task         domain.Schedule
	baseRule     domain.ScheduleRule
	interval     string
	contextEpoch string
	hours        bool
	git          domain.GitStatus
	runMenu      runMenuPhase
}

func (v *View) newSchedule(g *schedulePageState) {
	draft := &scheduleDraft{task: domain.Schedule{ID: domain.NewID(), Enabled: true,
		Rule: domain.ScheduleRule{Kind: "weekly", Time: "09:00", Days: []time.Weekday{1, 2, 3, 4, 5}}}, interval: "1", hours: true}
	g.draft, g.message, g.form = draft, "", n.ScrollState{}
	for _, w := range v.Model.Snapshot.Workspaces {
		if !w.Archived && scheduleRoute(v.Model, w.DeviceID).key != "" {
			v.scheduleDraftWorkspace(g, draft, w)
			break
		}
	}
}

func (v *View) editSchedule(g *schedulePageState, task domain.Schedule) {
	draft := &scheduleDraft{task: task, baseRule: task.Rule, interval: "1", hours: true}
	draft.task.Rule.Days = slices.Clone(task.Rule.Days)
	if task.Rule.Minutes > 0 {
		draft.hours = task.Rule.Minutes%60 == 0
		minutes := task.Rule.Minutes
		if draft.hours {
			minutes /= 60
		}
		draft.interval = strconv.Itoa(minutes)
	}
	g.draft, g.message, g.form = draft, "", n.ScrollState{}
	v.loadScheduleEditorContext(g, draft)
}

func (v *View) scheduleDraftWorkspace(g *schedulePageState, draft *scheduleDraft, w domain.Workspace) {
	draft.task.WorkspaceID, draft.task.DeviceID, draft.task.Zone = w.ID, w.DeviceID, ""
	draft.contextEpoch = ""
	draft.task.Git, draft.git = domain.GitChoice{}, domain.GitStatus{}
	draft.task.HarnessID = ""
	ready := v.scheduleReadiness(draft.task)
	draft.task.HarnessID = ready.HarnessID
	draft.task.Config = scheduleDefaultConfig(ready)
	v.loadScheduleEditorContext(g, draft)
}

func (v *View) loadScheduleEditorContext(g *schedulePageState, draft *scheduleDraft) {
	source := scheduleRoute(v.Model, draft.task.DeviceID)
	if !source.online || source.epoch == draft.contextEpoch {
		return
	}
	draft.contextEpoch = source.epoch
	w := draft.task.WorkspaceID
	current := func(m *app.Model) bool {
		return g.draft == draft && draft.task.WorkspaceID == w && scheduleRoute(m, source.device).epoch == source.epoch
	}
	v.do(app.Job{ConnectionID: source.key, Method: "schedule.zone", Apply: func(m *app.Model, body json.RawMessage, err error) {
		if err == nil && current(m) {
			var zone string
			if json.Unmarshal(body, &zone) == nil {
				draft.task.Zone = zone
			}
		}
	}})
	v.do(app.Job{ConnectionID: source.key, Method: "git.status", Input: protocol.GitQuery{WorkspaceID: w}, Apply: func(m *app.Model, body json.RawMessage, err error) {
		if err == nil && current(m) {
			var status domain.GitStatus
			if json.Unmarshal(body, &status) == nil {
				draft.git = status
			}
		}
	}})
}

func (v *View) scheduleReadiness(task domain.Schedule) domain.Readiness {
	if source := scheduleRoute(v.Model, task.DeviceID); source.key != "" {
		if state := v.Model.DeviceStates[source.key]; state != nil {
			ready, _ := state.Readiness.ForHarness(task.HarnessID)
			return ready
		}
		if source.key == "local" && v.Model.Snapshot.DeviceID == task.DeviceID {
			ready, _ := v.Model.Readiness.ForHarness(task.HarnessID)
			return ready
		}
	}
	return domain.Readiness{}
}

func scheduleDefaultConfig(ready domain.Readiness) domain.RunConfig {
	config := domain.RunConfig{Permission: "default"}
	for _, model := range ready.Models {
		if config.Model == "" || model.Default {
			config.Model, config.Effort = model.ID, model.DefaultEffort
		}
		if model.Default {
			break
		}
	}
	if !slices.Contains(ready.Permissions, config.Permission) && len(ready.Permissions) > 0 {
		config.Permission = ready.Permissions[0]
	}
	return config
}

func schedulePermission(id string, ready domain.Readiness) string {
	for _, choice := range ready.PermissionChoices {
		if choice.ID == id && choice.Name != "" {
			return choice.Name
		}
	}
	switch id {
	case "", "default":
		return "默认权限"
	case "read-only":
		return "只读"
	}
	return id
}

func (v *View) scheduleEditor(c *n.Context, t *n.Theme, g *schedulePageState, inset float32) {
	draft := g.draft
	v.loadScheduleEditorContext(g, draft)
	title := "新建定时任务"
	if draft.task.Revision != 0 {
		title = "编辑定时任务"
	}
	source := scheduleRoute(v.Model, draft.task.DeviceID)
	n.Column(c.Key("schedule-editor")).FillHeight().Grow(1).MinWidth(0).AlignItems(n.Center).Children(func() {
		n.Column(c).FillWidth().MaxWidth(scheduleEditorWidth+2*inset).Grow(1).MinHeight(0).Padding(0, inset, 16, inset).Children(func() {
			scheduleTitle(c, title, func() {
				if iconButton(c, conversationBackIcon, "返回任务列表").Disabled(g.busy).MarginStart(-scheduleEditorBackInset).Clicked() {
					g.draft, g.message = nil, ""
					g.refreshAt = time.Time{}
				}
			}, nil)
			n.Scroll(c.Key("schedule-editor-scroll")).FillWidth().Shrink(1).MinHeight(0).TrackScroll(&g.form).Label("定时任务设置").Children(func() {
				n.Column(c).FillWidth().PaddingY(16).Gap(scheduleEditorRowGap).Children(func() {
					scheduleFormRow(c, "任务名称", func() {
						configurationTextField(c, &draft.task.Name).
							Label("输入任务名称").Placeholder("例如，工作日提交摘要").Disabled(g.busy)
					})
					scheduleFormRow(c, "工作区", func() {
						v.scheduleWorkspaceControl(c, t, g, draft)
					}).AlignItems(n.Center)
					v.scheduleRuleEditor(c, t, g, draft)
					if next := scheduleNextRuns(draft, c.Now()); next != "" {
						scheduleFormRow(c, "运行预览", func() {
							n.Text(c, next).PaddingStart(configurationFieldInset).FontSize(configurationValueSize).
								FixedLineHeight(configurationTextLine).MinWidth(0).TextColor(t.TextMuted)
						}).AlignItems(n.Center)
					}
					n.Column(c.Key("schedule-instructions")).FillWidth().Margin(8, 0, 0, 0).Gap(6).Children(func() {
						configurationSectionTitle(c, "任务指令").FixedLineHeight(configurationTextLine)
						v.schedulePrompt(c, t, g, draft)
					})
				})
			})
			if g.message != "" {
				n.Text(c, g.message).FontSize(12).TextColor(t.Danger)
			}
			n.Row(c.Key("schedule-editor-footer")).FillWidth().Height(44).Shrink(0).Gap(8).AlignItems(n.Center).Children(func() {
				n.Spacer(c).Grow(1)
				if configurationAction(c, "取消").Disabled(g.busy).Clicked() {
					g.draft, g.message, g.refreshAt = nil, "", time.Time{}
				}
				if compactDialogPrimary(c, "保存任务").Disabled(!source.online || g.busy).Clicked() {
					task := draft.task
					task.Rule = draft.rule()
					if strings.TrimSpace(task.Name) == "" || strings.TrimSpace(task.Text) == "" || !task.Rule.Valid() || task.Config.Model == "" {
						g.message = "请填写任务名称、有效的触发时间和提示词，并选择模型"
						return
					}
					v.saveSchedule(g, source, task, true)
				}
			})
		})
	})
}

func scheduleFormRow(c *n.Context, label string, build func()) n.Element {
	return n.Row(c.Key(label)).FillWidth().Gap(scheduleEditorDetailGap).AlignItems(n.Start).Children(func() {
		n.Text(c, label).Width(scheduleEditorLabelWidth).Height(configurationControlHeight).Shrink(0).
			PaddingY(4).FontSize(configurationLabelSize).FixedLineHeight(configurationTextLine)
		n.Column(c).Grow(1).MinWidth(0).Children(build)
	})
}

func (v *View) scheduleWorkspaceControl(c *n.Context, t *n.Theme, g *schedulePageState, draft *scheduleDraft) {
	w, _ := scheduleWorkspace(v.Model, draft.task.WorkspaceID)
	label := w.Name
	if label == "" {
		label = "选择工作区"
	}
	deviceLabel := func(id string) string {
		name := v.Model.DeviceName(id)
		if !scheduleRoute(v.Model, id).online {
			name += " · 离线"
		}
		return name
	}
	device := ""
	if draft.task.DeviceID != "" {
		device = deviceLabel(draft.task.DeviceID)
	}
	editing := draft.task.Revision != 0
	var trigger n.Element
	if editing {
		trigger = n.Row(c).Tooltip(label).PaddingX(configurationFieldInset)
	} else {
		summary := label
		if device != "" {
			summary += " · " + device
		}
		trigger = control(c, configurationFieldRadius).Radius(configurationFieldRadius).
			MaxWidth(compactChoiceWidth).PaddingX(compactMenuInset+compactMenuRowInset).
			Background(t.Background).Border(1, t.Border).Label("选择工作区").Description(summary).Tooltip(summary).Disabled(g.busy)
	}
	trigger.FillWidth().Height(configurationControlHeight).Children(func() {
		n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
			compactMenuChoiceText(c, t, label, device)
			if !editing {
				inlineSymbol(c, chevron, label, n.Font{Family: t.Font, Size: compactMenuFontSize, Weight: 400}, 12, t.TextMuted)
			}
		})
	})
	if editing {
		return
	}
	var choices []menuChoice
	for _, workspace := range v.Model.Snapshot.Workspaces {
		if workspace.Archived || scheduleRoute(v.Model, workspace.DeviceID).key == "" {
			continue
		}
		choices = append(choices, menuChoice{id: workspace.ID, name: workspace.Name, detail: deviceLabel(workspace.DeviceID),
			checked: workspace.ID == w.ID, choose: func() { v.scheduleDraftWorkspace(g, draft, workspace) }})
	}
	v.choiceMenu(c, t, "schedule-workspace", trigger, choices)
}

func scheduleNextRuns(draft *scheduleDraft, now time.Time) string {
	if draft.task.Zone == "" {
		return ""
	}
	loc, err := time.LoadLocation(draft.task.Zone)
	if err != nil {
		return ""
	}
	rule := draft.rule()
	next := rule.Next(now, loc)
	if draft.task.Enabled && draft.task.Revision != 0 && draft.baseRule.Equal(rule) {
		next = draft.task.NextAt
	}
	if next.IsZero() {
		return ""
	}
	label := next.In(loc).Format("01/02 15:04")
	if following := rule.Next(next, loc); !following.IsZero() {
		label += "、" + following.In(loc).Format("01/02 15:04")
	}
	return label + " · " + loc.String()
}

func (draft *scheduleDraft) rule() domain.ScheduleRule {
	rule := domain.ScheduleRule{Kind: draft.task.Rule.Kind}
	switch rule.Kind {
	case "interval":
		rule.Minutes, _ = strconv.Atoi(draft.interval)
		if draft.hours && rule.Minutes <= 720 {
			rule.Minutes *= 60
		} else if draft.hours {
			rule.Minutes = 0
		}
	case "once":
		rule.Date, rule.Time = draft.task.Rule.Date, draft.task.Rule.Time
	case "weekly":
		rule.Days = slices.Clone(draft.task.Rule.Days)
		slices.Sort(rule.Days)
		rule.Time = draft.task.Rule.Time
	default:
		rule.Time = draft.task.Rule.Time
	}
	return rule
}

func (v *View) scheduleRuleEditor(c *n.Context, t *n.Theme, g *schedulePageState, draft *scheduleDraft) {
	scheduleFormRow(c, "触发时间", func() {
		n.Column(c).FillWidth().Gap(scheduleEditorDetailGap).AlignItems(n.Start).Children(func() {
			n.Row(c).FillWidth().Gap(scheduleEditorDetailGap).AlignItems(n.Center).Children(func() {
				kinds := []struct{ id, label string }{{"once", "一次"}, {"daily", "每天"}, {"weekly", "每周"}, {"interval", "固定间隔"}}
				choices := []menuChoice{}
				label := "选择频率"
				for _, kind := range kinds {
					active := draft.task.Rule.Kind == kind.id
					if active {
						label = kind.label
					}
					choices = append(choices, menuChoice{id: kind.id, name: kind.label, checked: active, choose: func() { draft.task.Rule.Kind = kind.id }})
				}
				v.choiceMenu(c, t, "schedule-rule", configurationChoiceField(c, "频率", label, g.busy).Width(96), choices)
				if draft.task.Rule.Kind == "interval" {
					configurationTextField(c, &draft.interval).Width(64).Label("间隔数值").Disabled(g.busy)
					unit := "分钟"
					if draft.hours {
						unit = "小时"
					}
					v.choiceMenu(c, t, "schedule-unit", configurationChoiceField(c, "间隔单位", unit, g.busy).Width(76),
						[]menuChoice{{id: "hours", name: "小时", checked: draft.hours, choose: func() { draft.hours = true }},
							{id: "minutes", name: "分钟", checked: !draft.hours, choose: func() { draft.hours = false }}})
					return
				}
				if draft.task.Rule.Kind == "once" {
					configurationTextField(c, &draft.task.Rule.Date).Width(112).Label("执行日期").Placeholder("YYYY-MM-DD").Disabled(g.busy)
				}
				configurationTextField(c, &draft.task.Rule.Time).Width(68).Label("执行时间").Placeholder("HH:mm").Disabled(g.busy)
			})
			if draft.task.Rule.Kind == "weekly" {
				n.Row(c.Key("weekdays")).Gap(2).Padding(2).Radius(7).Background(t.Surface).
					Border(1, t.Border).Children(func() {
					for i, name := range []string{"一", "二", "三", "四", "五", "六", "日"} {
						day := time.Weekday((i + 1) % 7)
						selected := slices.Contains(draft.task.Rule.Days, day)
						button := control(c.Key(day), 4).Size(28, 28).Radius(4).
							Role(n.RoleToggleButton).Checked(selected).Label("周" + name).Disabled(g.busy)
						color := t.TextMuted
						if selected {
							button.Background(t.Background).Border(1, t.Border)
							color = t.Text
						}
						button.Children(func() { n.Text(c, name).FontSize(configurationValueSize).TextColor(color) })
						if button.Clicked() {
							if selected {
								draft.task.Rule.Days = slices.DeleteFunc(draft.task.Rule.Days, func(d time.Weekday) bool { return d == day })
							} else {
								draft.task.Rule.Days = append(draft.task.Rule.Days, day)
							}
						}
					}
				})
			}
		})
	})
}

func (v *View) schedulePrompt(c *n.Context, t *n.Theme, g *schedulePageState, draft *scheduleDraft) {
	bg := t.Background
	if t.Dark {
		bg = t.Surface
	}
	fieldFeedback(c, n.Column(c.Key("schedule-prompt")).FillWidth().Padding(8).Gap(4).Radius(16).
		Background(bg)).Children(func() {
		if draft.git.Applicable || draft.task.Git.Branch != "" || draft.task.Git.NewWorktree {
			label := draft.task.Git.Branch
			if label == "" {
				label = "当前分支"
			}
			choices := []menuChoice{{id: "current", name: "当前分支", checked: draft.task.Git.Branch == "", choose: func() { draft.task.Git.Branch = "" }}}
			for _, branch := range draft.git.Branches {
				choices = append(choices, menuChoice{id: branch, name: branch, checked: branch == draft.task.Git.Branch, choose: func() { draft.task.Git.Branch = branch }})
			}
			n.Row(c.Key("schedule-git")).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
				if draft.git.Applicable || draft.task.Git.Branch != "" {
					v.choiceMenu(c, t, "schedule-branch", scheduleComposerControl(c, t, "分支", label, branchIcon, g.busy).MaxWidth(230), choices)
				}
				if draft.git.Applicable || draft.task.Git.NewWorktree {
					configurationCheckbox(c, &draft.task.Git.NewWorktree, "新建 worktree", n.Font{Family: t.Font, Size: configurationValueSize, Weight: 400}).Disabled(g.busy)
				}
			})
		}
		configurationTextArea(c, &draft.task.Text).Lines(4, 10).FontSize(14).FixedLineHeight(22).Padding(4, 8).
			Background(n.Transparent).Border(0, n.Transparent).Label("每次运行的提示词").
			Placeholder("描述每次运行时要执行的任务…").PlaceholderColor(subtle(t)).Disabled(g.busy)
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
			v.scheduleRunOptions(c, t, g, draft)
		})
	})
}

func scheduleComposerControl(c *n.Context, t *n.Theme, action, value string, icon *n.SVG, disabled bool) n.Element {
	font := n.Font{Family: t.Font, Size: configurationValueSize, Weight: 400}
	return control(c).Height(configurationControlHeight).MinWidth(0).PaddingX(8).Radius(6).
		Label(action).Description(value).Tooltip(value).Disabled(disabled).Children(func() {
		n.Row(c).Gap(5).MinWidth(0).AlignItems(n.Baseline).Children(func() {
			inlineSymbol(c, icon, value, font, 14, t.TextMuted).Shrink(0)
			n.Text(c, value).FontSize(font.Size).FixedLineHeight(configurationTextLine).MinWidth(0).Shrink(1).
				SingleLine().Ellipsis("…").TextColor(t.TextMuted)
			inlineSymbol(c, chevron, value, font, 12, t.TextMuted).Shrink(0)
		})
	})
}

func (v *View) scheduleRunOptions(c *n.Context, t *n.Theme, g *schedulePageState, draft *scheduleDraft) {
	ready := v.scheduleReadiness(draft.task)
	var permissions []menuChoice
	for _, id := range ready.Permissions {
		permissions = append(permissions, menuChoice{id: id, name: schedulePermission(id, ready), checked: id == draft.task.Config.Permission,
			choose: func() { draft.task.Config.Permission = id }})
	}
	v.choiceMenu(c, t, "schedule-permission", scheduleComposerControl(c.Key("permission"), t, "权限",
		schedulePermission(draft.task.Config.Permission, ready), shieldIcon, g.busy || len(permissions) == 0).MaxWidth(156), permissions)
	n.Spacer(c).Grow(1)
	var agents, models, efforts []menuChoice
	if len(ready.Harnesses) > 1 {
		for _, agent := range ready.Harnesses {
			agents = append(agents, menuChoice{id: agent.ID, name: agent.Name, checked: agent.ID == draft.task.HarnessID,
				disabled: agent.State != domain.RuntimeReady, choose: func() {
					draft.task.HarnessID = agent.ID
					draft.task.Config = scheduleDefaultConfig(v.scheduleReadiness(draft.task))
				}})
		}
	}
	modelLabel := draft.task.Config.Model
	if modelLabel == "" {
		modelLabel = "选择模型"
	}
	for _, model := range ready.Models {
		name := model.Name
		if name == "" {
			name = model.ID
		}
		active := model.ID == draft.task.Config.Model
		if active {
			modelLabel = name
			for _, effort := range model.Efforts {
				efforts = append(efforts, menuChoice{id: effort, name: effort, checked: effort == draft.task.Config.Effort,
					choose: func() { draft.task.Config.Effort = effort }})
			}
		}
		models = append(models, menuChoice{id: model.ID, name: name, checked: active, choose: func() {
			draft.task.Config.Model, draft.task.Config.Effort = model.ID, model.DefaultEffort
		}})
	}
	descriptor := ready.Descriptor()
	summary := modelLabel
	if draft.task.Config.Effort != "" {
		summary += " · " + draft.task.Config.Effort
	}
	trigger := scheduleComposerControl(c.Key("run"), t, "运行配置", summary, harnessIcon(descriptor),
		g.busy || len(models) == 0 && len(agents) == 0).MaxWidth(300)
	open := draft.runMenu != runMenuClosed
	if trigger.Clicked() {
		open = !open
		draft.runMenu = runMenuMain
	}
	popover(c.Key("schedule-run-menu"), trigger, &open, func(panel n.Element) {
		phase := draft.runMenu
		options, depth := models, 1
		switch phase {
		case runMenuMain:
			depth = 0
		case runMenuHarnesses:
			options = agents
		case runMenuEfforts:
			options = efforts
		}
		panel.AttachTo(trigger, n.AnchorTopEnd, n.AnchorBottomEnd)
		compactMenuSurface(panel, t).Width(240).Padding(4).Label("定时任务运行配置").Role(n.RoleMenu).Children(func() {
			configurationTabPanel(c.Key("schedule-run-pages"), strconv.Itoa(int(phase)), depth, configurationPanelFit, func() {
				if phase == runMenuMain {
					if len(agents) > 0 {
						if settingRow(c, "Agent", descriptor.Name+" ›").AutoFocus().Clicked() {
							draft.runMenu = runMenuHarnesses
						}
					}
					model := settingRow(c, "模型", modelLabel+" ›").Disabled(len(models) == 0)
					if len(agents) == 0 {
						model.AutoFocus()
					}
					if model.Clicked() {
						draft.runMenu = runMenuModels
					}
					if len(efforts) > 0 && settingRow(c, "推理强度", draft.task.Config.Effort+" ›").Clicked() {
						draft.runMenu = runMenuEfforts
					}
					return
				}
				if ghost(c, "‹  返回").FillWidth().Clicked() {
					draft.runMenu = runMenuMain
				}
				_, height := c.Size()
				n.Scroll(c).FillWidth().MaxHeight(min(300, height-76)).Children(func() {
					n.Column(c).FillWidth().FocusGroup(n.Vertical).Children(func() {
						focusNext := true
						for _, option := range options {
							row := settingRow(c.Key(option.id), option.name, "").Role(n.RoleMenuItemRadio).
								Checked(option.checked).Disabled(option.disabled).TooltipWhenTruncated(option.name)
							if option.checked {
								row.Background(menuSelectionSurface(t))
							}
							if focusNext && !option.disabled {
								row.AutoFocus()
								focusNext = false
							}
							if row.Clicked() {
								option.choose()
								draft.runMenu = runMenuMain
							}
						}
					})
				})
			}, false)
		})
	})
	if !open {
		draft.runMenu = runMenuClosed
	}
}

func (v *View) saveSchedule(g *schedulePageState, source scheduleConnection, input domain.Schedule, edited bool) {
	v.scheduleCommand(g, source, "schedule.save", input, func(body json.RawMessage) {
		saved, err := decodeSchedule(body, input)
		if err != nil {
			g.message = err.Error()
			return
		}
		cache := g.sources[source.key]
		if cache == nil {
			cache = &scheduleSource{device: source.device, epoch: source.epoch}
			g.sources[source.key] = cache
		}
		row := saved
		row.Text = ""
		i := slices.IndexFunc(cache.tasks, func(task domain.Schedule) bool { return task.ID == saved.ID })
		if i < 0 {
			cache.tasks = append(cache.tasks, row)
		} else {
			row.Latest = cache.tasks[i].Latest
			cache.tasks[i] = row
		}
		if edited {
			g.draft = nil
			g.pick(scheduleIdentity(saved))
		} else if g.detail.Schedule.ID == saved.ID {
			g.detail.Schedule = saved
		}
	})
}
