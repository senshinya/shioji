package main

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

func conversationHeaderScope(workspace, device string) string {
	return strings.Trim(workspace+" · "+device, " ·")
}

func (a *mobileApp) conversationHeaderScope(s domain.Session) string {
	return conversationHeaderScope(a.workspace(s.WorkspaceID).Name, a.deviceName())
}

func (a *mobileApp) conversationScope(s domain.Session) string {
	return strings.Trim(a.deviceName()+" · "+a.conversationWorkspaceScope(s), " ·")
}

func (a *mobileApp) conversationWorkspaceScope(s domain.Session) string {
	return strings.Trim(a.workspace(s.WorkspaceID).Name+" · "+s.GitBranch, " ·")
}

func (a *mobileApp) deviceName() string {
	if name := a.splitRoot().savedDevice(a.endpoint.DeviceID).Name; name != "" {
		return name
	}
	if a.preview {
		if a.previewDeviceName != "" {
			return a.previewDeviceName
		}
		return "Studio Display Mac"
	}
	if a.endpoint.Address == "" {
		return ""
	}
	return mobileDeviceName("", a.endpoint.Address)
}

func (a *mobileApp) devicesView(c *ui.Context) {
	a.maintainSessionCatalogs(c)
	p := palette(c)
	mobilePageBody(c, func() {
		mobileSection(c, "远程设备")
		devices := a.savedDevices.Devices
		if a.savedDevice(a.endpoint.DeviceID).Endpoint.DeviceID == "" && a.deviceName() != "" {
			devices = append([]savedMobileDevice{{Endpoint: a.endpoint, Name: a.deviceName()}}, devices...)
		}
		for _, saved := range devices {
			ep := saved.Endpoint
			active := ep.DeviceID == a.endpoint.DeviceID
			name := mobileDeviceName(saved.Name, ep.Address)
			catalog := a.sessionCatalogs[ep.DeviceID]
			fresh := !catalog.RequestedAt.IsZero() && c.Now().Sub(catalog.CheckedAt) < 2*sessionCatalogInterval
			timing := catalog.Timing
			status, statusInk := "正在检查", p.secondary
			if !catalog.CheckedAt.IsZero() {
				status = "离线"
			}
			online := fresh
			if active {
				name, ep = a.deviceName(), a.activeEndpoint()
				timing = a.activeDeviceTiming(ep.RouteID())
				phase := a.connectionPhase()
				online = a.model.Connected || fresh && (phase == app.ConnectionOpening || phase == app.ConnectionSyncing || phase == app.ConnectionSwitching)
				if !online && (phase == app.ConnectionUnavailable || phase == app.ConnectionSyncFailed) {
					status = "离线"
				}
			} else {
				for _, route := range saved.Endpoints() {
					if route.RouteID() == catalog.RouteID {
						ep = route
						break
					}
				}
			}
			if online {
				status, statusInk = "在线", p.active
			}
			subtitle := a.deviceRouteDisplay(ep, timing, online, c.Now()).summary()
			if a.preview && !active {
				switch ep.DeviceID {
				case "preview-laptop":
					name, subtitle, status, statusInk = "MacBook Pro", "直接 SSH · 18 ms", "在线", p.active
				case "preview-builder":
					name, subtitle, status = "构建机", "上次连接 09:26", "离线"
				}
			}
			_, glyph := mobileDeviceIcon(saved.Icon)
			mobileDetailRow(c.Key(ep.DeviceID), name, subtitle, name, glyph, func() {
				if active {
					a.openDevice(a.devices)
					return
				}
				if a.preview {
					a.cacheSessionCatalog()
					a.endpoint, a.previewDeviceName, a.model.Connected = saved.Endpoint, name, online
					if catalog.Snapshot.DeviceID != "" {
						a.model.Snapshot = catalog.Snapshot
						a.model.DevicePaths = []app.DevicePathStatus{{ID: ep.RouteID(), Phase: app.PathActive, Timing: timing}}
					}
					a.devices.Invalidate()
					a.openDevice(a.devices)
					return
				}
				if e := a.switchSavedDevice(saved.Endpoint); e != nil {
					a.setupError = e.Error()
					a.refresh()
					return
				}
				a.resetDeviceNavigation(func(err error) {
					if err == nil {
						a.openDevice(a.devices)
					}
				})
			}, func() {
				oneLine(c, status, 13, statusInk).PassThrough()
			}).Value(status + "，" + subtitle)
		}
		a.legacyMigrationRows(c)
		a.servicePreparationRows(c)
		mobileGroupHeading(c, "自动化")
		mobileRow(c.Key("all-schedules"), "定时任务", "", "", clockSymbol, func() {
			a.openSchedules(a.devices, "")
		})
		mobileGroupHeading(c, "网络")
		mobileRow(c.Key("tailnet"), "Tailnet", "设备发现与连接", "", deviceSymbol, func() { a.openTailnet(a.devices) })
		mobileRow(c.Key("forwards"), "端口转发", "", "", deviceSymbol, func() { a.openPortForwards(a.devices) })
		if a.setupError != "" {
			label(c, a.setupError, 13, p.warning).Padding(6, 12)
		}
	})
}

func deviceHost(endpoint mobileclient.Endpoint) string {
	if host, _, err := net.SplitHostPort(endpoint.Address); err == nil && host != "" {
		return host
	}
	return endpoint.Address
}

func (a *mobileApp) newConversationView(c *ui.Context, startAtTop bool) {
	p := palette(c)
	m := a.model
	ws := a.workspace(m.WorkspaceID)
	parent := a.draftPage
	if a.firstSubmissionVisible(parent) {
		a.conversation(c, parent, false)
		return
	}
	workspaces := m.Snapshot.Workspaces
	allArchived := len(workspaces) > 0 && !slices.ContainsFunc(workspaces, func(w domain.Workspace) bool { return !w.Archived })
	if allArchived || len(workspaces) == 0 && m.Connected && a.connectionPhase() == app.ConnectionIdle {
		ui.Column(c).Fill().Background(p.canvas).Children(func() {
			a.connectionNotice(c.Key("connection"), parent)
			ui.Spacer(c).Grow(1)
			mobileContent(c, draftComposerWidth).Padding(0, pageInset).Gap(14).AlignItems(ui.Center).Children(func() {
				title := "从一个工作区开始"
				if allArchived {
					title = "工作区已全部归档"
				}
				label(c, title, 20, p.ink).FontWeight(620).TextAlign(ui.Center)
				ui.Row(c).Wrap().Gap(8).Justify(ui.Center).Children(func() {
					primaryAction(c, "创建工作区", func() { a.openWorkspace(parent, domain.Workspace{}) }).
						Disabled(!m.Connected || a.connectionPhase() != app.ConnectionIdle || a.controller == nil && !a.preview)
					if allArchived {
						secondaryAction(c, "查看已归档", func() {
							a.archivePage(parent, mobileclient.WorkspaceLocation{}, "已归档", "工作区", false)
							a.refreshSessionCatalogs()
						})
					}
				})
			})
			ui.Spacer(c).Grow(1.2)
		})
		return
	}
	editor := a.bindDraftEditor(c, a.draftTarget())
	if startAtTop {
		editor.Doc.SetSelection(0, 0)
	}
	ui.Column(c).Fill().Background(p.canvas).Children(func() {
		a.connectionNotice(c.Key("connection"), parent)
		ui.Spacer(c).MinHeight(0)
		mobileContent(c, draftComposerWidth).Padding(0, pageInset).Children(func() {
			title := ws.Name
			if title == "" {
				title = "选择工作区"
			}
			ui.ButtonBase(c).MinHeight(mobileTapSize(c)).PaddingX(4).AlignItems(ui.Center).Gap(12).
				Disabled(editor.pending() || m.HasPendingInputForSession("")).
				Label("选择工作区，" + title).Role(ui.RoleButton).Children(func() {
				ui.Icon(c, folderSymbol).Size(16, 16).TextColor(p.secondary).PassThrough()
				oneLine(c, title, 15, p.ink).Grow(1).PassThrough()
				ui.Icon(c, pickerSymbol).Size(13, 13).TextColor(p.secondary).PassThrough()
			}).Menu(func(menu *ui.Menu) {
				for _, item := range m.Snapshot.Workspaces {
					if !item.Archived {
						menuChoice(menu, item.Name, item.ID == ws.ID, func() {
							selected := a.workspace(item.ID)
							if !editor.current(a, parent) || editor.pending() || m.HasPendingInputForSession("") ||
								selected.ID == "" || selected.Archived || selected.DeviceID != editor.target.device ||
								selected.ID == m.WorkspaceID {
								return
							}
							editor.Publish(m, editor.target.key)
							m.WorkspaceID = item.ID
							a.rebindHomeDraftAttachments()
							a.save()
							a.loadGit(item.ID)
							a.refresh()
						})
					}
				}
			})
			gitKey := "workspace:" + ws.ID
			status := m.GitStatuses[gitKey]
			if status.Applicable {
				config := m.GitDrafts[ws.ID]
				ui.Row(c).AlignItems(ui.Center).Gap(18).Children(func() {
					ui.SwitchBase(c, &config.NewWorktree).MinHeight(mobileTapSize(c)).PaddingX(4).Label("新建 worktree").
						AlignItems(ui.Center).Gap(12).OnChange(func() {
						latest := m.GitDrafts[ws.ID]
						latest.NewWorktree = config.NewWorktree
						m.GitDrafts[ws.ID] = latest
						a.save()
					}).Children(func() {
						fill, stroke := p.canvas, p.secondary
						if config.NewWorktree {
							fill, stroke = p.ink, p.ink
						}
						ui.Row(c).Size(16, 16).Radius(3).Background(fill).Border(1, stroke).AlignItems(ui.Center).Justify(ui.Center).
							PassThrough().Children(func() {
							if config.NewWorktree {
								ui.Icon(c, checkSymbol).Size(13, 13).TextColor(p.canvas).PassThrough()
							}
						})
						oneLine(c, "新建 worktree", 15, p.secondary).PassThrough()
					})
					ui.Spacer(c)
					open := func() {
						if !editor.current(a, parent) || editor.pending() || m.HasPendingInputForSession("") {
							return
						}
						if editor.Publish(m, editor.target.key) {
							a.save()
						}
						epoch := m.ConnectionEpoch
						a.openBranchPicker(parent, status.Branches, config.Branch, func(branch string) bool {
							latest := m.GitStatuses[gitKey]
							if !editor.current(a, parent) || m.HasPendingInputForSession("") || m.ConnectionEpoch != epoch ||
								!latest.Applicable || latest.Repository != status.Repository || !slices.Contains(latest.Branches, branch) {
								return false
							}
							updated := m.GitDrafts[ws.ID]
							updated.Branch = branch
							m.GitDrafts[ws.ID] = updated
							a.save()
							a.refresh()
							return true
						})
					}
					title := config.Branch
					if title == "" {
						title = "选择分支"
					}
					iconAction(c, title, "选择分支", branchSymbol, inlineIconSize, pickerSymbol, 13, 15, 400, open).
						Value(config.Branch).MinWidth(mobileTapSize(c)).PaddingX(4).Shrink(1).Justify(ui.End).Disabled(len(status.Branches) == 0)
				})
			}
			if entries := a.operationFeedbackRecords(ws.ID, ""); len(entries) != 0 {
				mobileRow(c.Key("draft-operation"), "最近操作", entries[0].Title, "", clockSymbol, func() {
					a.openOperationHistory(parent, ws.ID, "")
				})
			}
		})
		a.drawComposer(c, parent, a.session(), editor, true, false)
	})
}

func (a *mobileApp) loadGit(workspace string) {
	if workspace == "" || !a.model.Connected || a.controller == nil {
		return
	}
	query := protocol.GitQuery{WorkspaceID: workspace}
	if !a.controller.Do(app.Job{Method: "git.status", Input: query, ConnectionEpoch: a.model.ConnectionEpoch,
		Apply: func(m *app.Model, data json.RawMessage, err error) {
			var status domain.GitStatus
			if err == nil {
				err = json.Unmarshal(data, &status)
			}
			if err != nil {
				m.ReportFeedback(app.Feedback{WorkspaceID: workspace, Title: "Git 操作未完成", Details: err.Error()})
				return
			}
			m.SetGitStatus("workspace:"+workspace, status)
		},
	}) {
		a.model.ReportFeedback(app.Feedback{WorkspaceID: workspace, Title: "Git 操作未完成", Body: "请求繁忙，请稍后重试。"})
		a.refresh()
	}
}

func (a *mobileApp) openActivity(parent *mygo.Window, sid string) {
	var w *mygo.Window
	w = a.page("会话活动", func(c *ui.Context) {
		mobilePageBody(c, func() {
			mobileRow(c.Key("outline"), "对话导航", "", "", conversationsSymbol, func() { a.openConversationOutline(w, parent, sid) })
			mobileRow(c.Key("find"), inConversationFind, "", "", searchSymbol, func() {
				a.openConversationFind(parent, a.sessionByID(sid), false)
				a.callUIKit("showPage", map[string]uintptr{"window": parent.NativeHandle()}, nil)
			})
			mobileRow(c.Key("plan"), "计划与任务", "", "", planSymbol, func() { a.openPlan(w, sid) })
			mobileRow(c.Key("actions"), "会话操作", "", "", moreSymbol, func() { a.openSessionActions(w, sid) })
			if session := a.sessionByID(sid); session.ID != "" {
				if a.model.SessionReadiness(session).Capabilities.Compact {
					state := a.compaction(session)
					if a.compactionReason(session, state) == "" {
						mobileRow(c.Key("compact"), "压缩上下文", "", "", compactSymbol, func() {
							a.beginCompaction(session, nil)
							a.callUIKit("showPage", map[string]uintptr{"window": parent.NativeHandle()}, nil)
						})
					}
				}
				mobileRow(c.Key("operation-history"), "最近操作", "", "", clockSymbol, func() {
					a.openOperationHistory(w, session.WorkspaceID, sid)
				})
			}
			mobileRow(c.Key("review"), "工作区改动", "", "", filesSymbol, func() {
				if a.wide && parent == a.detail {
					a.openReview(parent, a.sessionByID(sid), parent, a.conversationEditor)
					a.callUIKit("showPage", map[string]uintptr{"window": parent.NativeHandle()}, nil)
					return
				}
				a.openReview(w, a.sessionByID(sid), parent, a.conversationEditor)
			})
			if a.wide {
				mobileRow(c.Key("parallel"), "并排查看会话", "", "", panelSymbol, func() { a.openParallelPicker(w, sid) })
			}
			mobileRow(c.Key("agent"), "子 Agent", "", "", branchSymbol, func() {
				a.openWorkList(w, sid, "agent", "子 Agent")
			})
			mobileRow(c.Key("background"), "后台任务", "", "", terminalSymbol, func() {
				a.openWorkList(w, sid, "background", "后台任务")
			})
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) seedPreview() {
	m := a.model
	m.Connected = true
	m.Snapshot.DeviceID = "preview-device"
	m.WorkerMinor = 40
	m.Readiness = domain.Readiness{State: "ready", HarnessID: "codex", Permissions: []string{"default"},
		PermissionChoices: []domain.OptionChoice{{ID: "default", Name: "默认权限"}},
		Models:            []domain.Model{{ID: "gpt-5.4", Name: "GPT-5.4", Default: true}}}
	m.Snapshot.Workspaces = []domain.Workspace{
		{ID: "ios", DeviceID: m.Snapshot.DeviceID, Name: "Shioji iOS", Folders: []string{"/Users/developer/Shioji"}, Revision: 1},
		{ID: "desktop", DeviceID: m.Snapshot.DeviceID, Name: "Shioji Mac"},
	}
	m.WorkspaceID = "ios"
	m.GitStatuses["workspace:ios"] = domain.GitStatus{Applicable: true, Branch: "main", DefaultBranch: "main", Branches: []string{"main", "mobile-composer"}}
	m.GitDrafts["ios"] = domain.GitChoice{Branch: "main"}
	m.Snapshot.Sessions = []domain.Session{
		{ID: "review", WorkspaceID: "ios", Title: "检查移动端会话体验", GitBranch: "main", Status: "running", HarnessID: "codex"},
		{ID: "keyboard", WorkspaceID: "ios", Title: "整理键盘交互和阅读位置", GitBranch: "mobile-composer", Status: "completed", HarnessID: "codex"},
		{ID: "work", WorkspaceID: "desktop", Title: "核对本轮后台任务", GitBranch: "main", Status: "idle", HarnessID: "codex"},
		{ID: "archived", WorkspaceID: "ios", Title: "旧版原生动画验证", Status: "completed", Archived: true},
	}
	s := m.Snapshot.Sessions[0]
	queue := domain.QueueState{SessionID: s.ID, Revision: 1}
	for i, text := range []string{"核对浅色和深色状态", "检查 iPad 窄窗切换后的阅读位置", "补上系统减少动态效果的录制", "确认长队列保留最后一条"} {
		queue.Items = append(queue.Items, domain.QueueItem{ID: fmt.Sprint(i), Input: domain.Submission{Text: text, WorkspaceID: s.WorkspaceID}})
	}
	sentAt := time.Date(2026, time.November, 19, 9, 36, 0, 0, time.Local)
	m.Details[s.ID] = domain.SessionDetail{Session: s, HistoryLoaded: true, Queue: queue, Plan: &domain.Plan{Steps: []domain.PlanStep{{Text: "核对原生动画", Status: "completed"}, {Text: "验证断线重连", Status: "in_progress"}}}, Items: []domain.Item{
		{ID: "prompt", Kind: "user", SentAt: &sentAt, Text: "帮我检查会话切换、待发消息和返回手势，尽量使用原生动画。"},
		{ID: "tool", Kind: "tool", Name: "读取会话和导航实现", Text: "读取了会话列表、输入框和待发消息的原生组件。"},
		{ID: "reply", Kind: "assistant", Text: "我先核对了会话列表和输入框。\n\n- 返回使用系统导航，侧滑取消时保留选中状态。\n- 待发消息展开后可以滚动，最多露出两条半。", Final: true},
	}, Works: []domain.WorkActivity{
		{ID: "agent", Kind: "agent", Title: "检查 iPad 分栏", State: "running"},
		{ID: "task", Kind: "background", Title: "编译模拟器构建", State: "completed"},
	}}
	if a.previewRoute == "board-home-empty" {
		m.Snapshot.Sessions = nil
	}
}
