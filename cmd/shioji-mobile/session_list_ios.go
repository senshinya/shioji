package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type mobileSessionGroup struct {
	key, title, device string
	workspace          mobileclient.WorkspaceLocation
	rows               []mobileSessionRow
}

type sessionWorkspaceChoice struct {
	location      mobileclient.WorkspaceLocation
	name, address string
}

func sessionWorkspaceChoices(catalogs []mobileclient.DeviceCatalog) []sessionWorkspaceChoice {
	var choices []sessionWorkspaceChoice
	for _, catalog := range catalogs {
		for _, workspace := range catalog.Snapshot.Workspaces {
			if workspace.Archived || workspace.ID == "" || workspace.DeviceID != catalog.Snapshot.DeviceID {
				continue
			}
			choices = append(choices, sessionWorkspaceChoice{
				location: mobileclient.WorkspaceLocation{DeviceID: workspace.DeviceID, WorkspaceID: workspace.ID},
				name:     workspace.Name, address: catalog.Label(),
			})
		}
	}
	return choices
}

func (a *mobileApp) sessionList(c *ui.Context) {
	p := palette(c)
	catalogs := a.browseCatalogs()
	workspaces := a.orderedWorkspaces(catalogs)
	global := a.filter == (mobileclient.WorkspaceLocation{})
	now := c.Now().In(time.Local)
	if !a.searching && !a.archive && a.groupingForScope(global) == groupByTime {
		c.After(mobileclient.StartOfDay(now).AddDate(0, 0, 1).Sub(now))
	}
	if !a.searching {
		a.sessionListToolbar(c, workspaces, global)
	}
	status := a.sessionListNotice
	if status == "" {
		if a.workspaceArchiveRequest.busy {
			status = "正在归档工作区…"
		} else {
			status = a.workspaceArchiveRequest.err
		}
	}
	if status == "" && a.endpoint.Address != "" {
		status = mobileConnectionLabel(a.connectionPhase())
	}
	if status != "" {
		label(c, status, captionTextSize, p.secondary).Role(ui.RoleStatus).Padding(4, 16, 8)
	}
	menu := contextMenuFrame{zoom: c.Zoom()}
	ui.Scroll(c).Grow(1).Basis(0).MinHeight(0).Padding(2, 16, 28).Children(func() {
		groups, count := a.sessionListRows(catalogs, workspaces, global, now)
		for _, group := range groups {
			if len(group.rows) == 0 {
				continue
			}
			if group.key == "" {
				for _, row := range group.rows {
					a.sessionRow(c, &menu, row)
				}
			} else {
				a.sessionGroup(c, &menu, group)
			}
		}
		if count == 0 {
			message := "暂无会话"
			if a.searching && strings.TrimSpace(a.search) != "" {
				message = "没有找到会话"
			} else if a.endpoint.Address == "" && !a.preview {
				message = "在「设备」中添加执行设备"
			} else if a.archive {
				message = "暂无归档"
			}
			label(c, message, 15, p.secondary).Margin(20, 0)
		}
	}).DrawOver(func(_ *ui.Painter, viewport ui.Rect) { a.sessionListMenu.publish(a, a.home, menu, viewport) })
}

func (a *mobileApp) sessionListToolbar(c *ui.Context, workspaces []sessionWorkspaceChoice, global bool) {
	title, scopeIcon := "全局视图", layersSymbol
	if !global {
		title, scopeIcon = "工作区", folderSymbol
		for _, workspace := range workspaces {
			if workspace.location == a.filter {
				title = workspace.name
				break
			}
		}
	}
	if a.archive {
		title = "归档"
	}
	ui.Row(c).MinHeight(55).Padding(2, 16, 4).Gap(8).AlignItems(ui.Center).Children(func() {
		floatingSurface(c, iconAction(c, title, "切换会话范围", scopeIcon, inlineIconSize, pickerSymbol, pickerIconSize, 15, 650, nil).
			Shrink(1), 22).
			PopoverMenuButton(func(m *ui.Menu) {
				menuChoice(m, "全局视图", global && !a.archive, func() { a.selectListScope(mobileclient.WorkspaceLocation{}) })
				for _, workspace := range workspaces {
					label := strings.Trim(workspace.name+" · "+workspace.address, " ·")
					menuChoice(m, label, workspace.location == a.filter && !a.archive, func() { a.selectListScope(workspace.location) })
				}
			})
		ui.Spacer(c).MinWidth(0)
		toolbarButton(c, "搜索会话", searchSymbol, func() { a.toggleSessionSearch(true) })
		toolbarButton(c, "会话视图", filterSymbol, nil).PopoverMenuButton(func(m *ui.Menu) {
			selected := a.groupingForScope(global)
			choose := func(title string, mode sessionGrouping) {
				menuChoice(m, title, !a.archive && selected == mode, func() {
					a.archive = false
					a.setSessionGrouping(mode, global)
				})
			}
			choose("按状态分组", groupByStatus)
			choose("按时间分组", groupByTime)
			if global {
				choose("按工作区分组", groupByWorkspace)
			} else {
				choose("按手动顺序", groupByManual)
			}
			m.Separator()
			if global {
				if m.Item("排列工作区").Disabled(len(workspaces) < 2).Chosen() {
					a.arrangeWorkspaces()
				}
				if m.Item("排列置顶会话").Disabled(len(a.pinnedRows()) < 2).Chosen() {
					a.arrangePins()
				}
			} else if m.Item("排列会话").Disabled(len(a.arrangeableSessions(a.filter)) < 2).Chosen() {
				a.arrangeSessions(a.filter)
			}
			if !global && !a.archive {
				if commands := a.workspaceListCommands(a.filter); len(commands) > 0 {
					m.Separator()
					for _, command := range commands {
						if m.Item(command.Title).Disabled(command.Disabled).Chosen() {
							command.run()
						}
					}
				}
			}
			m.Separator()
			if m.Item("定时任务").Chosen() {
				a.openSchedules(a.home, "")
			}
			menuChoice(m, "归档", a.archive, func() { a.openArchive(a.home) })
			menuChoice(m, "全文搜索", false, func() { a.openContentSearch(a.home, "") })
			menuChoice(m, "待我处理", false, func() { a.openAttention(a.home) })
			if m.Item("最近操作").Chosen() {
				a.openRecentOperations(a.home)
			}
		})
	})
}

func (a *mobileApp) sessionListRows(catalogs []mobileclient.DeviceCatalog, workspaces []sessionWorkspaceChoice, global bool, now time.Time) ([]mobileSessionGroup, int) {
	flat := a.searching || a.archive
	mode := a.groupingForScope(global)
	byWorkspace, byTime := !flat && mode == groupByWorkspace, !flat && mode == groupByTime
	byManual := !flat && mode == groupByManual
	var query string
	if a.searching {
		query = strings.ToLower(strings.TrimSpace(a.search))
	}
	groups := []mobileSessionGroup{{}}
	workspaceGroups := make(map[mobileclient.WorkspaceLocation]int)
	if !flat {
		groups[0] = mobileSessionGroup{key: "pinned", title: "置顶", rows: a.pinnedRows()}
		switch mode {
		case groupByWorkspace:
			for _, workspace := range workspaces {
				workspaceGroups[workspace.location] = len(groups)
				groups = append(groups, mobileSessionGroup{key: "workspace:" + workspace.location.DeviceID + ":" + workspace.location.WorkspaceID,
					title: workspace.name, device: workspace.address, workspace: workspace.location})
			}
		case groupByTime:
			groups = append(groups, mobileSessionGroup{key: "time:today", title: "今天"},
				mobileSessionGroup{key: "time:yesterday", title: "昨天"}, mobileSessionGroup{key: "time:earlier", title: "更早"},
				mobileSessionGroup{key: "time:unknown", title: "时间未知"})
		case groupByManual:
			groups = append(groups, mobileSessionGroup{})
		default:
			groups = append(groups, mobileSessionGroup{key: "attention", title: "待我处理"},
				mobileSessionGroup{key: "running", title: "进行中"}, mobileSessionGroup{key: "complete", title: "刚完成"},
				mobileSessionGroup{key: "recent", title: "最近"})
		}
	}
	count := len(groups[0].rows)
	for _, catalog := range catalogs {
		id := catalog.Snapshot.DeviceID
		remote := id != a.model.Snapshot.DeviceID
		for _, entry := range mobileclient.BrowseSessionCatalog(catalog.Snapshot, a.filter, a.archive) {
			session, workspace := entry.Session, entry.Workspace
			if query != "" && !strings.Contains(strings.ToLower(session.Title+" "+workspace.Name+" "+session.GitBranch+" "+catalog.Label()), query) {
				continue
			}
			row := mobileSessionRow{session: session, workspace: workspace, requests: entry.Pending, inGroup: byWorkspace,
				cached: remote && catalog.RequestedAt.IsZero() && !a.preview}
			if len(catalogs) > 1 {
				row.address = catalog.Label()
			}
			if flat {
				groups[0].rows = append(groups[0].rows, row)
				count++
				continue
			}
			if a.isSessionPinned(app.SessionLocation{SessionID: session.ID, WorkspaceID: workspace.ID, DeviceID: id}) {
				continue
			}
			var group int
			switch {
			case byWorkspace:
				group = workspaceGroups[mobileclient.WorkspaceLocation{DeviceID: id, WorkspaceID: workspace.ID}]
			case byManual:
				group = 1
			case byTime:
				row.inputAt = catalog.Snapshot.SessionInputTimes[session.ID]
				day := mobileclient.InputDay(row.inputAt, now)
				group = 1 + int(day)
				if day != mobileclient.SessionInputUnknown {
					stamp := row.inputAt.In(now.Location())
					format := "15:04"
					if day == mobileclient.SessionInputEarlier {
						format = "1月2日"
						if stamp.Year() != now.Year() {
							format = "2006年1月2日"
						}
					}
					row.dateLabel = stamp.Format(format)
				}
			case entry.Pending > 0:
				group = 1
			case session.Status.Active():
				group = 2
			case session.Status == domain.ExecutionCompleted:
				group = 3
			default:
				group = 4
			}
			groups[group].rows = append(groups[group].rows, row)
			count++
		}
	}
	if byTime {
		for i := 1; i < len(groups)-1; i++ {
			slices.SortStableFunc(groups[i].rows, func(a, b mobileSessionRow) int { return b.inputAt.Compare(a.inputAt) })
		}
	} else if byWorkspace || byManual {
		for _, group := range groups[1:] {
			if len(group.rows) < 2 {
				continue
			}
			workspace := group.rows[0].workspace
			scope := mobileclient.WorkspaceLocation{DeviceID: workspace.DeviceID, WorkspaceID: workspace.ID}
			ranks := navigationRanks(a.navigationOrder.SessionIDs(scope))
			slices.SortStableFunc(group.rows, func(a, b mobileSessionRow) int {
				return navigationRank(ranks, a.session.ID) - navigationRank(ranks, b.session.ID)
			})
		}
	}
	return groups, count
}

func (a *mobileApp) sessionGroup(c *ui.Context, menu *contextMenuFrame, group mobileSessionGroup) {
	p := palette(c)
	bg := p.canvas
	if a.wide {
		bg = p.panel
	}
	section := c.Key(group.key)
	collapsed := ui.Local(c.Root(), "session-group:"+group.key, func() bool { return false })
	ui.Column(section).Margin(0, 0, 5).Children(func() {
		pending := 0
		if *collapsed {
			for _, row := range group.rows {
				pending += row.requests
			}
		}
		value := group.device
		if pending > 0 {
			value = pendingSessionDescription(value, pending)
		}
		header := ui.ButtonBase(section).Sticky().Top(0).MinHeight(mobileTapSize(c)).Gap(8).AlignItems(ui.Center).Background(bg).
			Label(group.title).Value(value).Role(ui.RoleButton).OnClick(func() { *collapsed = !*collapsed }).Children(func() {
			oneLine(section, group.title, 13, p.secondary).FontWeight(640).Grow(1).MinWidth(0).PassThrough()
			if group.device != "" {
				oneLine(section, group.device, 11.5, p.secondary).MaxWidth(130).PassThrough()
			}
			if pending != 0 {
				oneLine(section, fmt.Sprint(pending), 12, p.warning).PassThrough()
			}
			arrow := downSymbol
			if *collapsed {
				arrow = rightSymbol
			}
			ui.Icon(section, arrow).Size(12, 12).Shrink(0).TextColor(p.secondary).PassThrough()
		})
		if group.workspace.WorkspaceID != "" {
			menu.add(header.Description("长按显示工作区操作"), group.key, contextMenuTarget{
				commands: a.workspaceListCommands(group.workspace),
			})
		}
		if !*collapsed {
			for _, row := range group.rows {
				a.sessionRow(section, menu, row)
			}
		}
	})
}

func (a *mobileApp) workspaceListCommands(scope mobileclient.WorkspaceLocation) []contextMenuCommand {
	snapshot := a.sessionCatalog(scope.DeviceID).Snapshot
	index := slices.IndexFunc(snapshot.Workspaces, func(workspace domain.Workspace) bool {
		return !workspace.Archived && scope.Includes(workspace)
	})
	if index < 0 {
		return nil
	}
	workspace := snapshot.Workspaces[index]
	open := func(action string) func() {
		return func() {
			a.openSessionListTarget(sessionListTarget{
				location: app.SessionLocation{DeviceID: scope.DeviceID, WorkspaceID: scope.WorkspaceID},
				action:   action,
			})
		}
	}
	return []contextMenuCommand{
		{ID: "workspace-compose", Title: "新建会话", Symbol: "square.and.pencil", run: open("")},
		{ID: workspaceSettingsAction, Title: "工作区设置", Symbol: "gearshape", run: open(workspaceSettingsAction)},
		{ID: workspaceImportAction, Title: "导入会话", Symbol: "square.and.arrow.down", run: open(workspaceImportAction)},
		{ID: workspaceArchiveAction, Title: "归档工作区", Symbol: "archivebox", run: open(workspaceArchiveAction),
			Disabled: a.workspaceArchiveRequest.busy || workspace.DeviceID == a.model.Snapshot.DeviceID && !a.canChangeWorkspaceArchive(workspace)},
	}
}

func (a *mobileApp) sessionRow(c *ui.Context, menu *contextMenuFrame, item mobileSessionRow) {
	p := palette(c)
	session, requests := item.session, item.requests
	ref := app.SessionLocation{DeviceID: item.workspace.DeviceID, WorkspaceID: item.workspace.ID, SessionID: session.ID}
	row := c.Key(ref.DeviceID + ":" + ref.WorkspaceID + ":" + ref.SessionID)
	pinned := item.pin.SessionID != ""
	current := ref.DeviceID == a.model.Snapshot.DeviceID
	selected := current && a.model.SessionID == session.ID && (a.wide || a.chat != nil)
	if a.wide {
		if review := a.currentSplitReview(); review.right {
			selected = ref.DeviceID == review.owner.model.Snapshot.DeviceID &&
				ref.WorkspaceID == review.state.session.WorkspaceID && ref.SessionID == review.state.session.ID
		}
	}
	bg := p.canvas.Alpha(0)
	if selected {
		bg = p.selected
	}
	status := p.secondary
	if requests > 0 {
		status = p.warning
	} else if session.Status.Active() || session.Status == domain.ExecutionCompleted {
		status = p.active
	}
	value := strings.Trim(item.metadata()+" · "+item.dateLabel, " ·")
	if session.ReadOnlyHistory() {
		value = strings.Trim(value+" · 只读会话", " ·")
	}
	if requests > 0 {
		value = pendingSessionDescription(value, requests)
	}
	target := sessionListTarget{location: ref, pinned: pinned, archived: session.Archived || item.workspace.Archived}
	open := func() { a.openSessionListTarget(target) }
	button := ui.ButtonBase(row).MinHeight(max(contentRowHeight, mobileTapSize(c))).Padding(4, 8).Margin(0, -8).AlignItems(ui.Center).Justify(ui.Center).Radius(10).
		Role(ui.RoleButton).Label(session.Title).Value(value).AccessSelected(selected).Background(bg).
		Description("长按预览会话").OnClick(open).Children(func() {
		ui.Column(row).WidthPercent(100).Gap(3).PassThrough().Children(func() {
			ui.Row(row).Gap(10).AlignItems(ui.Center).Children(func() {
				font := mobileStatusFont(row, 15)
				if session.ReadOnlyHistory() {
					alignedSymbol(row, readOnlySymbol, session.Title, font, 10, p.secondary)
				} else {
					ui.Box(row).Size(10, 10).Shrink(0).PassThrough().Draw(func(p *ui.Painter, r ui.Rect) {
						p.Fill(ui.Rect{X: r.X + 3, Y: r.Y + 3 + inkCenterOffset(session.Title, font), W: 4, H: 4}, mobileBreathingInk(p, status, current && a.model.Connected && session.Status.Active() && !c.Preferences().ReduceMotion), 2)
					})
				}
				oneLine(row.Key("session-title"), session.Title, 15, p.ink).Grow(1).
					ContentTransition(ui.ContentTransition{Animation: &mobileTitleMotion})
				if item.dateLabel != "" {
					oneLine(row, item.dateLabel, captionTextSize, p.secondary).Shrink(0)
				}
			})
			meta := item.metadata()
			if meta != "" {
				oneLine(row, meta, 11.5, p.secondary).Font("monospace").Margin(0, 0, 0, 20)
			}
		})
	})
	menu.add(button, ref.DeviceID+":"+ref.WorkspaceID+":"+ref.SessionID, contextMenuTarget{
		location: ref, session: session, open: open, commands: a.sessionRowCommands(target, item),
	})
}

func pendingSessionDescription(value string, count int) string {
	return strings.Trim(value+" · "+fmt.Sprintf("%d 项待处理", count), " ·")
}

func (a *mobileApp) sessionRowCommands(target sessionListTarget, row mobileSessionRow) []contextMenuCommand {
	ref := target.location
	var commands []contextMenuCommand
	if !target.archived {
		if a.isSessionPinned(ref) {
			commands = a.pinCommands(ref)
		} else {
			commands = append(commands, contextMenuCommand{ID: "pin", Title: "置顶会话", Symbol: "pin",
				run: func() { a.pinFeedback(a.setSessionPin(ref, true)) }})
		}
		if a.wide && (ref.DeviceID != a.model.Snapshot.DeviceID || a.model.SessionID != ref.SessionID) {
			commands = append(commands, contextMenuCommand{ID: "parallel", Title: "在右侧查看", Symbol: "rectangle.split.2x1",
				run: func() {
					if err := a.openParallelSession(ref); err != nil {
						a.sessionListNotice = err.Error()
						a.home.Invalidate()
					}
				}})
		}
	}
	commands = append(commands, contextMenuCommand{ID: "manage", Title: "会话操作", Symbol: "ellipsis.circle",
		run: func() {
			operation := target
			operation.action = "manage"
			a.openSessionListTarget(operation)
		}})
	if !row.workspace.Archived && (ref.DeviceID != a.model.Snapshot.DeviceID || a.model.Connected) {
		title, icon := "归档会话", "archivebox"
		if row.session.Archived {
			title, icon = "恢复会话", "arrow.uturn.backward"
		}
		commands = append(commands, contextMenuCommand{ID: "archive", Title: title, Symbol: icon,
			run: func() {
				operation := target
				operation.action = "archive"
				a.openSessionListTarget(operation)
			}})
	}
	return commands
}
