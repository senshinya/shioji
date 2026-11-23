package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobileScheduleDetailView struct {
	draw         func(*ui.Context)
	edit, remove func()
	busy         func() bool
}

func (a *mobileApp) openSchedule(parent *mygo.Window, seed domain.Schedule, scope string, changed, removed func()) {
	var view mobileScheduleDetailView
	w := a.page("任务详情", func(c *ui.Context) {
		if view.draw != nil {
			view.draw(c)
		}
	})
	view = a.scheduleDetailView(parent, w, seed, scope, changed, removed, nil)
	mygo.SetBarItems(w, mygo.BarItem{Title: "编辑任务", Icon: toolbarImage(pencilSymbol), OnClick: view.edit},
		mygo.BarItem{Title: "删除任务", Symbol: "trash", OnClick: view.remove})
	a.pushNative(parent, w)
}

func (a *mobileApp) scheduleDetailView(parent, w *mygo.Window, seed domain.Schedule, scope string,
	changed, removed, closeInspector func()) mobileScheduleDetailView {
	detail := domain.ScheduleDetail{Schedule: seed}
	req := mobileRequest{}
	read := scheduleObservation{}
	var connectionProblem string
	deadline := time.Now().Add(sessionCatalogTimeout)
	loaded := a.preview
	if a.preview {
		if mock := a.previewSchedule(); sameSchedule(seed, mock.Schedule) {
			detail = mock
		}
	} else if err := a.openDeviceFromResults(parent, seed.DeviceID); err != nil {
		connectionProblem = err.Error()
	} else {
		a.pageDevice[w] = a.deviceSelection
	}
	confirming := false
	runKey := "schedule-run:" + seed.ID
	var runID string
	apply := func(in domain.ScheduleDetail) error {
		if in.Schedule.Revision < detail.Schedule.Revision {
			return fmt.Errorf("设备返回了较早的任务版本")
		}
		if !loaded {
			runID = a.model.Drafts[runKey]
		}
		detail, loaded = in, true
		if runID != "" && slices.ContainsFunc(in.Runs, func(run domain.ScheduleRun) bool { return run.ID == runID && run.Manual }) {
			runID = ""
			delete(a.model.Drafts, runKey)
			a.save()
		}
		return nil
	}
	reload := func() {
		read.next = time.Time{}
		w.Invalidate()
	}
	reconnect := func() {
		if !a.pageCurrent(w) {
			return
		}
		connectionProblem = ""
		deadline = time.Now().Add(sessionCatalogTimeout)
		if a.endpoint.DeviceID == seed.DeviceID {
			a.retryConnection()
		} else if err := a.openDeviceFromResults(parent, seed.DeviceID); err != nil {
			connectionProblem = err.Error()
		} else {
			a.pageDevice[w] = a.deviceSelection
		}
		w.Invalidate()
	}
	canAct := func() bool {
		return a.pageCurrent(w) && !req.busy && !confirming && loaded && (a.preview ||
			a.scheduleConnected(seed) && read.epoch == a.model.ConnectionEpoch && read.fresh)
	}
	canEdit := func() bool {
		workspace := a.workspace(seed.WorkspaceID)
		return canAct() && workspace.ID != "" && workspace.DeviceID == seed.DeviceID && !workspace.Archived
	}
	setEnabled := func(enabled bool) {
		if !canEdit() {
			return
		}
		if a.preview {
			detail.Schedule.Enabled = enabled
			return
		}
		in := detail.Schedule
		in.Enabled = enabled
		a.pageJob(w, &req, "schedule.save", in, decodePage(func(saved domain.Schedule) error {
			if !validScheduleSave(in, saved) {
				return fmt.Errorf("设备返回了无效的保存回执")
			}
			detail.Schedule = saved
			reload()
			changed()
			return nil
		}))
	}
	runNow := func() {
		if !canEdit() {
			return
		}
		if a.stateWriter == nil {
			req.err = "无法保存定时任务运行标识"
			return
		}
		task := detail.Schedule
		if runID == "" {
			runID = domain.NewID()
		}
		a.model.Drafts[runKey] = runID
		a.save()
		in := protocol.ScheduleCommand{ID: task.ID, Revision: task.Revision, RunID: runID}
		a.pageJobPrepared(w, &req, "schedule.run", in, a.stateWriter.FlushLatest, decodePage(func(run domain.ScheduleRun) error {
			if run.ID != in.RunID || run.ScheduleID != task.ID {
				return fmt.Errorf("执行服务返回了其他任务的回执")
			}
			runID = ""
			delete(a.model.Drafts, runKey)
			a.save()
			detail.Runs = slices.DeleteFunc(detail.Runs, func(existing domain.ScheduleRun) bool { return existing.ID == run.ID })
			detail.Runs = append([]domain.ScheduleRun{run}, detail.Runs...)
			reload()
			changed()
			a.openScheduleReceipt(w, task, run)
			return nil
		}))
	}
	view := mobileScheduleDetailView{
		busy: func() bool { return req.busy || confirming },
	}
	heading := func(c *ui.Context, name, context string) {
		if closeInspector == nil {
			mobilePageContext(c, name, context)
			return
		}
		p := palette(c)
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).PaddingY(4).Children(func() {
				label(c, name, 16, p.ink).FontWeight(650)
				if context != "" {
					label(c, context, captionTextSize, p.secondary)
				}
			})
			iconButton(c, "关闭任务详情", closeSymbol, 15, closeInspector).Disabled(view.busy())
		})
	}
	zoneLabel := func(task domain.Schedule) string {
		if task.Zone == "" {
			return ""
		}
		return "执行设备时区 · " + task.Zone
	}
	view.draw = func(c *ui.Context) {
		if !req.busy && !confirming {
			a.observeSchedule(c, w, &read, seed, apply)
		}
		if !loaded {
			caption, problem := "正在读取定时任务", connectionProblem
			nextStatus := ""
			var retry func()
			upgrade := false
			switch {
			case problem != "":
				nextStatus, retry = "设备离线", reconnect
			case a.scheduleConnected(seed):
				if read.request.err != "" {
					problem, retry = "无法读取定时任务", reload
				}
			case a.model.Connected && a.model.ConnectionEpoch != "" && a.model.Snapshot.DeviceID == seed.DeviceID:
				problem, upgrade = "更新执行服务后可查看此任务", true
				nextStatus = "执行服务需更新"
			case a.connectionPhase() == app.ConnectionUnavailable || a.connectionPhase() == app.ConnectionSyncFailed || !c.Now().Before(deadline):
				problem, retry = "无法连接任务所属设备", reconnect
				nextStatus = "设备离线"
			default:
				caption = "等待设备连接后读取定时任务"
				nextStatus = "正在连接"
				c.After(deadline.Sub(c.Now()))
			}
			if closeInspector != nil {
				mobilePageBody(c, func() {
					heading(c, seed.Name, scope)
					a.drawScheduleFacts(c, seed, zoneLabel(seed), nextStatus)
					mobileGroupHeading(c, "运行历史")
					if problem == "" {
						mobileMessage(c, caption)
					} else {
						mobileMessage(c, problem)
					}
					switch {
					case upgrade:
						action(c, "更新执行服务", func() { a.openWorker(w) }).AlignSelf(ui.Start)
					case retry != nil:
						action(c, "重试", retry).AlignSelf(ui.Start)
					}
				})
				return
			}
			ui.Column(c.Key("schedule-loading")).Fill().Children(func() {
				mobilePageContent(c, 0).Shrink(0).Children(func() {
					heading(c, seed.Name, scope)
				})
				if upgrade {
					ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Children(func() {
						label(c, problem, captionTextSize, palette(c).secondary).Role(ui.RoleStatus)
						action(c, "更新执行服务", func() { a.openWorker(w) })
					})
					return
				}
				mobileSessionLoading(c.Key(seed.ID), caption, problem, retry)
			})
			return
		}
		task := detail.Schedule
		runText := "立即运行"
		if runID != "" {
			runText = "重试同一次运行"
		}
		content := func() {
			if closeInspector != nil {
				heading(c, task.Name, scope)
				toggle, symbol := "暂停", schedulePauseSymbol
				if !task.Enabled {
					toggle, symbol = "恢复", schedulePlaySymbol
				}
				ui.Row(c).Gap(4).AlignItems(ui.Center).Wrap().Children(func() {
					iconAction(c, runText, runText, schedulePlaySymbol, 12, nil, 0, captionTextSize, 500, runNow).
						Disabled(!canEdit())
					iconAction(c, toggle, toggle, symbol, 12, nil, 0, captionTextSize, 500, func() { setEnabled(!task.Enabled) }).
						Disabled(!canEdit())
					ui.Spacer(c)
					iconButton(c, "编辑任务", pencilSymbol, 14, view.edit).Disabled(!canEdit())
					iconButton(c, "删除任务", scheduleTrashSymbol, 14, view.remove).Disabled(!canAct())
				})
			} else {
				mobilePageContext(c, task.Name, a.workspace(task.WorkspaceID).Name, a.deviceName())
			}
			zone := zoneLabel(task)
			if closeInspector != nil {
				nextStatus := ""
				if !a.preview && !a.scheduleConnected(task) {
					nextStatus = "设备离线"
				}
				a.drawScheduleFacts(c, task, zone, nextStatus)
			} else {
				mobileRow(c, scheduleSummary(task), zone, "", clockSymbol, nil)
				if task.Enabled && !a.workspace(task.WorkspaceID).Archived && !task.NextAt.IsZero() {
					mobileRow(c, "下次运行", "", mobileScheduleDate(task.NextAt, task.Zone), nil, nil)
				}
				enabled := task.Enabled
				mobileRowContent(c, ui.Row(c).MinHeight(max(formRowHeight, mobileTapSize(c))), "启用任务", "", nil, false, func() {
					mobileSwitch(c, "启用任务", &enabled, func() { setEnabled(enabled) }).Disabled(!canEdit())
				})
			}
			mobileGroupHeading(c, "运行历史")
			for _, run := range detail.Runs {
				source := "定时运行"
				if run.Manual {
					source = "手动运行"
				}
				title := mobileScheduleDate(run.At, task.Zone) + " · " + source
				status := scheduleRunLabel(run.State)
				if link := scheduleSessionStatus(run); link != "" {
					status += " · " + link
				}
				mobileDetailRow(c.Key(run.ID), title, status, title, clockSymbol,
					func() { a.openScheduleReceipt(w, task, run) }, nil).Value(status)
			}
			if loaded && len(detail.Runs) == 0 {
				mobileMessage(c, "暂无运行记录")
			}
			a.scheduleReadNotice(c, seed, &read, loaded)
			req.status(c)
			if req.err != "" || read.request.err != "" {
				action(c, "刷新记录", func() {
					req.err = ""
					reload()
				}).Disabled(req.busy || read.request.busy || !a.scheduleConnected(seed))
			}
		}
		if closeInspector != nil {
			mobilePageBody(c, content)
		} else {
			mobilePageWithFooter(c, content, func() { primaryAction(c, runText, runNow).Disabled(!canEdit()) })
		}
	}
	view.edit = func() {
		if canEdit() {
			a.openScheduleEditor(w, detail.Schedule, func() { reload(); changed() })
		}
	}
	view.remove = func() {
		if !canAct() {
			return
		}
		task, epoch := detail.Schedule, a.model.ConnectionEpoch
		confirming = true
		go func() {
			result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: w, Type: mygo.MessageWarning,
				Message: "删除定时任务？", Detail: "「" + task.Name + "」将停止触发。已创建的会话保留在工作区。",
				Buttons: []string{"取消", "删除任务"}, DestructiveButtons: []int{1}})
			mygo.RunOnMain(func() {
				confirming = false
				if !a.pageCurrent(w) {
					return
				}
				defer w.Invalidate()
				if err != nil || result.Button != 1 {
					return
				}
				if !canAct() || a.model.ConnectionEpoch != epoch || detail.Schedule.Revision != task.Revision {
					req.err = "任务或连接已改变，请刷新后再试"
					return
				}
				finish := func() {
					delete(a.model.Drafts, runKey)
					a.save()
					if closeInspector != nil {
						closeInspector()
					} else {
						w.Pop()
					}
					if removed != nil {
						removed()
					}
				}
				if a.preview {
					finish()
					return
				}
				a.pageJob(w, &req, "schedule.delete", protocol.ScheduleCommand{ID: task.ID, Revision: task.Revision},
					func(_ json.RawMessage) error { finish(); return nil })
			})
		}()
	}
	return view
}

func validScheduleSave(input, saved domain.Schedule) bool {
	return sameSchedule(input, saved) && saved.Revision == input.Revision+1
}

func (a *mobileApp) drawScheduleFacts(c *ui.Context, task domain.Schedule, zone, nextStatus string) {
	p := palette(c)
	fact := func(title, value, note string) {
		row := c.Key(title)
		ui.Row(row).FillWidth().Gap(8).MinHeight(28).PaddingY(3).AlignItems(ui.Start).Children(func() {
			oneLine(row, title, captionTextSize, p.secondary).Width(57).Shrink(0)
			ui.Column(row).MinWidth(0).Grow(1).Gap(2).Children(func() {
				label(row, value, captionTextSize, p.ink)
				if note != "" {
					label(row, note, captionTextSize, p.secondary)
				}
			})
		})
	}
	next := "无下一次"
	switch {
	case nextStatus != "":
		next = nextStatus
		if nextStatus == "设备离线" && !task.NextAt.IsZero() {
			zone = "原定 " + mobileScheduleDate(task.NextAt, task.Zone)
		}
	case a.workspace(task.WorkspaceID).Archived:
		next = "工作区已归档"
	case !task.Enabled:
		next, zone = "已暂停", ""
	case !task.NextAt.IsZero():
		next = mobileScheduleDate(task.NextAt, task.Zone)
	}
	fact("下一次", next, zone)
	rule := mobileScheduleCadence(task.Rule)
	if task.Rule.Kind != "interval" && task.Rule.Time != "" {
		rule += " · " + task.Rule.Time
	}
	fact("重复规则", rule, "")

	configuration := []string{}
	for _, harness := range a.model.Readiness.Harnesses {
		if harness.ID == task.HarnessID && harness.Name != "" {
			configuration = append(configuration, harness.Name)
			break
		}
	}
	if task.Config.Model != "" {
		configuration = append(configuration, task.Config.Model)
	}
	if task.Config.Permission != "" {
		permission := task.Config.Permission
		for _, choice := range a.model.Readiness.PermissionChoices {
			if choice.ID == permission && choice.Name != "" {
				permission = choice.Name
				break
			}
		}
		configuration = append(configuration, permission)
	}
	if len(configuration) != 0 {
		fact("运行配置", strings.Join(configuration, " · "), "")
	}
	fact("会话", "每次新开会话", "")
}

func mobileScheduleLocation(zone string) *time.Location {
	if zone == "" || zone == "Local" {
		return nil
	}
	location, _ := time.LoadLocation(zone)
	return location
}

func mobileScheduleDate(at time.Time, zone string) string {
	const dateFormat = "1月2日 15:04"
	if location := mobileScheduleLocation(zone); location != nil {
		return at.In(location).Format(dateFormat)
	}
	return at.UTC().Format(dateFormat) + " UTC"
}

func scheduleSummary(task domain.Schedule) string {
	switch task.Rule.Kind {
	case "interval":
		return fmt.Sprintf("每 %d 分钟", task.Rule.Minutes)
	case "once":
		return strings.TrimSpace(task.Rule.Date + " " + task.Rule.Time)
	case "weekly":
		return "每周 " + task.Rule.Time
	default:
		return "每天 " + task.Rule.Time
	}
}

func scheduleRunLabel(state domain.ScheduleRunState) string {
	names := map[domain.ScheduleRunState]string{
		domain.ScheduleQueued: "已排队", domain.ScheduleStarting: "正在启动", domain.ScheduleRunning: "正在运行",
		domain.ScheduleApproval: "等待审批", domain.ScheduleCompleted: "已完成", domain.ScheduleFailed: "失败",
		domain.ScheduleInterrupted: "已中断", domain.ScheduleMissed: "已错过", domain.ScheduleUnknown: "待核对",
	}
	return names[state]
}

func scheduleSessionStatus(run domain.ScheduleRun) string {
	switch run.SessionLink {
	case "archived":
		return "会话已归档"
	case "deleted":
		return "会话已删除"
	default:
		return ""
	}
}

func (a *mobileApp) scheduleReadNotice(c *ui.Context, task domain.Schedule, read *scheduleObservation, loaded bool) {
	if a.preview {
		return
	}
	switch {
	case !a.scheduleConnected(task):
		message := "连接执行设备后读取运行记录"
		if loaded {
			message = "等待执行设备连接 · 显示上次读取的记录"
		}
		mobileMessage(c, message)
	case read.request.err != "":
		read.request.status(c)
	case !loaded:
		mobileMessage(c, "正在读取运行记录…")
	}
}

func (a *mobileApp) openScheduleReceipt(parent *mygo.Window, task domain.Schedule, run domain.ScheduleRun) {
	var w *mygo.Window
	read := scheduleObservation{}
	receiptID := run.ID
	w = a.page("运行回执", func(c *ui.Context) {
		a.observeSchedule(c, w, &read, task, func(in domain.ScheduleDetail) error {
			index := slices.IndexFunc(in.Runs, func(candidate domain.ScheduleRun) bool { return candidate.ID == receiptID })
			if index < 0 {
				return fmt.Errorf("此次运行已不在最近的记录中")
			}
			task, run = in.Schedule, in.Runs[index]
			return nil
		})
		mobilePageBody(c, func() {
			mobilePageContext(c, task.Name, a.workspace(task.WorkspaceID).Name, a.deviceName())
			duration := ""
			if !run.StartedAt.IsZero() && !run.FinishedAt.Before(run.StartedAt) {
				duration = "用时 " + mobileElapsed(run.FinishedAt.Sub(run.StartedAt).Milliseconds())
			}
			source := "定时运行"
			if run.Manual {
				source = "手动运行"
			}
			mobileRow(c, scheduleRunLabel(run.State), mobileScheduleDate(run.At, task.Zone)+" · "+source, duration, infoSymbol, nil)
			if run.Error != "" {
				mobileMessage(c, run.Error)
			}
			if status := scheduleSessionStatus(run); status != "" {
				mobileMessage(c, status)
			}
			session := a.sessionByID(run.SessionID)
			if run.SessionLink != "deleted" && (read.request.err == "" || !a.scheduleConnected(task)) &&
				session.ID != "" && session.WorkspaceID == task.WorkspaceID &&
				(session.Context.DeviceID == "" || session.Context.DeviceID == task.DeviceID) {
				mobileRow(c, "打开对应会话", session.Title, "", conversationsSymbol, func() {
					if a.pageCurrent(w) && run.SessionLink != "deleted" && run.SessionID == session.ID {
						a.openSessionFrom(w, session.ID)
					}
				})
			}
			a.scheduleReadNotice(c, task, &read, true)
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) openScheduleEditor(parent *mygo.Window, task domain.Schedule, changed func()) {
	newTask := task.ID == ""
	initial := task
	task.Rule.Days = slices.Clone(task.Rule.Days)
	if newTask {
		task.ID = domain.NewID()
		task.DeviceID = a.model.Snapshot.DeviceID
		if task.WorkspaceID == "" {
			task.WorkspaceID = a.model.WorkspaceID
		}
		if workspace := a.workspace(task.WorkspaceID); workspace.ID == "" || workspace.Archived || workspace.DeviceID != task.DeviceID {
			task.WorkspaceID = ""
			for _, workspace := range a.model.Snapshot.Workspaces {
				if !workspace.Archived && workspace.DeviceID == task.DeviceID {
					task.WorkspaceID = workspace.ID
					break
				}
			}
		}
		a.scheduleRunDefaults(&task)
		task.Rule = domain.ScheduleRule{Kind: "daily", Time: "09:00", Minutes: 60}
		task.Enabled = true
	}
	interval := task.Rule.Minutes
	if interval == 0 {
		interval = 60
	}
	hours := interval%60 == 0
	if hours {
		interval /= 60
	}
	amount := strconv.Itoa(interval)
	rule := func() domain.ScheduleRule {
		value := scheduleRuleFields(task.Rule)
		if value.Kind == "interval" {
			value.Minutes, _ = strconv.Atoi(amount)
			if hours {
				if value.Minutes > 720 || value.Minutes < 1 {
					value.Minutes = 0
				}
				value.Minutes *= 60
			}
		}
		return value
	}
	req, zoneReq := mobileRequest{}, mobileRequest{}
	var w *mygo.Window
	loadZone := func() {
		if a.preview {
			task.Zone = "Asia/Taipei"
			w.Invalidate()
			return
		}
		a.pageJob(w, &zoneReq, "schedule.zone", nil, decodePage(func(zone string) error {
			task.Zone = zone
			return nil
		}))
	}
	save := func() {
		if !a.pageCurrent(w) || req.busy {
			return
		}
		in := task
		in.Name, in.Text = strings.TrimSpace(task.Name), strings.TrimSpace(task.Text)
		in.Rule = rule()
		workspace := a.workspace(in.WorkspaceID)
		if in.Name == "" || in.Text == "" || in.WorkspaceID == "" || !in.Rule.Valid() || in.Config.Model == "" {
			req.err = "请填写任务名称、工作区、有效时间和提示词，并选择模型"
			w.Invalidate()
			return
		}
		if workspace.Archived || workspace.DeviceID != in.DeviceID || in.DeviceID != a.model.Snapshot.DeviceID {
			req.err = "请选择当前执行设备上的未归档工作区"
			w.Invalidate()
			return
		}
		a.pageJob(w, &req, "schedule.save", in, decodePage(func(saved domain.Schedule) error {
			if !validScheduleSave(in, saved) {
				return fmt.Errorf("设备返回了无效的保存回执")
			}
			changed()
			w.Pop()
			return nil
		}))
	}
	title := "编辑定时任务"
	if newTask {
		title = "新建定时任务"
	}
	w = a.page(title, func(c *ui.Context) {
		mobilePageBody(c, func() {
			mobileSection(c, "任务")
			formField(c, "名称", "", &task.Name)
			if newTask {
				choiceName := a.workspace(task.WorkspaceID).Name
				if choiceName == "" {
					choiceName = "选择工作区"
				}
				formChoice(c, "工作区", choiceName+" · "+a.deviceName(), func(m *ui.Menu) {
					for _, ws := range a.model.Snapshot.Workspaces {
						if !ws.Archived && ws.DeviceID == task.DeviceID {
							menuChoice(m, ws.Name, task.WorkspaceID == ws.ID, func() {
								if task.WorkspaceID != ws.ID {
									task.WorkspaceID, task.HarnessID, task.Git = ws.ID, "", domain.GitChoice{}
									a.scheduleRunDefaults(&task)
									a.loadGit(ws.ID)
								}
							})
						}
					}
				})
			} else {
				formRow(c, "工作区", func() {
					oneLine(c, a.workspace(task.WorkspaceID).Name+" · "+a.deviceName(), 15, palette(c).secondary).MaxWidthPercent(62)
				})
			}
			label(c, "任务指令", 13, palette(c).secondary).Padding(8, 0, 6)
			mobileTextArea(c, &task.Text, "提示词").Height(82).Padding(9, 12).
				FontSize(controlTextSize * c.Preferences().TextScale).Radius(10)
			mobileGroupHeading(c, "运行时间")
			kinds := []struct{ id, label string }{{"once", "单次"}, {"daily", "每天"}, {"weekly", "每周"}, {"interval", "固定间隔"}}
			for _, kind := range kinds {
				if kind.id == task.Rule.Kind {
					formChoice(c, "频率", kind.label, func(m *ui.Menu) {
						for _, choice := range kinds {
							menuChoice(m, choice.label, choice.id == task.Rule.Kind, func() { task.Rule.Kind = choice.id })
						}
					})
				}
			}
			if task.Rule.Kind == "interval" {
				formField(c, "间隔", "", &amount)
				unit := "分钟"
				if hours {
					unit = "小时"
				}
				formChoice(c, "间隔单位", unit, func(m *ui.Menu) {
					menuChoice(m, "分钟", !hours, func() { hours = false })
					menuChoice(m, "小时", hours, func() { hours = true })
				})
			} else {
				formField(c, "时间", "09:00", &task.Rule.Time)
				if task.Rule.Kind == "once" {
					formField(c, "日期", "2026-11-30", &task.Rule.Date)
				}
				if task.Rule.Kind == "weekly" {
					ui.Row(c).Wrap().Children(func() {
						for day, name := range []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"} {
							pressed := false
							for _, selected := range task.Rule.Days {
								pressed = pressed || time.Weekday(day) == selected
							}
							action(c.Key(day), name, func() {
								if pressed {
									for i, selected := range task.Rule.Days {
										if selected == time.Weekday(day) {
											task.Rule.Days = append(task.Rule.Days[:i], task.Rule.Days[i+1:]...)
											break
										}
									}
								} else {
									task.Rule.Days = append(task.Rule.Days, time.Weekday(day))
								}
							}).AccessSelected(pressed).Background(palette(c).selected.Alpha(map[bool]float32{true: 1, false: 0}[pressed]))
						}
					})
				}
			}
			if location := mobileScheduleLocation(task.Zone); location != nil {
				mobileRow(c, "执行设备时区", "", location.String(), nil, nil)
				if text := scheduleRunPreview(initial, rule(), c.Now(), location); text != "" {
					mobileRow(c, "运行预览", text, "", nil, nil)
				}
			} else if task.Zone != "" {
				mobileMessage(c, "执行设备未报告可用的时区名称")
			} else {
				zoneReq.status(c)
				if zoneReq.err != "" {
					action(c, "重试读取时区", loadZone).AlignSelf(ui.Start)
				}
			}
			mobileGroupHeading(c, "执行配置")
			branch := task.Git.Branch
			if branch == "" {
				branch = "工作区默认分支"
			}
			git := a.model.GitStatuses["workspace:"+task.WorkspaceID]
			if git.Applicable || task.Git.Branch != "" || task.Git.NewWorktree {
				formChoice(c, "分支", branch, func(menu *ui.Menu) {
					menuChoice(menu, "工作区默认分支", task.Git.Branch == "", func() { task.Git.Branch = "" })
					for _, choice := range git.Branches {
						menuChoice(menu, choice, task.Git.Branch == choice, func() { task.Git.Branch = choice })
					}
				})
				mobileToggle(c.Key("schedule-worktree"), "新建 worktree", &task.Git.NewWorktree, nil)
			}
			ready := a.model.SessionReadiness(domain.Session{WorkspaceID: task.WorkspaceID, HarnessID: task.HarnessID})
			if len(ready.Harnesses) > 1 {
				formChoice(c, "Agent", ready.Descriptor().Name, func(menu *ui.Menu) {
					for _, harness := range ready.Harnesses {
						if harness.State == domain.RuntimeReady {
							menuChoice(menu, harness.Name, task.HarnessID == harness.ID, func() {
								if task.HarnessID != harness.ID {
									task.HarnessID = harness.ID
									a.scheduleRunDefaults(&task)
								}
							})
						}
					}
				})
			}
			modelName := task.Config.Model
			var efforts []string
			for _, model := range ready.Models {
				if model.ID == task.Config.Model {
					modelName = model.Name
					efforts = model.Efforts
				}
			}
			formChoice(c, "模型", modelName, func(menu *ui.Menu) {
				for _, model := range ready.Models {
					menuChoice(menu, model.Name, task.Config.Model == model.ID, func() {
						task.Config.Model, task.Config.Effort, task.Config.Fast = model.ID, model.DefaultEffort, false
					})
				}
			})
			if len(efforts) > 0 {
				formChoice(c, "推理强度", task.Config.Effort, func(menu *ui.Menu) {
					for _, effort := range efforts {
						menuChoice(menu, effort, task.Config.Effort == effort, func() { task.Config.Effort = effort })
					}
				})
			}
			permission := task.Config.Permission
			for _, choice := range ready.PermissionChoices {
				if choice.ID == permission {
					permission = choice.Name
					break
				}
			}
			formChoice(c, "权限", permission, func(menu *ui.Menu) {
				for _, choice := range ready.PermissionChoices {
					menuChoice(menu, choice.Name, task.Config.Permission == choice.ID, func() { task.Config.Permission = choice.ID })
				}
			})
			req.status(c)

		})
	})
	loadZone()
	a.loadGit(task.WorkspaceID)
	a.pushWithCommit(parent, w, "保存任务", save)
}

func scheduleRuleFields(rule domain.ScheduleRule) domain.ScheduleRule {
	switch rule.Kind {
	case "interval":
		return domain.ScheduleRule{Kind: rule.Kind, Minutes: rule.Minutes}
	case "once":
		return domain.ScheduleRule{Kind: rule.Kind, Date: rule.Date, Time: rule.Time}
	case "weekly":
		days := slices.Clone(rule.Days)
		slices.Sort(days)
		return domain.ScheduleRule{Kind: rule.Kind, Time: rule.Time, Days: days}
	default:
		return domain.ScheduleRule{Kind: rule.Kind, Time: rule.Time}
	}
}

func scheduleRunPreview(task domain.Schedule, rule domain.ScheduleRule, now time.Time, location *time.Location) string {
	next := rule.Next(now, location)
	if task.Enabled && task.Revision != 0 && scheduleRuleFields(task.Rule).Equal(rule) && (task.Zone == location.String() || rule.Kind == "interval") {
		next = task.NextAt
	}
	if next.IsZero() {
		return ""
	}
	const format = "1月2日 15:04"
	text := next.In(location).Format(format)
	if following := rule.Next(next, location); !following.IsZero() {
		text += "、" + following.In(location).Format(format)
	}
	return text
}

func (a *mobileApp) scheduleRunDefaults(task *domain.Schedule) {
	ready := a.model.SessionReadiness(domain.Session{WorkspaceID: task.WorkspaceID, HarnessID: task.HarnessID})
	task.HarnessID = ready.HarnessID
	task.Config = domain.RunConfig{Permission: "default"}
	for _, model := range ready.Models {
		if task.Config.Model == "" || model.Default {
			task.Config.Model, task.Config.Effort = model.ID, model.DefaultEffort
		}
		if model.Default {
			break
		}
	}
	if len(ready.Permissions) > 0 && !slices.Contains(ready.Permissions, task.Config.Permission) {
		task.Config.Permission = ready.Permissions[0]
	}
}

func (a *mobileApp) previewSchedule() domain.ScheduleDetail {
	at := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	task := domain.Schedule{ID: "verify-reconnect", DeviceID: a.model.Snapshot.DeviceID,
		WorkspaceID: "ios", Name: "每日构建检查", Text: "运行构建检查。失败时总结原因与相关文件。", HarnessID: "codex",
		Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"}, Zone: "Asia/Taipei", Enabled: true, Revision: 1,
		Git: domain.GitChoice{Branch: "main"}, Config: domain.RunConfig{Model: "gpt-5.4", Effort: "medium", Permission: "default"}}
	task.NextAt = task.Rule.Next(time.Now(), mobileScheduleLocation(task.Zone))
	runs := []domain.ScheduleRun{
		{ID: "first-run", ScheduleID: task.ID, At: at, StartedAt: at, FinishedAt: at.Add(42 * time.Second), State: domain.ScheduleCompleted, SessionID: "review"},
		{ID: "previous-run", ScheduleID: task.ID, At: at.AddDate(0, 0, -1), State: domain.ScheduleCompleted},
	}
	task.Latest = &runs[0]
	return domain.ScheduleDetail{Schedule: task, Runs: runs}
}
