package ui

import (
	"encoding/json"
	"fmt"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
)

const archiveRowHeight float32 = 40
const archiveActionWidth float32 = 60 // two 28-DIP targets and their 4-DIP gap

var archiveDeleteIcon = svg("M4 5h12M8 2h4l1 3M5 5l1 13h8l1-13M8 8v7M12 8v7")

type archiveUI struct {
	open                       bool
	tab, scope, query, viewing string
	lists                      map[string]*n.ListState
	deletion                   *archiveDeleteUI
	queries                    map[string]string
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
	a := root.archiveState()
	a.open = true
	a.viewing = ""
	root.workspaceEditor = nil
	root.skillReturn = nil
	root.devices = false
	root.searchOpen = false
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
		root.settings.selectPage("appearance")
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
	n.Column(c.Key("archive-page")).FillHeight().Grow(1).MinWidth(0).Children(func() {
		n.Row(c).FillWidth().Height(48).DragWindow()
		n.Column(c).Fill().AlignItems(n.Center).Children(func() {
			inset := float32(configurationInset)
			if v.viewportWidth <= 1000 {
				inset = 24
			}
			n.Column(c.Key("archive-content")).FillWidth().MaxWidth(912).Grow(1).MinHeight(0).Padding(24, inset, 24, inset).Children(func() {
				n.Row(c).FillWidth().Height(32).Gap(8).AlignItems(n.Center).Margin(0, 0, 12, 0).Children(func() {
					if scope.ID != "" && iconButton(c, conversationBackIcon, "返回已归档工作区").MarginStart(-5).Clicked() {
						a.selectList("workspaces", "")
					}
					title := "已归档"
					if scope.ID != "" {
						title = scope.Name
					}
					n.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(n.Baseline).Children(func() {
						n.Text(c, title).SingleLine().Ellipsis("…").FontSize(20).FontWeight(500).FixedLineHeight(28).MinWidth(0).TooltipWhenTruncated(title)
						if scope.ID != "" {
							name := m.DeviceName(scope.DeviceID)
							n.Text(c, name).SingleLine().Ellipsis("…").MaxWidth(120).Shrink(0).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).TooltipWhenTruncated(name)
						}
					})
					label := fmt.Sprintf("保留 %d 天", m.ArchiveDays())
					if m.ArchiveDays() == 0 {
						label = "永久保留"
					}
					help := fmt.Sprintf("从归档之日起 %d 天后自动删除。点击调整保留时间。", m.ArchiveDays())
					if m.ArchiveDays() == 0 {
						help = "不会自动删除归档内容。点击调整保留时间。"
					}
					if status := m.ArchivePolicyStatus(); status != "" {
						label = "保留时间"
						help = status
					}
					button := control(c.Key("archive-retention")).Height(28).PaddingX(8).Radius(6).Label("归档保留设置").Tooltip(help).Children(func() { inlineIconText(c, settingsIcon, label, n.Font{Family: t.Font, Size: 12}, 14, 6, t.TextMuted) })
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
					n.Column(c).Fill().Margin(16, 0, 0, 0).Children(func() { v.archiveTable(c, t, a, scope, key) })
				}, false)
			})
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
	})
	metadataWidth := float32(160)
	gap := float32(16)
	if v.viewportWidth <= 1000 {
		metadataWidth = 112
		gap = 8
	}
	n.Row(c.Key("archive-table-header")).FillWidth().Height(28).PaddingX(8).Gap(gap).AlignItems(n.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		title, metadata := "标题", "Workspace"
		if workspaceRows {
			title, metadata = "工作区", "设备"
		}
		n.Text(c, title).Grow(1).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		n.Text(c, metadata).Width(metadataWidth).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
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
	n.DialogBase(c.Key("delete-archive"), &d.open, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .16)).Transition(fadeMotion(c, 180*time.Millisecond))
		width, _ := c.Size()
		panel.Width(min(380, width-48)).Padding(24).Radius(12).Background(raised(t)).Border(1, t.Border).Shadow(0, 8, 28, 0, n.RGBA(0, 0, 0, .16)).Transition(popupMotion(c)).Children(func() {
			n.Column(c).FillWidth().Gap(12).Children(func() {
				kind, title := "会话", d.session.Title
				message := "“" + title + "”将无法恢复。"
				if d.session.ID == "" {
					kind = "工作区"
					title = d.workspace.Name
					message = fmt.Sprintf("“%s”及其中的 %d 个会话将无法恢复。", title, d.count)
				}
				n.Text(c, "彻底删除"+kind+"？").FontSize(15).FontWeight(500).FixedLineHeight(22)
				n.Text(c, message).FontSize(13).FixedLineHeight(22)
				n.Text(c, "同时移除 Shioji 保存的记录与附件。\n项目文件和 Git 仓库不会删除。").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
				if d.error != "" {
					n.Text(c, d.error).FontSize(12).FixedLineHeight(20).TextColor(t.Danger)
				}
				n.Row(c).FillWidth().Gap(8).Margin(12, 0, 0, 0).Children(func() {
					n.Spacer(c).Grow(1)
					if configurationAction(c, "取消").Disabled(d.busy).Clicked() {
						d.open = false
					}
					label := "彻底删除"
					if d.busy {
						label = "正在删除…"
					}
					if n.PrimaryButton(c, label).Height(28).FontSize(12).Radius(6).Disabled(d.busy || !v.canDeleteArchive(d.connection)).Clicked() {
						v.deleteArchive(d)
					}
				})
			})
		})
	})
	if d.busy {
		d.open = true
	}
	if !d.open && !d.busy {
		a.deletion = nil
	}
}

func (v *View) deleteArchive(d *archiveDeleteUI) {
	method := "session.delete"
	input := protocol.ArchiveDeleteInput{SessionID: d.session.ID, Revision: d.session.Revision}
	if d.session.ID == "" {
		method = "workspace.delete"
		input = protocol.ArchiveDeleteInput{WorkspaceID: d.workspace.ID, Revision: d.workspace.Revision}
	}
	d.busy = true
	d.error = ""
	if !v.do(app.Job{ConnectionID: d.connection, Method: method, Input: input, Apply: func(m *app.Model, body json.RawMessage, err error) {
		d.busy = false
		if err != nil {
			d.error = err.Error()
			d.open = true
			return
		}
		var deletion domain.ArchiveDeletion
		if json.Unmarshal(body, &deletion) != nil {
			d.error = "设备返回了无效的删除回执"
			d.open = true
			return
		}
		m.ApplyArchiveDeletion(deletion)
		d.open = false
	}}) {
		d.busy = false
		d.error = "操作队列繁忙，请重试"
	}
}

func (v *View) syncArchivePolicies() {
	if v.parent != nil || v.Controller == nil {
		return
	}
	if v.archivePolicyPending == nil {
		v.archivePolicyPending = map[string]string{}
	}
	jobs := v.Model.ArchivePolicyJobs()
	for _, job := range jobs {
		state := v.Model.DeviceStates[job.ConnectionID]
		if state == nil {
			continue
		}
		change := v.Model.ArchivePolicyChanges[state.Snapshot.DeviceID]
		token := change.ID + "/" + job.ConnectionEpoch
		if pending := v.archivePolicyPending[job.ConnectionID]; pending != "" && strings.HasSuffix(pending, "/"+job.ConnectionEpoch) {
			continue
		}
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
