package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"path"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

type workspaceEditor struct {
	Tab, Harness            string
	GeneralScroll           n.ScrollState
	Workspace               domain.Workspace
	HookSession             domain.Session
	Main, Connection, Error string
	Busy                    bool
}
type sessionRename struct {
	Session                  domain.Session
	Title, Connection, Error string
	Open, Busy               bool
	NaturalHeight            float32
	attempt                  uint64
}

var moreActionsIcon = svg("M4 10h.1M10 10h.1M16 10h.1")

type navVisibility struct {
	From, To float32
	Started  time.Time
}

func (s *navVisibility) value(now time.Time) float32 {
	p := min(1, float32(now.Sub(s.Started))/float32(180*time.Millisecond))
	return s.From + (s.To-s.From)*n.EaseOut(p)
}

func (s *navVisibility) retarget(now time.Time, target float32) float32 {
	value := s.value(now)
	if target != s.To {
		s.From, s.To, s.Started = value, target, now
	}
	return value
}

func (v *View) navigationPresence(c *n.Context, key string, visible bool) float32 {
	if v.navVisibility == nil {
		v.navVisibility = map[string]*navVisibility{}
	}
	target := float32(0)
	if visible {
		target = 1
	}
	state := v.navVisibility[key]
	if state == nil {
		state = &navVisibility{From: target, To: target, Started: c.Now()}
		v.navVisibility[key] = state
	}
	if c.Preferences().ReduceMotion {
		state.From, state.To = target, target
		return target
	}
	value := state.retarget(c.Now(), target)
	if value != target {
		c.AnimationFrame()
	}
	return value
}

func (v *View) toolbarWorkspace(c *n.Context, t *n.Theme) {
	var workspace domain.Workspace
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == v.Model.WorkspaceID {
			workspace = w
			break
		}
	}
	trigger := control(c.Key("workspace-directories")).Height(26).PaddingX(4).Radius(5).Label("工作区目录").Children(func() { n.Text(c, v.workspaceName()).SingleLine().MaxWidth(150).FontSize(12).TextColor(t.TextMuted) })
	if v.menuOpen == nil {
		v.menuOpen = map[string]bool{}
	}
	open := v.menuOpen["workspace-directories"]
	if trigger.Clicked() {
		open = !open
	}
	panel := n.PopoverBase(c.Key("workspace-directories-popover"), trigger, &open, func(panel n.Element) {
		panel.Transition(workspacePopupMotion(c))
		compactMenuSurface(panel, t).Width(280).Children(func() {
			n.Scroll(c).FillWidth().MaxHeight(320).Children(func() {
				n.Column(c).FillWidth().Gap(0).Children(func() {
					const iconGap = 7
					font := n.Font{Family: t.Font, Size: compactMenuFontSize, Weight: 400}
					for i, folder := range workspace.Folders {
						n.Row(c.Key(folder)).FillWidth().Height(compactMenuRowHeight).PaddingX(compactMenuRowInset).
							Gap(iconGap).AlignItems(n.Baseline).Tooltip(folder).Children(func() {
							inlineSymbol(c, workspaceIcon, folder, font, compactMenuIconSize, t.TextMuted)
							n.Text(c, path.Base(folder)).SingleLine().FontSize(compactMenuFontSize).FixedLineHeight(20).Grow(1)
							if i == 0 {
								n.Text(c, "主目录").FontSize(11).TextColor(t.TextMuted)
							}
						})
					}
					if len(workspace.Folders) > 0 {
						n.Column(c).Height(1).FillWidth().Background(t.Border).MarginY(3)
					}
					action := func(label string, icon *n.SVG, disabled bool) n.Element {
						return control(c.Key(label), compactMenuRowRadius).FillWidth().Height(compactMenuRowHeight).
							PaddingX(compactMenuRowInset).Radius(compactMenuRowRadius).Justify(n.Start).Label(label).Disabled(disabled).Children(func() {
							inlineIconText(c, icon, label, font, compactMenuIconSize, iconGap, t.Text)
						})
					}
					if action("工作区设置", settingsIcon, workspace.ID == "").Clicked() {
						v.editWorkspace(workspace)
						open = false
					}
					if v.Model.SessionID != "" && v.Model.ArchivedContext() != "" {
						for _, s := range v.Model.Snapshot.Sessions {
							if s.ID == v.Model.SessionID && v.canInspectSessionHooks(s) && action("会话 Hooks", settingsIcon, false).Clicked() {
								v.openSessionHooks(s)
								open = false
							}
						}
					}
					key := v.Model.WorkspaceConnection(workspace.ID)
					if action("导入会话…", conversationImportIcon, workspace.ID == "" || workspace.Archived || !v.canImportConversations(key)).Clicked() {
						v.openConversationImport(key, workspace, false)
						open = false
					}
				})
			})
		})
	})
	if panel.Dismissed() {
		trigger.Focus()
	}
	v.menuOpen["workspace-directories"] = open
}

func (v *View) canManage(key string) bool {
	if v.Controller == nil {
		return false
	}
	if len(v.Model.DeviceStates) == 0 {
		return v.Model.Connected && v.Model.WorkerMinor >= 5
	}
	state := v.Model.DeviceStates[key]
	return state != nil && state.Connected && state.WorkerMinor >= 5
}
func (v *View) workspaceMenu(menu *n.Menu, w domain.Workspace) {
	key := v.Model.WorkspaceConnection(w.ID)
	ready := v.canManage(key)
	if menu.Item("新建会话").Disabled(w.Archived).Chosen() {
		v.Model.WorkspaceID = w.ID
		v.home()
	}
	if menu.Item("工作区设置").Chosen() {
		v.editWorkspace(w)
	}
	if menu.Item("导入会话…").Disabled(w.Archived || !v.canImportConversations(key)).Chosen() {
		v.openConversationImport(key, w, false)
	}
	menu.Separator()
	label := "归档工作区"
	if w.Archived {
		label = "恢复工作区"
	}
	if menu.Item(label).Disabled(!ready || v.pendingArchiveChange(w.ID, "") != nil).Chosen() {
		v.archiveWorkspace(w, !w.Archived)
	}
	if !ready {
		menu.Separator()
		menu.Item("需要连接并更新执行服务").Disabled(true)
	}
}
func (v *View) sessionMenu(menu *n.Menu, s domain.Session, ref app.SessionLocation) {
	v.navigationMenu(menu, s, ref)
	if menu.Item("分叉会话").Disabled(!v.historyReady(s) || !v.persistenceModel().SessionReadiness(s).Capabilities.Fork || v.historyBusy[s.ID]).Chosen() {
		v.forkSession(s, "")
	}
	v.sessionReloadMenu(menu, s)
	if s.ForkedFrom != "" && menu.Item("查看来源会话").Chosen() {
		for _, origin := range v.Model.Snapshot.Sessions {
			if origin.ID == s.ForkedFrom {
				v.navigateSession(origin.ID, origin.WorkspaceID)
				break
			}
		}
	}
	menu.Separator()
	key := v.Model.WorkspaceConnection(s.WorkspaceID)
	ready := v.canManage(key)

	if v.canInspectSessionHooks(s) && menu.Item("会话 Hooks").Chosen() {
		v.openSessionHooks(s)
	}
	if menu.Item("重命名").Disabled(!ready).Chosen() {
		v.rename = &sessionRename{Session: s, Title: s.Title, Connection: key, Open: true}
	}
	if s.TitleOverride && s.SourceTitle != "" {
		if menu.Item("使用来源名称").Disabled(!ready).Chosen() {
			v.sessionChange("session.reset-title", s, "")
		}
	}
	if menu.Item("在右侧查看").Disabled(s.ID == v.splitRoot().Model.SessionID).Chosen() {
		v.openParallel(s.ID)
	}
	if s.ID == v.splitRoot().Model.SecondarySessionID && menu.Item("关闭并排会话").Chosen() {
		v.closeParallel()
	}
	menu.Separator()
	label := "归档会话"
	if s.Archived {
		label = "恢复会话"
	}
	if menu.Item(label).Disabled(!ready || v.pendingArchiveChange(s.WorkspaceID, s.ID) != nil).Chosen() {
		v.sessionChange("session.archive", s, "")
	}
	if !ready {
		menu.Separator()
		menu.Item("需要连接并更新执行服务").Disabled(true)
	}
}
func (v *View) editWorkspace(w domain.Workspace) {
	v.splitRoot().closeSchedulePage()
	if a := v.splitRoot().archive; a != nil {
		a.open = false
		a.viewing = ""
	}
	v.skillReturn = nil
	w.Folders = append([]string(nil), w.Folders...)
	main := ""
	if len(w.Folders) > 0 {
		main = w.Folders[0]
	}
	v.workspaceEditor = &workspaceEditor{Workspace: w, Main: main, Connection: v.Model.WorkspaceConnection(w.ID)}
	v.devices = false
}
func (v *View) archiveWorkspace(w domain.Workspace, archived bool) {
	v.changeArchive(w.ID, "", archived, w.Revision)
}
func (v *View) sessionChange(method string, s domain.Session, title string) {
	if method == "session.archive" {
		v.changeArchive(s.WorkspaceID, s.ID, !s.Archived, s.Revision)
		return
	}
	v.do(app.Job{ConnectionID: v.Model.WorkspaceConnection(s.WorkspaceID), Method: method, Input: protocol.SessionChangeInput{SessionID: s.ID, Revision: s.Revision, TitleRevision: s.TitleRevision, Title: title, Archived: !s.Archived}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if err != nil {
			m.Error = err.Error()
		}
	}})
}
func (v *View) managementDialogs(c *n.Context, t *n.Theme) {
	r := v.rename
	if r == nil {
		return
	}
	dialog := compactDialog(c.Key("rename-session:"+r.Session.ID), t, &r.Open, 380, func(panel n.Element) {
		if r.NaturalHeight > 0 {
			panel.Height(panel.AnimateTo("rename-height", r.NaturalHeight+compactDialogVerticalChrome, motionAnimation(c, 180*time.Millisecond)))
		}
		panel.Children(func() {
			content := compactDialogContent(c, "重命名会话", func() {
				n.Column(c).FillWidth().Gap(8).Children(func() {
					input := n.TextAreaBase(c.Key("title"), &r.Title).FillWidth().Lines(1, 4).MinHeight(32).Padding(6, configurationFieldInset).FontSize(13).FixedLineHeight(20).Radius(configurationFieldRadius).Background(t.Surface).Border(1, t.Border).Label("新的会话名称").AutoFocus().Disabled(r.Busy).OnTextKey(func(k n.TextKey) bool {
						if k.Key == n.KeyEnter && k.Mods == 0 {
							r.Title = k.Text()
							v.saveSessionName(r)
							return true
						}
						return false
					})
					fieldFeedback(c, input)
					if r.Error != "" {
						n.Text(c.Key("rename-error"), r.Error).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Transition(disclosureMotion(c, 160*time.Millisecond))
					}
				})
			}, func() {
				if r.Error != "" && compactDialogLeadingAction(c, "重新载入").Label("重新载入会话名称").Disabled(r.Busy).Clicked() {
					for _, current := range v.Model.Snapshot.Sessions {
						if current.ID == r.Session.ID {
							r.Session = current
							r.Title = current.Title
							r.Error = ""
							break
						}
					}
				}
				n.Spacer(c).Grow(1)
				if configurationAction(c, "取消").Label("取消重命名").Clicked() {
					r.Open = false
				}
				label := "保存"
				if r.Busy {
					label = "正在保存"
				}
				if compactDialogPrimary(c.Key("save-name"), label).MinWidth(78).Label("保存名称").Disabled(r.Busy || strings.TrimSpace(r.Title) == "" || !v.canManage(r.Connection)).Clicked() {
					v.saveSessionName(r)
				}
			})
			invalidate := c.Services().Invalidate
			content.DrawOver(func(_ *n.Painter, bounds n.Rect) {
				if content.Valid() && r.NaturalHeight != bounds.H {
					r.NaturalHeight = bounds.H
					invalidate()
				}
			})
		})
	})
	if dialog.Dismissed() {
		r.Open = false
	}
}
func (v *View) saveSessionName(r *sessionRename) {
	if r.Busy || !v.canManage(r.Connection) || strings.TrimSpace(r.Title) == "" {
		return
	}
	r.Busy = true
	r.attempt++
	attempt := r.attempt
	if !v.do(app.Job{ConnectionID: r.Connection, Method: "session.rename", Input: protocol.SessionChangeInput{SessionID: r.Session.ID, Revision: r.Session.Revision, TitleRevision: r.Session.TitleRevision, Title: strings.TrimSpace(r.Title)}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if v.rename != r || !r.Open || !r.Busy || attempt != r.attempt {
			return
		}
		r.Busy = false
		if err != nil {
			r.Error = err.Error()
			return
		}
		r.Open = false
	}}) {
		r.Busy = false
		r.Error = "操作繁忙，请重试"
	}
}
func (v *View) workspaceSettingsView(c *n.Context, t *n.Theme) {
	e := v.workspaceEditor
	if e == nil {
		return
	}
	n.Column(c).FillWidth().FillHeight().Grow(1).Padding(0, configurationInset, 20).Children(func() {
		if s := e.HookSession; s.ID != "" {
			v.managedHooksView(c, t, v.hookManagementFor(e.Connection, s.HarnessID, e.Workspace.ID, s.ID))
			return
		}
		n.Column(c).FillWidth().FillHeight().Grow(1).Children(func() {
			tabs := []configurationTabItem{{ID: "", Label: "常规"}, {ID: "Skills", Label: "Skills"}, {ID: "MCP", Label: "MCP"}, {ID: "Hooks", Label: "Hooks"}}
			e.Tab = configurationTabs(c.Key("workspace-tabs"), e.Tab, tabs, configurationUnderline, e.Busy)
			index := configurationTabIndex(tabs, e.Tab)
			n.Column(c).AlignSelf(n.Stretch).MarginEnd(-workspaceDirectoryScrollInset).
				FillHeight().Grow(1).Padding(12, 0, 0).Children(func() {
				configurationTabPanel(c.Key("workspace-tab-panel"), e.Tab, index, configurationPanelFill, func() {
					if index == 0 {
						v.workspaceSettingsForm(c, t)
						return
					}
					n.Column(c).Fill().Padding(0, workspaceDirectoryScrollInset, 0, 0).Children(func() {
						switch e.Tab {
						case "Skills":
							v.workspaceSkills(c, t, e)
						case "Hooks":
							v.workspaceHooks(c, t, e)
						case "MCP":
							v.workspaceToolsView(c, t, e)
						}
					})
				}, false)
			})
		})
	})
}

func (v *View) workspaceSettingsForm(c *n.Context, t *n.Theme) {
	e := v.workspaceEditor
	if e == nil {
		return
	}
	rowMotion := disclosureMotion(c, 180*time.Millisecond)
	rowMotion.Position = true
	positionMotion := n.ElementTransition{Position: true, Duration: motionAnimation(c, 180*time.Millisecond).Duration, Ease: n.EaseOut}
	n.Scroll(c.Key("workspace-settings:" + e.Workspace.ID)).Fill().Grow(1).TrackScroll(&e.GeneralScroll).Children(func() {
		n.Column(c).FillWidth().Padding(8, workspaceDirectoryScrollInset, 8, 0).AlignItems(n.Start).Children(func() {
			n.Column(c).FillWidth().Gap(20).Children(func() {
				n.Column(c).FillWidth().Gap(6).Children(func() {
					n.Text(c, "名称").FontSize(12).TextColor(t.TextMuted)
					configurationTextField(c.Key("workspace-name"), &e.Workspace.Name).Height(32).FontSize(13).Label("工作区名称").Disabled(e.Busy)
				})
				n.Column(c).FillWidth().Gap(8).Children(func() {
					n.Text(c, "目录").FontSize(13)
					for _, folder := range append([]string(nil), e.Workspace.Folders...) {
						n.Row(c.Key(folder)).FillWidth().MinHeight(32).Gap(8).AlignItems(n.Center).Transition(rowMotion).Children(func() {
							n.Radio(c, &e.Main, folder, "").Size(18, 20).MarginStart(workspaceDirectoryChoiceInset).MarginEnd(-workspaceDirectoryChoiceInset).Label("设为主目录：" + folder).Disabled(e.Busy)
							n.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset(folder, n.Font{Family: t.Font, Size: 12, Weight: 400}))).Children(func() {
								n.Text(c, folder).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).FontSize(12).TooltipWhenTruncated(folder)
								if folder == e.Main {
									n.Text(c.Key("main-folder"), "主目录").FontSize(11).TextColor(t.TextMuted).Transition(fadeMotion(c, 160*time.Millisecond))
								}
							})
							if iconButton(c, closeIcon, "移除目录："+folder).MarginEnd(-workspaceDirectoryActionOutset).
								Disabled(e.Busy || len(e.Workspace.Folders) == 1).Clicked() {
								folders := []string{}
								for _, p := range e.Workspace.Folders {
									if p != folder {
										folders = append(folders, p)
									}
								}
								e.Workspace.Folders = folders
								if e.Main == folder {
									e.Main = folders[0]
								}
							}
						})
					}
					n.Row(c.Key("add-directory")).FillWidth().Justify(n.End).Transition(positionMotion).Children(func() {
						if iconButton(c, plus, "添加目录").MarginEnd(-workspaceDirectoryActionOutset).
							Tooltip("添加文件夹").Disabled(e.Busy).Clicked() {
							v.chooseWorkspaceDirectory(e.Connection, "选择附加目录", e.Main, true, func() bool { return v.workspaceEditor == e && !e.Busy }, func(paths []string) { v.addWorkspaceFolders(e, paths) }, func(err error) { e.Error = err.Error() })
						}
					})
				})
				if e.Error != "" {
					n.Text(c.Key("workspace-error"), e.Error).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Transition(rowMotion)
				}
				n.Row(c.Key("workspace-actions")).FillWidth().Gap(8).Transition(positionMotion).Children(func() {
					if ghost(c, "取消").Label("取消工作区编辑").Clicked() {
						v.workspaceEditor = nil
					}
					if e.Error != "" && ghost(c, "重新载入").Disabled(e.Busy).Clicked() {
						for _, w := range v.Model.Snapshot.Workspaces {
							if w.ID == e.Workspace.ID {
								v.editWorkspace(w)
								break
							}
						}
					}
					n.Spacer(c).Grow(1)
					if n.PrimaryButton(c, "保存").Label("保存工作区").Height(28).FontSize(12).Radius(6).Disabled(e.Busy || strings.TrimSpace(e.Workspace.Name) == "" || !v.canManage(e.Connection)).Clicked() {
						v.saveWorkspace(e)
					}
				})
			})
		})
	})
}
func (v *View) addWorkspaceFolders(e *workspaceEditor, folders []string) {
	for _, folder := range folders {
		found := false
		for _, existing := range e.Workspace.Folders {
			found = found || existing == folder
		}
		if !found {
			e.Workspace.Folders = append(e.Workspace.Folders, folder)
		}
	}
}
func (v *View) saveWorkspace(e *workspaceEditor) {
	if e.Busy || !v.canManage(e.Connection) {
		return
	}
	w := e.Workspace
	w.Name = strings.TrimSpace(w.Name)
	w.Folders = []string{e.Main}
	for _, folder := range e.Workspace.Folders {
		if folder != e.Main {
			w.Folders = append(w.Folders, folder)
		}
	}
	e.Busy = true
	e.Error = ""
	if !v.do(app.Job{ConnectionID: e.Connection, Method: "workspace.update", Input: w, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		e.Busy = false
		if v.workspaceEditor != e {
			return
		}
		if err != nil {
			e.Error = err.Error()
			return
		}
		v.workspaceEditor = nil
	}}) {
		e.Busy = false
		e.Error = "操作繁忙，请重试"
	}
}
