package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

var scheduleClockIcon = designIcon("M12 6v6l4 2M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16Z")
var schedulePlayIcon = designIcon("M5 3l12 7-12 7Z")
var schedulePauseIcon = designIcon("M7 4v12m6-12v12")
var scheduleMinusIcon = designIcon("M6 10h8")

const scheduleInspectorWidth = 328

type schedulePageState struct {
	open, busy, confirmDelete bool
	selected                  scheduleKey
	workspace                 string
	filter, session, message  string
	refreshAt                 time.Time
	timer                     *time.Timer
	sources                   map[string]*scheduleSource
	detail                    domain.ScheduleDetail
	detailLoading             bool
	detailRequest             uint64
	draft                     *scheduleDraft
	deleteTarget              domain.Schedule
	list                      n.ListState
	history, form             n.ScrollState
	unconfirmedRuns           map[scheduleKey]string
}

type scheduleKey struct{ device, id string }

func scheduleIdentity(task domain.Schedule) scheduleKey {
	return scheduleKey{device: task.DeviceID, id: task.ID}
}

type scheduleSource struct {
	device, epoch string
	tasks         []domain.Schedule
	busy          bool
	err           string
}

type scheduleConnection struct {
	key, device, epoch string
	online             bool
}

func scheduleConnections(m *app.Model) []scheduleConnection {
	var result []scheduleConnection
	seen := make(map[string]bool)
	add := func(key, id string) {
		state := m.DeviceStates[key]
		if key == "local" && id == "" && state != nil {
			id = state.Snapshot.DeviceID
		}
		if key == "local" && id == "" && len(m.DeviceStates) == 0 && m.ConnectionID() == "local" {
			id = m.Snapshot.DeviceID
		}
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		src := scheduleConnection{key: key, device: id}
		if state != nil && state.Snapshot.DeviceID == id {
			src.epoch, src.online = state.ConnectionEpoch, state.Connected && state.ConnectionEpoch != ""
		} else if key == "local" && len(m.DeviceStates) == 0 && m.Snapshot.DeviceID == id && m.ConnectionID() == key {
			src.epoch, src.online = m.ConnectionEpoch, m.Connected && m.ConnectionEpoch != ""
		}
		result = append(result, src)
	}
	add("local", m.LocalDeviceID)
	for _, connection := range m.Connections {
		add(connection.ID, connection.DeviceID)
	}
	return result
}

func (v *View) schedulePageOpen() bool { return v.schedules != nil && v.schedules.open }

func (v *View) openSchedulePage() {
	if v.schedules == nil {
		v.schedules = &schedulePageState{sources: make(map[string]*scheduleSource)}
	}
	g := v.schedules
	g.open, g.session, g.refreshAt = true, "", time.Time{}
	v.workspaceEditor, v.skillReturn = nil, nil
	v.devices, v.searchOpen = false, false
	if v.archive != nil {
		v.archive.open, v.archive.viewing = false, ""
	}
}

func (v *View) closeSchedulePage() {
	if g := v.schedules; g != nil {
		g.open, g.session, g.confirmDelete = false, "", false
		if g.timer != nil {
			g.timer.Stop()
		}
	}
}

func (g *schedulePageState) pick(key scheduleKey) {
	g.selected, g.message, g.detail = key, "", domain.ScheduleDetail{}
	g.detailLoading = false
	g.detailRequest++
	g.history = n.ScrollState{}
	g.refreshAt = time.Time{}
}

func scheduleWorkspace(m *app.Model, id string) (domain.Workspace, bool) {
	i := slices.IndexFunc(m.Snapshot.Workspaces, func(w domain.Workspace) bool { return w.ID == id })
	if i < 0 {
		return domain.Workspace{}, false
	}
	return m.Snapshot.Workspaces[i], true
}

func scheduleRoute(m *app.Model, device string) scheduleConnection {
	for _, src := range scheduleConnections(m) {
		if src.device == device {
			return src
		}
	}
	return scheduleConnection{}
}

func (v *View) loadSchedules(c *n.Context, g *schedulePageState) {
	connections := scheduleConnections(v.Model)
	for key, source := range g.sources {
		if !slices.ContainsFunc(connections, func(conn scheduleConnection) bool {
			return conn.key == key && conn.device == source.device
		}) {
			delete(g.sources, key)
		}
	}
	if g.selected.device != "" && scheduleRoute(v.Model, g.selected.device).key == "" {
		g.pick(scheduleKey{})
		g.confirmDelete = false
	}
	for _, conn := range connections {
		if state := v.Model.DeviceStates[conn.key]; state != nil && state.Snapshot.DeviceID == conn.device && g.sources[conn.key] == nil {
			g.sources[conn.key] = &scheduleSource{device: conn.device, epoch: conn.epoch, tasks: slices.Clone(state.ScheduleSummaries)}
		}
	}
	now := c.Now()
	if v.Controller == nil || !g.open || v.devices || g.draft != nil || now.Before(g.refreshAt) {
		return
	}
	const refresh = 4 * time.Second
	g.refreshAt = now.Add(refresh)
	if g.timer != nil {
		g.timer.Stop()
	}
	g.timer = time.AfterFunc(refresh, c.Services().Invalidate)
	for _, conn := range connections {
		if !conn.online {
			continue
		}
		cache := g.sources[conn.key]
		if cache == nil || cache.epoch != conn.epoch {
			next := &scheduleSource{device: conn.device, epoch: conn.epoch}
			if cache != nil && cache.device == conn.device {
				next.tasks = cache.tasks
			}
			cache, g.sources[conn.key] = next, next
		}
		if cache.busy {
			continue
		}
		cache.busy = v.do(app.Job{ConnectionID: conn.key, Method: "schedule.list", Apply: func(m *app.Model, body json.RawMessage, err error) {
			cache.busy = false
			current := scheduleRoute(m, conn.device)
			if !current.online || current.epoch != conn.epoch || g.sources[conn.key] != cache {
				return
			}
			var tasks []domain.Schedule
			if err == nil {
				err = json.Unmarshal(body, &tasks)
			}
			if err != nil {
				cache.err = err.Error()
				return
			}
			for _, task := range tasks {
				if task.ID == "" || task.DeviceID != conn.device || task.Text != "" {
					cache.err = "设备返回了无效的定时任务列表"
					return
				}
			}
			cache.tasks, cache.err = tasks, ""
			if state := m.DeviceStates[conn.key]; state != nil && state.Snapshot.DeviceID == conn.device {
				state.ScheduleSummaries = slices.Clone(tasks)
			}
			if task, ok := g.task(g.selected); ok && task.DeviceID == conn.device {
				v.loadScheduleDetail(g, task, conn)
			}
		}})
	}
}

func (g *schedulePageState) task(key scheduleKey) (domain.Schedule, bool) {
	if key.id != "" {
		for _, cache := range g.sources {
			if cache.device != key.device {
				continue
			}
			for _, task := range cache.tasks {
				if task.ID == key.id {
					return task, true
				}
			}
		}
	}
	return domain.Schedule{}, false
}

func (v *View) loadScheduleDetail(g *schedulePageState, task domain.Schedule, source scheduleConnection) {
	key := scheduleIdentity(task)
	if g.selected != key || !source.online || !g.open || v.devices || g.detailLoading || g.draft != nil {
		return
	}
	g.detailRequest++
	request := g.detailRequest
	g.detailLoading = v.do(app.Job{ConnectionID: source.key, Method: "schedule.get", Input: protocol.ScheduleCommand{ID: task.ID},
		Apply: func(m *app.Model, body json.RawMessage, err error) {
			if g.detailRequest != request || g.selected != key {
				return
			}
			g.detailLoading = false
			current := scheduleRoute(m, source.device)
			if !current.online || current.epoch != source.epoch {
				return
			}
			var detail domain.ScheduleDetail
			if err == nil {
				err = json.Unmarshal(body, &detail)
			}
			if err != nil {
				g.message = err.Error()
				return
			}
			if detail.Schedule.ID != task.ID || detail.Schedule.DeviceID != source.device || detail.Schedule.WorkspaceID != task.WorkspaceID {
				g.message = "设备返回了无效的任务详情"
				return
			}
			g.detail = detail
		}})
}

func (v *View) scheduleCommand(g *schedulePageState, source scheduleConnection, method string, input any, done func(json.RawMessage)) {
	if g.busy || !source.online {
		return
	}
	g.message = ""
	g.busy = v.do(app.Job{ConnectionID: source.key, Method: method, Input: input, Apply: func(m *app.Model, body json.RawMessage, err error) {
		g.busy, g.refreshAt = false, time.Time{}
		if q, ok := input.(protocol.ScheduleCommand); ok && method == "schedule.run" {
			var remote *protocol.RemoteError
			if err == nil || errors.As(err, &remote) && remote.Code != "notSent" {
				delete(g.unconfirmedRuns, scheduleKey{device: source.device, id: q.ID})
			}
		}
		if err != nil {
			g.message = err.Error()
			return
		}
		current := scheduleRoute(m, source.device)
		if !current.online || current.epoch != source.epoch {
			g.message = "设备连接已改变，请重新核对任务状态"
			return
		}
		done(body)
	}})
	if !g.busy {
		g.message = "操作队列繁忙，请重试"
	}
}

func (v *View) schedulePage(c *n.Context, t *n.Theme) {
	g := v.schedules
	v.loadSchedules(c, g)
	width, _ := c.Size()
	task, selected := g.task(g.selected)
	if g.workspace != "" {
		if w, ok := scheduleWorkspace(v.Model, g.workspace); !ok || w.Archived || scheduleRoute(v.Model, w.DeviceID).key == "" {
			g.workspace = ""
		}
	}
	inset := float32(configurationInset)
	if width <= 1000 {
		inset = 24
	}
	n.Row(c.Key("schedule-page")).FillHeight().Grow(1).MinWidth(0).Children(func() {
		switch {
		case g.draft != nil:
			v.scheduleEditor(c, t, g, inset)
		case width <= 1000:
			page, depth := "catalog", 0
			if selected {
				page, depth = "detail", 1
			}
			configurationTabPanel(c.Key("schedule-compact-navigation"), page, depth, configurationPanelFill, func() {
				if selected {
					v.scheduleInspector(c, t, g, task, inset, true)
				} else {
					v.scheduleCatalog(c, t, g, inset).FillWidth()
				}
			}, false)
		default:
			motion := columnMotion(c, scheduleInspectorWidth)
			v.scheduleCatalog(c, t, g, inset).Transition(n.ElementTransition{Duration: motion.Duration, Ease: motion.Ease, Size: motion.Size})
			if selected {
				n.Column(c.Key("schedule-inspector-column")).FillHeight().Shrink(0).Clip().Transition(motion).Children(func() {
					v.scheduleInspector(c, t, g, task, 16, false)
				})
			}
		}
	})
	if g.confirmDelete {
		v.scheduleDeleteDialog(c, t, g, g.deleteTarget)
	}
}

func scheduleTitle(c *n.Context, heading string, navigation, trailing func()) {
	n.Row(c.Key("schedule-heading")).FillWidth().Height(48+archiveTitleTopInset).Padding(archiveTitleTopInset, 0, 0, 0).
		Gap(6).AlignItems(n.Center).DragWindow().Children(func() {
		if navigation != nil {
			navigation()
		}
		n.Text(c, heading).FontSize(20).FontWeight(500).FixedLineHeight(28).SingleLine().Ellipsis("…").Grow(1)
		if trailing != nil {
			trailing()
		}
	})
}

func (v *View) scheduleCatalog(c *n.Context, t *n.Theme, g *schedulePageState, inset float32) n.Element {
	return n.Column(c.Key("schedule-catalog")).FillHeight().Grow(1).MinWidth(0).AlignItems(n.Center).Children(func() {
		n.Column(c).FillWidth().MaxWidth(912).Grow(1).MinHeight(0).Padding(0, inset, 24, inset).Children(func() {
			scheduleTitle(c, "定时任务", nil, func() {
				if scheduleAction(c, t, "新建任务", plus).MarginEnd(-scheduleActionInset).Clicked() {
					v.newSchedule(g)
				}
			})
			rows := v.scheduleRows(g)
			n.Row(c.Key("schedule-filters")).Height(40).Margin(8, 0, 0, 0).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
				workspaces := []menuChoice{{id: "all", name: "全部工作区", checked: g.workspace == "", choose: func() { g.workspace = "" }}}
				workspaceLabel := "全部工作区"
				for _, w := range v.Model.Snapshot.Workspaces {
					if w.Archived || scheduleRoute(v.Model, w.DeviceID).key == "" {
						continue
					}
					active := g.workspace == w.ID
					if active {
						workspaceLabel = w.Name
					}
					workspaces = append(workspaces, menuChoice{id: w.ID, name: w.Name, detail: v.Model.DeviceName(w.DeviceID), checked: active, choose: func() { g.workspace = w.ID }})
				}
				v.choiceMenu(c, t, "schedule-workspace-filter", configurationChoiceField(c, "工作区筛选", workspaceLabel, false).Width(128), workspaces)
				states := []struct{ id, label string }{{"", "全部状态"}, {"active", "已启用"}, {"paused", "已暂停"}, {"offline", "设备离线"}}
				choices := []menuChoice{}
				statusLabel := states[0].label
				for _, state := range states {
					active := g.filter == state.id
					if active {
						statusLabel = state.label
					}
					choices = append(choices, menuChoice{id: state.id, name: state.label, checked: active, choose: func() { g.filter = state.id }})
				}
				v.choiceMenu(c, t, "schedule-status-filter", configurationChoiceField(c, "状态筛选", statusLabel, false).Width(102), choices)
				n.Spacer(c).Grow(1)
				n.Text(c, fmt.Sprintf("%d 个任务", len(rows))).FontSize(12).TextColor(t.TextMuted)
			})
			if g.message != "" && g.selected.id == "" {
				n.Text(c, g.message).FontSize(12).TextColor(t.Danger)
			}
			for _, conn := range scheduleConnections(v.Model) {
				if source := g.sources[conn.key]; source != nil && source.err != "" && conn.online {
					n.Text(c, v.Model.DeviceName(conn.device)+" · "+source.err).FontSize(12).TextColor(t.TextMuted)
				}
			}
			if len(rows) == 0 {
				label := "还没有定时任务"
				if slices.ContainsFunc(scheduleConnections(v.Model), func(conn scheduleConnection) bool {
					source := g.sources[conn.key]
					return conn.online && source != nil && source.busy && source.tasks == nil
				}) {
					label = "正在载入定时任务…"
				} else if g.workspace != "" || g.filter != "" {
					label = "此筛选下没有定时任务"
				}
				n.Text(c, label).Margin(24, 0, 0, 0).FontSize(13).TextColor(t.TextMuted)
				return
			}
			n.Row(c.Key("schedule-table-title")).Height(28).FillWidth().PaddingX(8).Gap(8).AlignItems(n.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				n.Text(c, "任务").FontSize(12).TextColor(t.TextMuted).Grow(1.55).MinWidth(0)
				n.Text(c, "工作区").FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0)
				n.Text(c, "下一次").FontSize(12).TextColor(t.TextMuted).Width(96).Shrink(0)
				n.Text(c, "最近一次").FontSize(12).TextColor(t.TextMuted).Width(68).Shrink(0)
			})
			g.list.Key = func(i int) any { return scheduleIdentity(rows[i]) }
			n.List(c.Key("schedule-list"), &g.list, len(rows), func(i int) {
				v.scheduleRow(c, t, g, rows[i])
			}).FillWidth().Grow(1).MinHeight(0).Label("定时任务列表")
		})
	})
}

func (v *View) scheduleRows(g *schedulePageState) []domain.Schedule {
	var tasks []domain.Schedule
	for _, conn := range scheduleConnections(v.Model) {
		cache := g.sources[conn.key]
		if cache == nil || cache.device != conn.device {
			continue
		}
		for _, task := range cache.tasks {
			if g.workspace != "" && g.workspace != task.WorkspaceID {
				continue
			}
			if (g.filter == "offline" && conn.online) || (g.filter == "active" && (!task.Enabled || !conn.online)) ||
				(g.filter == "paused" && (task.Enabled || !conn.online)) {
				continue
			}
			if w, ok := scheduleWorkspace(v.Model, task.WorkspaceID); ok && w.DeviceID == conn.device && !(g.filter == "active" && w.Archived) {
				tasks = append(tasks, task)
			}
		}
	}
	return tasks
}

func (v *View) scheduleRow(c *n.Context, t *n.Theme, g *schedulePageState, task domain.Schedule) {
	source := scheduleRoute(v.Model, task.DeviceID)
	w, _ := scheduleWorkspace(v.Model, task.WorkspaceID)
	next := scheduleNext(task, source.online, w.Archived)
	nextNote := scheduleRuleLabel(task.Rule)
	if !source.online && !task.NextAt.IsZero() {
		nextNote = "原定 " + scheduleDate(task.NextAt, task.Zone, "01/02 15:04")
	}
	latest := domain.ScheduleRunState("")
	if task.Latest != nil {
		latest = task.Latest.State
	}
	key := scheduleIdentity(task)
	row := control(c.Key(key)).FillWidth().Height(44).PaddingX(8).Radius(0).
		Background(n.Transparent).Label(task.Name)
	if key == g.selected {
		row.Background(t.SurfacePressed)
	}
	row.BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Children(func() {
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
			n.Text(c, task.Name).Grow(1.55).MinWidth(0).FontSize(13).SingleLine().Ellipsis("…").TooltipWhenTruncated(task.Name)
			n.Column(c).Grow(1).MinWidth(0).Children(func() {
				scheduleCell(c, w.Name, v.Model.DeviceName(task.DeviceID), t)
			})
			n.Column(c).Width(96).Shrink(0).Children(func() { scheduleCell(c, next, nextNote, t) })
			n.Row(c).Width(68).Shrink(0).AlignItems(n.Center).Children(func() { scheduleStatus(c, t, latest) })
		})
	})
	if row.Clicked() && key != g.selected {
		g.pick(key)
		v.loadScheduleDetail(g, task, source)
	}
}

func scheduleCell(c *n.Context, title, subtitle string, t *n.Theme) {
	n.Text(c, title).FontSize(12).FixedLineHeight(18).SingleLine().Ellipsis("…").TooltipWhenTruncated(title)
	n.Text(c, subtitle).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).SingleLine().Ellipsis("…").TooltipWhenTruncated(subtitle)
}

func scheduleNext(task domain.Schedule, online, archived bool) string {
	switch {
	case !online:
		return "设备离线"
	case archived:
		return "工作区已归档"
	case !task.Enabled:
		return "已暂停"
	case task.NextAt.IsZero():
		return "无下一次"
	}
	return scheduleDate(task.NextAt, task.Zone, "01/02 15:04")
}

func scheduleDate(at time.Time, zone, format string) string {
	if zone != "" && zone != "Local" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return at.In(loc).Format(format)
		}
	}
	return at.Local().Format(format)
}

func scheduleRuleLabel(rule domain.ScheduleRule) string {
	switch rule.Kind {
	case "once":
		return "一次"
	case "daily":
		return "每天"
	case "weekly":
		if slices.Equal(rule.Days, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}) {
			return "每个工作日"
		}
		days := []string{"日", "一", "二", "三", "四", "五", "六"}
		text := "每周"
		for _, day := range rule.Days {
			if int(day) >= 0 && int(day) < len(days) {
				text += days[day]
			}
		}
		return text
	case "interval":
		if rule.Minutes%60 == 0 {
			return fmt.Sprintf("每 %d 小时", rule.Minutes/60)
		}
		return fmt.Sprintf("每 %d 分钟", rule.Minutes)
	}
	return "—"
}

func scheduleStatus(c *n.Context, t *n.Theme, state domain.ScheduleRunState) {
	label, glyph, color := "未运行", scheduleMinusIcon, t.TextMuted
	switch state {
	case domain.ScheduleQueued:
		label, glyph = "待运行", scheduleClockIcon
	case domain.ScheduleStarting, domain.ScheduleRunning:
		label, glyph, color = "执行中", scheduleClockIcon, t.Text
	case domain.ScheduleApproval:
		label, glyph = "待处理", attentionIcon
	case domain.ScheduleCompleted:
		label, glyph = "已完成", check
	case domain.ScheduleFailed:
		label, glyph, color = "已失败", attentionIcon, t.Danger
	case domain.ScheduleInterrupted:
		label, glyph = "已停止", schedulePauseIcon
	case domain.ScheduleMissed:
		label = "已错过"
	case domain.ScheduleUnknown:
		label, glyph = "待核对", attentionIcon
	}
	inlineIconText(c, glyph, label, n.Font{Family: t.Font, Size: 12, Weight: 400}, 12, 5, color)
}

func (v *View) scheduleDeleteDialog(c *n.Context, t *n.Theme, g *schedulePageState, task domain.Schedule) {
	compactDialog(c.Key("schedule-delete"), t, &g.confirmDelete, 380, func(panel n.Element) {
		panel.Children(func() {
			compactDialogContent(c, "删除定时任务？", func() {
				n.Text(c, "「"+task.Name+"」将停止触发。已创建的会话保留在工作区。").FontSize(13)
			}, func() {
				n.Spacer(c).Grow(1)
				if configurationAction(c, "取消").Clicked() {
					g.confirmDelete = false
				}
				if compactDialogPrimary(c, "删除任务").Label("确认删除任务").Clicked() {
					g.confirmDelete = false
					v.scheduleCommand(g, scheduleRoute(v.Model, task.DeviceID), "schedule.delete", protocol.ScheduleCommand{ID: task.ID, Revision: task.Revision}, func(_ json.RawMessage) {
						if cache := g.sources[scheduleRoute(v.Model, task.DeviceID).key]; cache != nil {
							cache.tasks = slices.DeleteFunc(cache.tasks, func(row domain.Schedule) bool { return row.ID == task.ID })
						}
						g.pick(scheduleKey{})
					})
				}
			})
		})
	})
}

func decodeSchedule(body json.RawMessage, expected domain.Schedule) (domain.Schedule, error) {
	var saved domain.Schedule
	if err := json.Unmarshal(body, &saved); err != nil {
		return saved, err
	}
	if saved.ID != expected.ID || saved.WorkspaceID != expected.WorkspaceID || saved.DeviceID != expected.DeviceID || saved.Revision != expected.Revision+1 {
		return saved, errors.New("设备返回了无效的保存回执")
	}
	return saved, nil
}
