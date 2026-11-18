package ui

import (
	"encoding/json"
	"fmt"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"strings"

	n "github.com/egoist/mygo/ui"
)

const archiveRowHeight float32 = 40
const archiveActionWidth float32 = 60 // two 28-DIP targets and their 4-DIP gap
const archiveTitleTopInset = 16

var archiveDeleteIcon = svg("M4 5h12M8 2h4l1 3M5 5l1 13h8l1-13M8 8v7M12 8v7")

type archiveUI struct {
	open                       bool
	tab, scope, query, viewing string
	lists                      map[string]*n.ListState
	deletion                   *archiveDeleteUI
	queries                    map[string]string
	returnTo                   *archiveReturn
}

type archiveReturn struct {
	session, workspace, secondary, secondaryWorkspace string
	focusSecondary                                    bool
}

func (a *archiveUI) selectList(tab, scope string) {
	if a.queries == nil {
		a.queries = map[string]string{}
	}
	a.queries[a.tab+":"+a.scope] = a.query
	a.tab, a.scope = tab, scope
	a.query = a.queries[tab+":"+scope]
}

type archiveDeleteUI struct {
	connection string
	workspace  domain.Workspace
	session    domain.Session
	count      int
	open, busy bool
	error      string
	operation  string
	batch      bool
	workspaces bool
	targets    []archiveDeleteTarget
	failed     []archiveDeleteTarget
	next       int
}

type archiveDeleteTarget struct {
	connection, method, title string
	input                     protocol.ArchiveDeleteInput
}

func (v *View) archiveState() *archiveUI {
	root := v.splitRoot()
	if root.archive == nil {
		root.archive = &archiveUI{tab: "sessions", lists: map[string]*n.ListState{}}
	}
	return root.archive
}

func (v *View) openArchive() {
	root := v.splitRoot()
	root.closeSchedulePage()
	a := root.archiveState()
	if !a.open && a.viewing == "" {
		m := root.Model
		a.returnTo = &archiveReturn{m.SessionID, m.WorkspaceID, m.SecondarySessionID, m.SecondaryWorkspaceID, m.FocusSecondary}
	}
	a.open = true
	a.viewing = ""
	root.workspaceEditor = nil
	root.skillReturn = nil
	root.devices = false
	root.searchOpen = false
}

func (v *View) closeArchive() {
	root := v.splitRoot()
	a := root.archiveState()
	a.open, a.viewing = false, ""
	if source := a.returnTo; source != nil {
		m := root.Model
		if source.session != "" {
			if location, ok := m.NavigationSessionLocation(source.session); ok {
				source.workspace = location.WorkspaceID
			} else {
				source.session = ""
			}
		}
		if !slices.ContainsFunc(m.Snapshot.Workspaces, func(w domain.Workspace) bool { return w.ID == source.workspace }) {
			source.workspace = ""
		}
		if source.secondary != "" {
			if location, ok := m.NavigationSessionLocation(source.secondary); ok {
				source.secondaryWorkspace = location.WorkspaceID
			} else {
				source.secondary, source.secondaryWorkspace = "", ""
			}
		}
		if m.SessionID != source.session {
			root.cancelPagedHistory()
		}
		if m.SecondarySessionID != source.secondary && root.secondary != nil {
			root.secondary.cancelPagedHistory()
		}
		m.SessionID, m.WorkspaceID = source.session, source.workspace
		m.SecondarySessionID, m.SecondaryWorkspaceID = source.secondary, source.secondaryWorkspace
		m.FocusSecondary = source.focusSecondary && source.session != "" && source.secondary != ""
		a.returnTo = nil
	}
}

func (v *View) openArchivedSession(session domain.Session) {
	root := v.splitRoot()
	root.Model.FocusSecondary = false
	root.navigateSession(session.ID, session.WorkspaceID)
	a := root.archiveState()
	a.open = false
	a.viewing = session.ID
}

func (v *View) archiveSettings() {
	root := v.splitRoot()
	if root.settings != nil {
		root.settings.selectArchiveRetention()
	}
	if root.OpenSettings != nil {
		root.OpenSettings()
	}
}

func (v *View) archivePage(c *n.Context, t *n.Theme) {
	a := v.archiveState()
	m := v.Model
	var scope domain.Workspace
	if a.scope != "" {
		for _, w := range m.Snapshot.Workspaces {
			if w.ID == a.scope && w.Archived {
				scope = w
				break
			}
		}
		if scope.ID == "" {
			a.selectList(a.tab, "")
		}
	}
	inset := float32(configurationInset)
	if v.viewportWidth <= 1000 {
		inset = 24
	}
	n.Column(c.Key("archive-page")).FillHeight().Grow(1).MinWidth(0).AlignItems(n.Center).Children(func() {
		n.Column(c.Key("archive-content")).FillWidth().MaxWidth(912).Grow(1).MinHeight(0).Padding(0, inset, 24, inset).Children(func() {
			n.Row(c).FillWidth().Height(48+archiveTitleTopInset).Padding(archiveTitleTopInset, 0, 0, 0).
				Gap(8).AlignItems(n.Center).Margin(0, 0, 12, 0).DragWindow().Children(func() {
				title := "已归档"
				font := n.Font{Family: t.Font, Size: 20, Weight: 500}
				if scope.ID != "" {
					title = scope.Name
					const backLabel = "返回已归档工作区"
					back := control(c.Key("archive-back")).Size(28, 28).MarginStart(-5).Label(backLabel).Tooltip(backLabel).Children(func() {
						inlineSymbol(c, conversationBackIcon, title, font, 16, t.TextMuted).FixedLineHeight(28)
					})
					if back.Clicked() {
						a.selectList("workspaces", "")
					}
				}
				n.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(n.Baseline).Children(func() {
					n.Text(c, title).SingleLine().Ellipsis("…").Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(28).MinWidth(0).TooltipWhenTruncated(title)
					if scope.ID != "" {
						name := m.DeviceName(scope.DeviceID)
						n.Text(c, name).SingleLine().Ellipsis("…").MaxWidth(120).Shrink(0).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).TooltipWhenTruncated(name)
					}
				})
				button := control(c.Key("archive-retention")).Height(28).PaddingX(8).Radius(6).Label("归档保留设置").Tooltip("查看和调整各设备的归档保留期。").Children(func() {
					inlineIconText(c, settingsIcon, archiveRetentionTitle, n.Font{Family: t.Font, Size: 12}, 14, 6, t.TextMuted)
				})
				if button.Clicked() {
					v.archiveSettings()
				}
			})
			if scope.ID == "" {
				if next := configurationTabs(c.Key("archive-tabs"), a.tab, []configurationTabItem{{ID: "sessions", Label: "会话"}, {ID: "workspaces", Label: "工作区"}}, configurationUnderline, false); next != a.tab {
					a.selectList(next, "")
				}
			} else {
				n.Row(c).FillWidth().Height(36).Gap(8).AlignItems(n.Center).Children(func() {
					n.Text(c, "工作区已归档").FontSize(12).TextColor(t.TextMuted)
					n.Spacer(c).Grow(1)
					if configurationAction(c, "恢复工作区").Disabled(!v.canManage(m.WorkspaceConnection(scope.ID))).Clicked() {
						v.archiveWorkspace(scope, false)
					}
				})
			}
			index := 0
			if a.tab == "workspaces" {
				index = 1
			}
			key := a.tab + ":" + a.scope
			configurationTabPanel(c.Key("archive-panels"), key, index, configurationPanelFill, func() {
				n.Column(c).FillWidth().Grow(1).MinHeight(0).Margin(16, 0, 0, 0).Children(func() { v.archiveTable(c, t, a, scope, key) })
			}, false)
		})
	})
}

func (v *View) archiveTable(c *n.Context, t *n.Theme, a *archiveUI, scope domain.Workspace, key string) {
	m := v.Model
	workspaceRows := a.tab == "workspaces" && scope.ID == ""
	sessions := m.ArchivedSessions(a.query, scope.ID)
	workspaces := m.ArchivedWorkspaces(a.query)
	count := len(sessions)
	kind := "会话"
	placeholder := "搜索标题或工作区"
	if workspaceRows {
		count = len(workspaces)
		kind = "工作区"
		placeholder = "搜索归档工作区"
	}
	list := a.lists[key]
	if list == nil {
		list = &n.ListState{KeepOffset: true}
		a.lists[key] = list
	}
	n.Row(c).FillWidth().Height(28).Gap(12).AlignItems(n.Center).Margin(0, 0, 8, 0).Children(func() {
		field := fieldFeedback(c, n.Row(c).Grow(1).MaxWidth(300).Height(28).PaddingX(8).Gap(8).AlignItems(n.Center).Radius(6).Border(1, t.Border).Background(t.Background))
		field.Children(func() {
			n.Icon(c, searchIcon).Size(14, 14).TextColor(t.TextMuted)
			input := n.TextInputBase(c.Key("archive-search"), &a.query).Grow(1).MinWidth(0).Height(26).FontSize(12).FixedLineHeight(20).Placeholder(placeholder).Label(placeholder)
			if input.Changed() {
				list.ScrollTo(0, n.Start)
			}
			if a.query != "" && iconButton(c, closeIcon, "清除归档搜索").Clicked() {
				a.query = ""
				list.ScrollTo(0, n.Start)
				input.Focus()
			}
		})
		n.Spacer(c).Grow(1)
		n.Text(c, fmt.Sprintf("%d 个%s", count, kind)).FontSize(12).FontFeatures("tnum").TextColor(t.TextMuted)
		total := len(m.ArchivedSessions("", a.scope))
		if workspaceRows {
			total = len(m.ArchivedWorkspaces(""))
		}
		if configurationAction(c, "删除全部").Disabled(total == 0 || a.deletion != nil && a.deletion.busy).Tooltip("删除此列表的全部归档" + kind + "，包含搜索未显示的内容").Clicked() {
			all := v.archiveDeleteTargets(a)
			a.deletion = &archiveDeleteUI{batch: true, workspaces: workspaceRows, targets: all, count: len(all), open: true}
		}
	})
	if d := a.deletion; d != nil && !d.open && (d.busy || d.error != "") {
		n.Row(c.Key("archive-delete-status")).FillWidth().MinHeight(28).Gap(8).AlignItems(n.Center).Margin(0, 0, 8, 0).Children(func() {
			message := d.error
			color := t.Danger
			if d.busy {
				message, color = "正在删除…", t.TextMuted
				if d.batch {
					message = fmt.Sprintf("正在删除 %d / %d…", max(0, d.next-1), len(d.targets))
				}
			}
			n.Text(c, message).FontSize(12).FixedLineHeight(20).TextColor(color).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").TooltipWhenTruncated(message)
			if !d.busy && configurationAction(c, "重试失败项").Clicked() {
				v.deleteArchive(d)
			}
		})
	}
	metadataWidth := float32(160)
	gap := float32(16)
	if v.viewportWidth <= 1000 {
		metadataWidth = 112
		gap = 8
	}
	n.Row(c.Key("archive-table-header")).FillWidth().Height(28).PaddingX(8).Gap(gap).AlignItems(n.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		title, metadata := "标题", "工作区"
		if workspaceRows {
			title, metadata = "工作区", "设备"
		}
		n.Text(c, title).Grow(1).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		column := n.Text(c, metadata).Width(metadataWidth).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		if !workspaceRows {
			column.Label("归档工作区列")
		}
		n.Spacer(c).Width(archiveActionWidth).Grow(0).Shrink(0)
	})
	if count == 0 {
		message := "没有已归档的" + kind
		if a.query != "" {
			message = "没有匹配的归档内容"
		}
		n.Text(c, message).FillWidth().TextAlign(n.Center).PaddingY(84).FontSize(13).TextColor(t.TextMuted)
		n.Spacer(c).Grow(1)
		return
	}
	list.Key = func(i int) any {
		if workspaceRows {
			return workspaces[i].ID
		}
		return sessions[i].Session.ID
	}
	n.List(c.Key("archive-list:"+key), list, count, func(i int) {
		var session domain.Session
		var w domain.Workspace
		title, metadata := "", ""
		if workspaceRows {
			w = workspaces[i]
			title, metadata = w.Name, m.DeviceName(w.DeviceID)
		} else {
			session = sessions[i].Session
			w = sessions[i].Workspace
			title, metadata = session.Title, w.Name
		}
		v.archiveRow(c, t, a, w, session, title, metadata, metadataWidth, gap, workspaceRows)
	}).FillWidth().Grow(1).MinHeight(0).Label("已归档" + kind + "列表")
}

func (v *View) archiveRow(c *n.Context, t *n.Theme, a *archiveUI, w domain.Workspace, s domain.Session, title, metadata string, metadataWidth, gap float32, isWorkspace bool) {
	row := n.Row(c).FillWidth().Height(archiveRowHeight).PaddingX(8).Gap(gap).AlignItems(n.Center).BorderWidth(0, 0, .5, 0).BorderColor(t.Border)
	show := row.Hovered() || row.FocusWithin()
	if show {
		row.Background(t.Surface)
	}
	row.Children(func() {
		label := "查看归档会话：" + title
		if isWorkspace {
			label = "查看归档工作区：" + title
		}
		// Both text columns share one native baseline; actions use the same
		// measured glyph center while keeping their own 28-DIP hit rectangles.
		button := n.ButtonBase(c.Key("archive-title")).Grow(1).MinWidth(0).Height(archiveRowHeight).Justify(n.Start).Label(label).Children(func() {
			n.Row(c).FillWidth().Gap(gap).AlignItems(n.Baseline).Children(func() {
				n.Text(c, title).SingleLine().Ellipsis("…").Grow(1).FontSize(13).FixedLineHeight(20).TooltipWhenTruncated(title)
				n.Text(c.Key("archive-metadata"), metadata).Width(metadataWidth).SingleLine().Ellipsis("…").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).TooltipWhenTruncated(metadata).Label("归档归属：" + title)
			})
		})
		metadataLabel := "工作区："
		if isWorkspace {
			metadataLabel = "设备："
		}
		button.Description(metadataLabel + metadata)
		button.FocusRing(button.FocusVisible())
		if button.Pressed() {
			row.Background(t.SurfacePressed)
		}
		if button.Clicked() {
			if isWorkspace {
				a.selectList("workspaces", w.ID)
			} else {
				v.openArchivedSession(s)
			}
		}
		font := n.Font{Family: t.Font, Size: 13}
		opacity := float32(0)
		if show {
			opacity = 1
		}
		n.Row(c.Key("archive-actions")).Width(archiveActionWidth).Height(28).Gap(4).AlignItems(n.Center).Opacity(opacity).Transform(n.Translation(0, -inlineCenterOffset(title, font))).Children(func() {
			connection := v.Model.WorkspaceConnection(w.ID)
			ready := v.canManage(connection)
			restorable := ready && (isWorkspace || !w.Archived)
			if a.deletion != nil && a.deletion.busy {
				ready = false
				restorable = false
			}
			restoreLabel := "恢复会话：" + title
			if isWorkspace {
				restoreLabel = "恢复工作区：" + title
			}
			restore := iconButton(c, recoveryIcon, restoreLabel).Disabled(!show || !restorable)
			if !ready {
				restore.Tooltip("连接所属设备后可恢复")
			} else if !isWorkspace && w.Archived {
				restore.Tooltip("请先恢复所属工作区")
			}
			if restore.Clicked() {
				if isWorkspace {
					v.archiveWorkspace(w, false)
				} else {
					v.sessionChange("session.archive", s, "")
				}
			}
			deleteLabel := "彻底删除会话：" + title
			if isWorkspace {
				deleteLabel = "彻底删除工作区：" + title
			}
			remove := iconButton(c, archiveDeleteIcon, deleteLabel).Disabled(!show || !ready || !v.canDeleteArchive(connection))
			if !ready {
				remove.Tooltip("连接所属设备后可彻底删除")
			} else if !v.canDeleteArchive(connection) {
				remove.Tooltip("更新所属设备的 worker 后可彻底删除")
			}
			if remove.Clicked() {
				a.deletion = &archiveDeleteUI{connection: connection, workspace: w, session: s, open: true}
				if isWorkspace {
					for _, member := range v.Model.Snapshot.Sessions {
						if member.WorkspaceID == w.ID {
							a.deletion.count++
						}
					}
				}
			}
		})
	})
}

func (v *View) canDeleteArchive(key string) bool {
	if !v.canManage(key) {
		return false
	}
	if len(v.Model.DeviceStates) == 0 {
		return v.Model.WorkerMinor >= 40
	}
	state := v.Model.DeviceStates[key]
	return state != nil && state.WorkerMinor >= 40
}

func (v *View) archiveDialogs(c *n.Context, t *n.Theme) {
	a := v.archive
	if a == nil || a.deletion == nil {
		return
	}
	d := a.deletion
	compactDialog(c.Key("delete-archive"), t, &d.open, 380, func(panel n.Element) {
		kind := "会话"
		message := "“" + d.session.Title + "”将无法恢复。"
		if d.session.ID == "" {
			kind = "工作区"
			message = fmt.Sprintf("“%s”及其中的 %d 个会话将无法恢复。", d.workspace.Name, d.count)
		}
		if d.batch {
			kind = "全部归档会话"
			message = fmt.Sprintf("全部 %d 个归档会话将无法恢复。", d.count)
			if d.workspaces {
				kind = "全部归档工作区"
				message = fmt.Sprintf("全部 %d 个归档工作区及其中的会话将无法恢复。", d.count)
			}
		}
		panel.Children(func() {
			compactDialogContent(c, "彻底删除"+kind+"？", func() {
				n.Column(c).FillWidth().Gap(8).Children(func() {
					n.Text(c, message).FillWidth().FontSize(13).FixedLineHeight(20)
					n.Text(c, "中止会话任务，移除记录与附件；未完成的回收在后台重试。\n原项目文件和 Git 仓库不会删除。\n自建 worktree 备份到分支后回收，不备份被忽略的文件。").FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
					if d.error != "" {
						n.Text(c, d.error).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.Danger)
					}
				})
			}, func() {
				n.Spacer(c).Grow(1)
				if configurationAction(c, "取消").Disabled(d.busy).Clicked() {
					d.open = false
				}
				label := "彻底删除"
				if d.busy {
					label = "正在删除…"
				} else if d.batch && d.error != "" {
					label = "重试失败项"
				}
				if compactDialogPrimary(c, label).Disabled(d.busy || !d.batch && !v.canDeleteArchive(d.connection)).Clicked() {
					v.deleteArchive(d)
				}
			})
		})
	})
	if !d.open && !d.busy && d.error == "" {
		a.deletion = nil
	}
}

func (v *View) deleteArchive(d *archiveDeleteUI) {
	if d.busy {
		return
	}
	if d.batch {
		d.busy, d.open, d.error, d.next = true, false, "", 0
		d.failed = nil
		v.deleteNextArchive(d)
		return
	}
	method := "session.delete"
	input := protocol.ArchiveDeleteInput{SessionID: d.session.ID, Revision: d.session.Revision}
	if d.session.ID == "" {
		method = "workspace.delete"
		input = protocol.ArchiveDeleteInput{WorkspaceID: d.workspace.ID, Revision: d.workspace.Revision}
	}
	if d.operation == "" {
		d.operation = domain.NewID()
	}
	input.OperationID, input.CloseRuntime = d.operation, v.archiveCanCloseRuntime(d.connection)
	d.busy = true
	d.open = false
	d.error = ""
	if !v.do(app.Job{ConnectionID: d.connection, Method: method, Input: input, Apply: func(m *app.Model, body json.RawMessage, err error) {
		d.busy = false
		if err != nil {
			d.error = err.Error()
			return
		}
		var deletion domain.ArchiveDeletion
		if json.Unmarshal(body, &deletion) != nil {
			d.error = "设备返回了无效的删除回执"
			return
		}
		m.ApplyArchiveDeletion(deletion)
		if deletion.Warning != "" {
			m.ReportFeedback(app.Feedback{Title: "归档已删除", Body: deletion.Warning})
		}
		d.open = false
	}}) {
		d.busy = false
		d.error = "操作队列繁忙，请重试"
		d.open = true
	}
}

func (v *View) archiveCanCloseRuntime(connection string) bool {
	if len(v.Model.DeviceStates) == 0 {
		return v.Model.WorkerMinor >= 44
	}
	state := v.Model.DeviceStates[connection]
	return state != nil && state.WorkerMinor >= 44
}

func (v *View) archiveDeleteTargets(a *archiveUI) []archiveDeleteTarget {
	var out []archiveDeleteTarget
	if a.tab == "workspaces" && a.scope == "" {
		for _, w := range v.Model.ArchivedWorkspaces("") {
			out = append(out, archiveDeleteTarget{connection: v.Model.WorkspaceConnection(w.ID), method: "workspace.delete", title: w.Name, input: protocol.ArchiveDeleteInput{WorkspaceID: w.ID, Revision: w.Revision, OperationID: domain.NewID()}})
		}
	} else {
		for _, s := range v.Model.ArchivedSessions("", a.scope) {
			out = append(out, archiveDeleteTarget{connection: v.Model.WorkspaceConnection(s.Workspace.ID), method: "session.delete", title: s.Session.Title, input: protocol.ArchiveDeleteInput{SessionID: s.Session.ID, Revision: s.Session.Revision, OperationID: domain.NewID()}})
		}
	}
	return out
}

func (v *View) deleteNextArchive(d *archiveDeleteUI) {
	for d.next < len(d.targets) {
		target := d.targets[d.next]
		d.next++
		fail := func(message string) {
			d.failed = append(d.failed, target)
			if d.error == "" {
				d.error = target.title + "：" + message
			}
		}
		if !v.canDeleteArchive(target.connection) {
			fail("请连接所属设备并更新 worker 后重试")
			continue
		}
		target.input.CloseRuntime = v.archiveCanCloseRuntime(target.connection)
		if !v.do(app.Job{ConnectionID: target.connection, Method: target.method, Input: target.input, Apply: func(m *app.Model, body json.RawMessage, err error) {
			var deletion domain.ArchiveDeletion
			if err != nil {
				fail(err.Error())
			} else if json.Unmarshal(body, &deletion) != nil || deletion.DeviceID != m.Snapshot.DeviceID || target.input.SessionID != "" && !slices.Contains(deletion.SessionIDs, target.input.SessionID) || target.input.WorkspaceID != "" && deletion.WorkspaceID != target.input.WorkspaceID {
				fail("设备返回了无效的删除回执")
			} else {
				m.ApplyArchiveDeletion(deletion)
				if deletion.Warning != "" {
					m.ReportFeedback(app.Feedback{Title: "归档已删除", Body: deletion.Warning})
				}
			}
			v.deleteNextArchive(d)
		}}) {
			fail("操作队列繁忙，请重试")
			continue
		}
		return
	}
	d.busy = false
	if len(d.failed) > 0 {
		d.error = fmt.Sprintf("%d 项未删除。%s", len(d.failed), d.error)
		d.targets = d.failed
	}
}

func (v *View) syncArchivePolicies() {
	if v.parent != nil || v.Controller == nil {
		return
	}
	if v.archivePolicyPending == nil {
		v.archivePolicyPending = map[string]string{}
	}
	jobs := v.Model.ArchivePolicyJobs(func(connection, epoch string) bool {
		return strings.HasSuffix(v.archivePolicyPending[connection], "/"+epoch)
	})
	for _, job := range jobs {
		state := v.Model.DeviceStates[job.ConnectionID]
		if state == nil {
			continue
		}
		change := v.Model.ArchivePolicyChanges[state.Snapshot.DeviceID]
		token := change.ID + "/" + job.ConnectionEpoch
		if v.Saver != nil {
			v.queueState()
			job.Prepare = v.Saver.FlushLatest
		}
		apply := job.Apply
		key := job.ConnectionID
		job.Apply = func(m *app.Model, b json.RawMessage, err error) {
			if v.archivePolicyPending[key] == token {
				delete(v.archivePolicyPending, key)
			}
			apply(m, b, err)
			if v.settings != nil {
				v.settings.services.Invalidate()
			}
		}
		if v.Controller.Do(job) {
			v.archivePolicyPending[key] = token
		}
	}
}
