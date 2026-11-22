package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/mobileclient"
)

const archivedSessionSearch = "搜索标题或工作区"

func (a *mobileApp) openArchive(parent *mygo.Window) {
	a.openArchivePage(parent, false)
}

func (a *mobileApp) openArchivePage(parent *mygo.Window, revealRetention bool) {
	if a.wide && parent == a.home && a.detail != nil {
		parent = a.detail
	}
	a.archivePage(parent, mobileclient.WorkspaceLocation{}, "已归档", "会话", revealRetention)
	a.refreshSessionCatalogs()
}

func (a *mobileApp) archivePage(parent *mygo.Window, scope mobileclient.WorkspaceLocation, title, tab string, revealRetention bool) {
	queries := map[string]*string{"会话": new(string), "工作区": new(string)}
	var req mobileRequest
	var w *mygo.Window
	var menu mobileContextMenu
	var opening *mobileclient.ArchivedConversation
	var deadline time.Time
	var retentionRevealed bool
	open := func(row mobileclient.ArchivedConversation) {
		if opening != nil {
			return
		}
		if a.preview || a.model.Connected && a.model.ConnectionEpoch != "" && a.model.Snapshot.DeviceID == row.Device.ID && a.endpoint.DeviceID == row.Device.ID {
			a.openArchivedConversation(w, row, &req)
			return
		}
		if err := a.openDeviceFromResults(w, row.Device.ID); err != nil {
			req.err = err.Error()
			w.Invalidate()
			return
		}
		opening, deadline, req.err = &row, time.Now().Add(sessionCatalogTimeout), ""
		w.Invalidate()
	}
	w = a.page(title, func(c *ui.Context) {
		if opening != nil {
			switch {
			case a.model.Connected && a.model.ConnectionEpoch != "" && a.model.Snapshot.DeviceID == opening.Device.ID && a.endpoint.DeviceID == opening.Device.ID:
				row := *opening
				opening = nil
				mygo.RunOnMain(func() { a.openArchivedConversation(w, row, &req) })
			case !c.Now().Before(deadline):
				opening, req.err = nil, "无法连接归档所属的设备"
			default:
				c.After(deadline.Sub(c.Now()))
			}
		}
		catalogs := a.browseCatalogs()
		rows := mobileclient.CollectArchive(catalogs, scope)
		deletion := a.archiveDeletion
		busy := req.busy || opening != nil || deletion != nil && deletion.running
		frame := contextMenuFrame{zoom: c.Zoom()}
		mobilePageBody(c, func() {
			if scope.WorkspaceID == "" {
				mobileSegments(c, []mobileSegment{{title: "会话"}, {title: "工作区"}}, tab, func(title string) { tab = title })
			} else {
				if len(rows.Workspaces) == 0 {
					mobileMessage(c, "此工作区已离开归档")
					a.archiveDeletionStatus(c, deletion)
					return
				}
				row := rows.Workspaces[0]
				ui.Row(c.Key("workspace-context")).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Children(func() {
					oneLine(c, row.Device.Label()+" · 已归档", captionTextSize, palette(c).secondary).Grow(1).MinWidth(0)
					action(c, "恢复工作区", func() { a.restoreArchiveWorkspace(w, &req, row) }).Disabled(busy || !a.canUseArchiveDevice(row.Device, 5))
					action(c, "彻底删除工作区", func() {
						a.confirmArchiveDeletion(w, []mobileArchiveTarget{archiveWorkspaceTarget(row)}, false, "")
					}).Disabled(busy || !a.canUseArchiveDevice(row.Device, 40))
				})
			}

			workspaceList := scope.WorkspaceID == "" && tab == "工作区"
			query := queries[tab]
			hits := rows.Match(*query)
			kind, placeholder := "会话", archivedSessionSearch
			filtered, total := len(hits.Sessions), len(rows.Sessions)
			if workspaceList {
				kind, placeholder = "工作区", "搜索归档工作区"
				filtered, total = len(hits.Workspaces), len(rows.Workspaces)
			}
			search := c.Key("archive-search:" + tab)
			field := mobileSearchField(search, ui.Row(search).Margin(5, 0, 2), query, placeholder, placeholder, func() {
				if *query != "" {
					iconButton(search, "清除归档搜索", closeSymbol, 13, func() { *query = ""; w.Invalidate() }).
						KeepFocus().Margin(0, -9, 0, 0)
				}
			})
			if field.Changed() {
				w.Invalidate()
			}
			p := palette(c)
			ui.Row(c.Key("archive-heading")).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Gap(8).Children(func() {
				oneLine(c.Key("count"), fmt.Sprintf("%d 个%s", filtered, kind), captionTextSize, p.secondary).
					FontFeatures("tnum").Grow(1)
				if deletion != nil && deletion.running {
					status := "删除中"
					if deletion.batch {
						status = fmt.Sprintf("删除中 %d/%d", deletion.next, len(deletion.targets))
					}
					oneLine(c.Key("archive-delete-progress"), status, captionTextSize, p.secondary).
						FontFeatures("tnum").Role(ui.RoleStatus).PaddingX(3).MaxWidthPercent(60).Shrink(0)
					return
				}
				action(c.Key("delete-all"), "删除全部", func() {
					var targets []mobileArchiveTarget
					if workspaceList {
						for _, row := range rows.Workspaces {
							targets = append(targets, archiveWorkspaceTarget(row))
						}
					} else {
						for _, row := range rows.Sessions {
							targets = append(targets, archiveSessionTarget(row))
						}
					}
					scopeName := ""
					if scope.WorkspaceID != "" {
						scopeName = title
					}
					a.confirmArchiveDeletion(w, targets, true, scopeName)
				}).PaddingX(3).Disabled(total == 0 || busy)
			})
			a.archiveDeletionStatus(c, deletion)
			if opening != nil {
				mobileMessage(c, "正在打开 "+opening.Device.Label()+" 的会话…")
			}
			if workspaceList {
				for _, row := range hits.Workspaces {
					key := "workspace:" + row.Device.ID + ":" + row.ID
					var restore, remove func()
					if !busy && a.canUseArchiveDevice(row.Device, 5) {
						restore = func() { a.restoreArchiveWorkspace(w, &req, row) }
					}
					if !busy && a.canUseArchiveDevice(row.Device, 40) {
						remove = func() { a.confirmArchiveDeletion(w, []mobileArchiveTarget{archiveWorkspaceTarget(row)}, false, "") }
					}
					a.archiveRow(c.Key(key), &frame, key, row.Name, fmt.Sprintf("%s · %d 个会话", row.Device.Label(), row.Count),
						"恢复工作区", folderSymbol, contextMenuTarget{}, func() {
							a.archivePage(w, mobileclient.WorkspaceLocation{DeviceID: row.Device.ID, WorkspaceID: row.ID}, row.Name, "会话", false)
						}, restore, remove)
				}
			} else {
				for _, row := range hits.Sessions {
					s := row.Session
					key := "session:" + row.Device.ID + ":" + row.Workspace.ID + ":" + s.ID
					subtitle := row.Workspace.Name + " · 只读"
					if len(catalogs) > 1 {
						subtitle = row.Workspace.Name + " · " + row.Device.Label() + " · 只读"
					}
					var restore, remove func()
					if !busy && s.Archived && !row.Workspace.Archived && a.canUseArchiveDevice(row.Device, 5) {
						restore = func() { a.restoreArchiveSession(w, &req, row) }
					}
					if !busy && a.canUseArchiveDevice(row.Device, 40) {
						remove = func() { a.confirmArchiveDeletion(w, []mobileArchiveTarget{archiveSessionTarget(row)}, false, "") }
					}
					target := contextMenuTarget{session: s, location: app.SessionLocation{
						DeviceID: row.Device.ID, WorkspaceID: row.Workspace.ID, SessionID: s.ID,
					}}
					a.archiveRow(c.Key(key), &frame, key, s.Title, subtitle, "恢复会话", filesSymbol, target,
						func() { open(row) }, restore, remove)
				}
			}
			if filtered == 0 {
				message := "没有归档" + kind
				if total != 0 {
					message = "没有匹配的归档内容"
				}
				mobileMessage(c, message)
			}
			if scope.WorkspaceID == "" {
				var found bool
				policy := ui.Column(c.Key("archive-retention")).FillWidth().Children(func() {
					found = a.archiveRetention(c, w, &req, busy, catalogs)
					if revealRetention && !found {
						mobileGroupHeading(c, "归档保留期")
						mobileMessage(c, "连接支持归档管理的执行设备后可设置保留期。")
					}
				})
				if revealRetention && (!retentionRevealed || found) {
					policy.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollStart})
					retentionRevealed = true
					revealRetention = !found
				}
			}
			req.status(c)
		}).DrawOver(func(_ *ui.Painter, viewport ui.Rect) { menu.publish(a, w, frame, viewport) })
	})
	menu.listen(a, w)
	if a.archivePages == nil {
		a.archivePages = make(map[uintptr]*mygo.Window)
	}
	handle := w.NativeHandle()
	a.archivePages[handle] = w
	w.OnClosed(func() { delete(a.archivePages, handle) })
	a.pushNative(parent, w)
}

func (a *mobileApp) openArchivedConversation(w *mygo.Window, row mobileclient.ArchivedConversation, req *mobileRequest) {
	if !a.pageCurrent(w) {
		return
	}
	s, workspace := a.sessionByID(row.Session.ID), a.workspace(row.Workspace.ID)
	if workspace.ID == "" || workspace.DeviceID != row.Device.ID || s.ID == "" || s.WorkspaceID != workspace.ID ||
		s.Context.DeviceID != "" && s.Context.DeviceID != row.Device.ID || s.ThreadID != row.Session.ThreadID ||
		!workspace.Archived && !s.Archived {
		req.err = "归档会话已变化，请重新选择"
		w.Invalidate()
		return
	}
	a.openSessionFrom(w, s.ID)
}

func (a *mobileApp) archiveRow(c *ui.Context, frame *contextMenuFrame, key, title, subtitle, restoreTitle string, icon *ui.SVG,
	target contextMenuTarget, open, restore, remove func()) {
	row := mobileDetailRow(c, title, subtitle, title, icon, open, nil).FillWidth().MinWidth(0).Value(subtitle)
	target.open = open
	if restore != nil {
		target.commands = append(target.commands, contextMenuCommand{ID: "restore", Title: restoreTitle, Symbol: "arrow.uturn.backward", run: restore})
	}
	if remove != nil {
		target.commands = append(target.commands, contextMenuCommand{ID: "delete", Title: "彻底删除", Symbol: "trash", Destructive: true, run: remove})
	}
	hint := "长按显示归档操作"
	if target.session.ID != "" {
		hint = "长按预览会话"
	}
	frame.add(row.Description(hint), key, target)
}
