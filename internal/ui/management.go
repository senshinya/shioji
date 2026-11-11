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
	Main, Connection, Error string
	Busy                    bool
}
type sessionRename struct {
	Session                  domain.Session
	Title, Connection, Error string
	Open, Busy               bool
	NaturalHeight            float32
}

var moreActionsIcon = svg("M4 10h.1M10 10h.1M16 10h.1")

type navVisibility struct {
	From, To float32
	Started  time.Time
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
	sample := func() float32 {
		p := min(1, float32(c.Now().Sub(state.Started))/float32(180*time.Millisecond))
		return state.From + (state.To-state.From)*n.EaseOut(p)
	}
	value := sample()
	if target != state.To {
		state.From, state.To, state.Started = value, target, c.Now()
	}
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
		panel.Width(280).Padding(3).Radius(9).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
			n.Scroll(c).FillWidth().MaxHeight(320).Children(func() {
				n.Column(c).FillWidth().Gap(0).Children(func() {
					for i, folder := range workspace.Folders {
						n.Row(c.Key(folder)).FillWidth().Height(26).PaddingX(7).Gap(7).AlignItems(n.Baseline).Tooltip(folder).Children(func() {
							font := n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}
							inlineSymbol(c, workspaceIcon, folder, font, 14, t.TextMuted)
							n.Text(c, path.Base(folder)).SingleLine().FontSize(13).FixedLineHeight(20).Grow(1)
							if i == 0 {
								n.Text(c, "主目录").FontSize(11).TextColor(t.TextMuted)
							}
						})
					}
					if len(workspace.Folders) > 0 {
						n.Column(c).Height(1).FillWidth().Background(t.Border).MarginY(3)
					}
					if ghost(c, "工作区设置").FillWidth().Height(26).FontSize(13).PaddingX(7).Justify(n.Start).Disabled(workspace.ID == "").Clicked() {
						v.editWorkspace(workspace)
						open = false
					}
					key := v.Model.WorkspaceConnection(workspace.ID)
					entry := control(c, 4).FillWidth().Height(26).PaddingX(7).Justify(n.Start).Label("导入会话…").Disabled(workspace.ID == "" || workspace.Archived || !v.canImportConversations(key)).Children(func() {
						inlineIconText(c, conversationImportIcon, "导入会话…", n.Font{Family: t.Font, Size: 13}, 14, 7, t.Text)
					})
					if entry.Clicked() {
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
	if menu.Item(label).Disabled(!ready).Chosen() {
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
	if menu.Item(label).Disabled(!ready).Chosen() {
		v.sessionChange("session.archive", s, "")
	}
	if !ready {
		menu.Separator()
		menu.Item("需要连接并更新执行服务").Disabled(true)
	}
}
func (v *View) editWorkspace(w domain.Workspace) {
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
	v.do(app.Job{ConnectionID: v.Model.WorkspaceConnection(w.ID), Method: "workspace.archive", Input: protocol.WorkspaceArchiveInput{WorkspaceID: w.ID, Revision: w.Revision, Archived: archived}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if err != nil {
			m.Error = err.Error()
		}
	}})
}
func (v *View) sessionChange(method string, s domain.Session, title string) {
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
	dialog := n.DialogBase(c.Key("rename-session:"+r.Session.ID), &r.Open, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .16)).Transition(fadeMotion(c, 180*time.Millisecond))
		width, _ := c.Size()
		panel.Width(min(380, width-48)).Padding(16).Radius(10).Clip().Background(raised(t)).Border(1, t.Border).Shadow(0, 8, 28, 0, n.RGBA(0, 0, 0, .16)).Transition(popupMotion(c))
		if r.NaturalHeight > 0 {
			panel.Height(panel.AnimateTo("rename-height", r.NaturalHeight+34, motionAnimation(c, 180*time.Millisecond)))
		}
		panel.Children(func() {
			content := n.Column(c.Key("rename-content")).FillWidth().Shrink(0).Gap(12)
			invalidate := c.Services().Invalidate
			content.DrawOver(func(_ *n.Painter, bounds n.Rect) {
				if content.Valid() && r.NaturalHeight != bounds.H {
					r.NaturalHeight = bounds.H
					invalidate()
				}
			})
			content.Children(func() {
				n.Text(c, "重命名会话").FontSize(14).FontWeight(500)
				input := n.TextAreaBase(c.Key("title"), &r.Title).FillWidth().Lines(1, 4).MinHeight(32).Padding(6, 8).FontSize(13).FixedLineHeight(20).Radius(6).Background(t.Surface).Border(1, t.Border).Label("新的会话名称").AutoFocus().Disabled(r.Busy).OnTextKey(func(k n.TextKey) bool {
					if k.Key == n.KeyEnter && k.Mods == 0 {
						r.Title = k.Text()
						v.saveSessionName(r)
						return true
					}
					return false
				})
				fieldFeedback(c, input)
				if r.Error != "" {
					n.Text(c.Key("rename-error"), r.Error).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Transition(disclosureMotion(c, 160*time.Millisecond))
				}
				n.Row(c).FillWidth().Gap(8).Children(func() {
					if ghost(c, "取消").Clicked() {
						r.Open = false
					}
					if r.Error != "" && ghost(c, "重新载入").Label("重新载入会话名称").Disabled(r.Busy).Clicked() {
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
					if n.PrimaryButton(c, "保存").Label("保存名称").Height(28).FontSize(12).Radius(6).Disabled(r.Busy || strings.TrimSpace(r.Title) == "" || !v.canManage(r.Connection)).Clicked() {
						v.saveSessionName(r)
					}
				})
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
	if !v.do(app.Job{ConnectionID: r.Connection, Method: "session.rename", Input: protocol.SessionChangeInput{SessionID: r.Session.ID, Revision: r.Session.Revision, TitleRevision: r.Session.TitleRevision, Title: strings.TrimSpace(r.Title)}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		r.Busy = false
		if v.rename != r || !r.Open {
			return
		}
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
	n.Column(c).FillWidth().FillHeight().Grow(1).Padding(0, 32, 20).Children(func() {
		n.Column(c).FillWidth().MaxWidth(840).FillHeight().Grow(1).Children(func() {
			tabs := []configurationTabItem{{ID: "", Label: "常规"}, {ID: "Skills", Label: "Skills"}, {ID: "MCP", Label: "MCP"}}
			e.Tab = configurationTabs(c.Key("workspace-tabs"), e.Tab, tabs, configurationUnderline, e.Busy)
			n.Column(c).FillWidth().FillHeight().Grow(1).Padding(12, 0, 0).Children(func() {
				configurationTabPanel(c.Key("workspace-tab-panel"), e.Tab, configurationTabIndex(tabs, e.Tab), configurationPanelFill, func() {
					if e.Tab == "Skills" {
						v.workspaceSkills(c, t, e)
					} else if e.Tab == "MCP" {
						v.workspaceToolsView(c, t, e)
					} else {
						v.workspaceSettingsForm(c, t)
					}
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
	n.Scroll(c.Key("workspace-settings:" + e.Workspace.ID)).FillWidth().FillHeight().Grow(1).TrackScroll(&e.GeneralScroll).Children(func() {
		n.Column(c).FillWidth().Padding(8, 0).AlignItems(n.Start).Children(func() {
			n.Column(c).FillWidth().MaxWidth(840).Gap(20).Children(func() {
				n.Column(c).FillWidth().Gap(6).Children(func() {
					n.Text(c, "名称").FontSize(12).TextColor(t.TextMuted)
					textField(c.Key("workspace-name"), &e.Workspace.Name).FillWidth().Height(32).FontSize(13).Label("工作区名称").Disabled(e.Busy)
				})
				n.Column(c).FillWidth().Gap(8).Children(func() {
					n.Text(c, "目录").FontSize(13)
					for _, folder := range append([]string(nil), e.Workspace.Folders...) {
						n.Row(c.Key(folder)).FillWidth().MinHeight(32).Gap(8).AlignItems(n.Center).Transition(rowMotion).Children(func() {
							n.Radio(c, &e.Main, folder, "").Size(18, 20).Label("设为主目录：" + folder).Disabled(e.Busy)
							n.Text(c, folder).SingleLine().Grow(1).FontSize(12).Tooltip(folder)
							if folder == e.Main {
								n.Text(c.Key("main-folder"), "主目录").FontSize(11).TextColor(t.TextMuted).Transition(fadeMotion(c, 160*time.Millisecond))
							}
							if iconButton(c, closeIcon, "移除目录："+folder).Size(24, 24).Disabled(e.Busy || len(e.Workspace.Folders) == 1).Clicked() {
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
						if iconButton(c, plus, "添加目录").Tooltip("添加文件夹").Disabled(e.Busy).Clicked() {
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
func (v *View) archivedNotice(c *n.Context, t *n.Theme) bool {
	kind := v.Model.ArchivedContext()
	if kind == "" {
		return false
	}
	n.Row(c.Key("archived-notice")).FillWidth().MaxWidth(768).Gap(8).AlignItems(n.Center).Transition(disclosureMotion(c, 180*time.Millisecond)).Children(func() {
		n.Text(c, kind+"已归档").FontSize(12).TextColor(t.TextMuted)
		if ghost(c, "恢复"+kind).FontSize(12).Disabled(!v.canManage(v.Model.ConnectionID())).Clicked() {
			if kind == "工作区" {
				for _, w := range v.Model.Snapshot.Workspaces {
					if w.ID == v.Model.WorkspaceID {
						v.archiveWorkspace(w, false)
						break
					}
				}
			} else {
				for _, s := range v.Model.Snapshot.Sessions {
					if s.ID == v.Model.SessionID {
						v.sessionChange("session.archive", s, "")
						break
					}
				}
			}
		}
	})
	return true
}
