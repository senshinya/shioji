// Package ui renders Shioji with MyGo's native components; no WebView is used.
package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type editBinding struct {
	Text      string
	LastModel string
}

type View struct {
	recoveries                                                                                                      map[string]*recoveryState
	paneFocusVersion                                                                                                uint64
	outline                                                                                                         *conversationOutline
	parent, secondary                                                                                               *View
	visibleSessions                                                                                                 map[string]string
	attachmentStore                                                                                                 *attachments.Repository
	attachmentImages                                                                                                map[string]*attachmentImage
	attachmentUploading                                                                                             map[string]bool
	attachmentPreparing                                                                                             map[string]int
	attachmentPreview                                                                                               *attachmentPreview
	navVisibility                                                                                                   map[string]*navVisibility
	workspaceEditor                                                                                                 *workspaceEditor
	rename                                                                                                          *sessionRename
	showArchived                                                                                                    bool
	currentTranscriptRow                                                                                            int
	activityMarkdown                                                                                                markdown.Options
	secretAnswers                                                                                                   map[string]string
	secretBindings                                                                                                  map[string]*editBinding
	activityPatches                                                                                                 map[string]*activityPatch
	replyStreams                                                                                                    map[string]*replyStream
	replySession                                                                                                    string
	replyEpoch                                                                                                      string
	replyFrame                                                                                                      uint64
	replyLatestOrder                                                                                                int64
	toolMotions                                                                                                     map[string]*toolDisclosureMotion
	frameTime                                                                                                       time.Time
	toolsAnimating                                                                                                  bool
	sent                                                                                                            *sentMessageMotion
	sessionEntry                                                                                                    *sessionEntryMotion
	composerHadFocus                                                                                                bool
	focusComposer                                                                                                   bool
	queueBusy                                                                                                       map[string]bool
	queueDrag                                                                                                       *queuedRowDrag
	queueSources                                                                                                    map[string]queuedMessageSource
	queueSourceSession                                                                                              string
	queueSourceEpoch                                                                                                string
	queueArrivals                                                                                                   map[string]*queuedArrivalMotion
	queueDisplay                                                                                                    domain.QueueState
	composerHeightKey                                                                                               string
	composerInputHeight                                                                                             float32
	composerNaturalHeight                                                                                           float32
	composerEditID                                                                                                  string
	composerHeightUntil                                                                                             time.Time
	inputBounds                                                                                                     n.Rect
	reduceMotion                                                                                                    bool
	draftBindings                                                                                                   map[string]*editBinding
	answerBindings                                                                                                  map[string]*editBinding
	customBindings                                                                                                  map[string]*editBinding
	Saver                                                                                                           *app.StateWriter
	lastQueued                                                                                                      []byte
	lastState                                                                                                       []byte
	Model                                                                                                           *app.Model
	Controller                                                                                                      *app.Controller
	StatePath                                                                                                       string
	lists                                                                                                           map[string]*n.ListState
	nav                                                                                                             n.ListState
	md                                                                                                              markdown.Options
	runOpen, permissionOpen                                                                                         bool
	runPage                                                                                                         string
	devices                                                                                                         bool
	selected                                                                                                        string
	selectedDevice                                                                                                  string
	deviceSelected                                                                                                  string
	deviceList                                                                                                      bool
	deviceSearch                                                                                                    string
	sshOpen                                                                                                         bool
	sshName, sshTarget, sshPort, sshError, sshRequest                                                               string
	checkingSSH                                                                                                     bool
	remoteWorkspaceOpen                                                                                             bool
	remoteWorkspaceConnection, remoteWorkspaceName, remoteWorkspacePath, remoteWorkspaceExtra, remoteWorkspaceError string
	remoteWorkspaceRequest                                                                                          string
	remoteWorkspaceBusy                                                                                             bool
	collapsedWorkspaces                                                                                             map[string]bool
	dockHeight                                                                                                      float32
	searchOpen                                                                                                      bool
	searchText, searchSent                                                                                          string
	searchResults                                                                                                   []domain.SearchResult
	searchList                                                                                                      n.ListState
	branchOpen                                                                                                      bool
	branchSearch                                                                                                    string
	branchList                                                                                                      n.ListState
	switchingBranch                                                                                                 bool
	gitLoaded                                                                                                       map[string]string
	gitPending                                                                                                      map[string]bool
	resizingInspector                                                                                               bool
	changes                                                                                                         map[string]*changesState
	menuOpen                                                                                                        map[string]bool
	menuContext                                                                                                     string
}

func New(model *app.Model, controller *app.Controller, path string) *View {
	v := &View{draftBindings: map[string]*editBinding{}, answerBindings: map[string]*editBinding{}, customBindings: map[string]*editBinding{}, Model: model, Controller: controller, StatePath: path, lists: map[string]*n.ListState{}, collapsedWorkspaces: map[string]bool{}, md: markdown.Options{ShareSelection: true, WrapCode: true}}
	if path != "" {
		v.Saver = app.NewStateWriter(path)
	}
	return v
}
func (v *View) collectBindings() {
	for key, b := range v.draftBindings {
		if b.Text != b.LastModel {
			v.Model.Drafts[key] = b.Text
			b.LastModel = b.Text
		}
	}
	for key, b := range v.answerBindings {
		if b.Text != b.LastModel {
			v.Model.Answers[key] = b.Text
			b.LastModel = b.Text
		}
	}
	for key, b := range v.customBindings {
		if b.Text != b.LastModel {
			v.Model.Answers["custom:"+key] = b.Text
			v.Model.Answers[key] = b.Text
			b.LastModel = b.Text
		}
	}

}
func (v *View) Flush() error {
	root := v.splitRoot()
	root.collectBindings()
	if root.secondary != nil {
		root.secondary.collectBindings()
	}
	if root.Saver == nil {
		return nil
	}
	data, err := root.Model.EncodeState()
	if err != nil {
		return err
	}
	return root.Saver.Flush(data)
}

func svg(path string) *n.SVG {
	return n.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><path d="` + path + `" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
}

var plus = svg("M10 4v12M4 10h12")
var chevron = svg("M6 8l4 4 4-4")
var monitor = svg("M3 3h14v10H3zM7 17h6M10 13v4")
var check = svg("M4 10l4 4 8-9")
var attention = svg("M10 3l8 14H2zM10 7v4M10 14v.2")
var arrow = n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M12 21V3M4 11l8-8 8 8" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
var stop = n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><rect x="6" y="5" width="12" height="14" rx="2" fill="currentColor"/></svg>`))
var terminalIcon = svg("M4 6l4 4-4 4M11 14h5")
var questionIcon = svg("M17.5 13.3h-10l-5 4.2v-15h15z")

func (v *View) View(c *n.Context) {
	m := v.Model
	v.frameTime, v.reduceMotion = c.Now(), c.Preferences().ReduceMotion
	if v.Controller != nil {
		v.Controller.SetInvalidate(c.Services().Invalidate)
		for {
			select {
			case update := <-v.Controller.Updates:
				update(m)
			default:
				goto drained
			}
		}
	}
drained:
	m.RefreshDevices()
	v.syncQueuedMotions()
	v.syncSendMotion()
	v.syncSessionEntry()
	if v.selected != m.SessionID || v.selectedDevice != m.ConnectionID() {
		v.selected = m.SessionID
		v.selectedDevice = m.ConnectionID()
		m.FoldToolHistory[m.SessionID] = true
		if v.Controller != nil {
			v.Controller.SelectDevice(m.ConnectionID(), m.SessionID)
		}
	}
	t := *c.Theme()
	t.FontSize = 13
	t.Font = "system-ui"
	t.FontAntialiased = true
	t.Spacing = 3
	t.Radius = 6
	if t.Dark {
		t.Background = n.Hex("#101010")
		t.Surface = n.Hex("#202020")
		t.SurfaceHover = n.Hex("#292929")
		t.SurfacePressed = n.Hex("#292929")
		t.Border = n.Hex("#343434")
		t.Text = n.Hex("#e9e9e9")
		t.TextMuted = n.Hex("#ababab")
		t.Accent = n.Hex("#e5e5e5")
		t.AccentText = n.Hex("#181818")
		t.Selection = n.Hex("#292929")
		t.Focus = n.Hex("#8c8c90")
		t.Success = n.Hex("#32d769")
		t.Warning = n.Hex("#ff9f0a")
		t.Danger = n.Hex("#ff6257")
	} else {
		t.Background = n.Hex("#ffffff")
		t.Surface = n.Hex("#f4f4f4")
		t.SurfaceHover = n.Hex("#eeeeee")
		t.SurfacePressed = n.Hex("#e7e7e7")
		t.Border = n.Hex("#e3e3e3")
		t.Text = n.Hex("#1c1c1c")
		t.TextMuted = n.Hex("#666666")
		t.Accent = n.Hex("#262626")
		t.AccentText = n.Hex("#ffffff")
		t.Selection = n.Hex("#e7e7e7")
		t.Focus = n.Hex("#97979b")
		t.Success = n.Hex("#138a43")
		t.Warning = n.Hex("#c66a00")
		t.Danger = n.Hex("#d92d20")
	}
	t.AccentHover = t.Accent
	t.AccentPressed = t.Accent
	c.SetTheme(&t)
	// Keep the overlay parent alive so popup entrance and exit both have a
	// persistent parent, even when no popup is currently open.
	n.Overlay(c, func() {})
	v.md.Theme = &markdown.Theme{FontSize: 14, LineHeight: 24.0 / 14, BlockSpacing: 8.0 / 14}
	if v.Saver != nil {
		v.Saver.SetInvalidate(c.Services().Invalidate)
		saved, err := v.Saver.Result()
		v.lastState = saved
		if err != nil {
			m.Error = "无法保存草稿：" + err.Error()
		}
		defer func() {
			data, err := m.EncodeState()
			if err == nil && !bytes.Equal(data, v.lastQueued) {
				v.Saver.Queue(data)
				v.lastQueued = data
			}
		}()
	}

	menuContext := fmt.Sprintf("%s/%s/%t", m.DraftKey(), m.WorkspaceID, v.devices)
	if menuContext != v.menuContext {
		clear(v.menuOpen)
		v.menuContext = menuContext
	}
	v.prepareSecondary()
	v.requestGit(false)
	if c.Shortcut(n.Cmd, n.KeyK) {
		v.searchOpen = !v.searchOpen
	}
	if c.Shortcut(n.Cmd, n.KeyN) {
		v.home()
	}
	if m.SessionID != "" && !v.devices {
		v.syncReplyStreams(m.Details[m.SessionID])
	}
	owner := v
	n.Row(c).Fill().Background(t.Background).Children(func() {
		v.sidebar(c, &t)
		width, _ := c.Size()
		owner = v.focusedView()
		open := owner.Model.SessionID != "" && !v.devices && v.workspaceEditor == nil && m.Inspectors[owner.Model.SessionID] && (!m.FocusSecondary || m.SecondarySessionID == owner.Model.SessionID)
		inline := width > 1160
		if !open {
			v.resizingInspector = false
		}
		panelWidth := v.Model.InspectorWidth
		if panelWidth == 0 {
			panelWidth = 280
		}
		limit := width - 256 - 360
		if !inline {
			navigation := float32(236)
			if width <= 980 {
				navigation = 224
			}
			limit = width - navigation - 16
		}
		panelWidth = min(max(panelWidth, 280), min(limit, 960))
		conversation := n.Column(c.Key("conversation-pane")).FillHeight().Grow(1)
		if !v.resizingInspector {
			conversation.Transition(n.ElementTransition{Duration: motionAnimation(c, 200*time.Millisecond).Duration, Ease: n.EaseOut, Size: true})
		}
		conversation.Children(func() {
			if v.devices {
				v.toolbar(c, &t)
				v.deviceView(c, &t)
			} else if v.workspaceEditor != nil {
				v.toolbar(c, &t)
				v.workspaceSettingsView(c, &t)
			} else if m.SessionID == "" {
				v.toolbar(c, &t)
				v.homeView(c, &t)
			} else {
				v.parallelConversation(c, &t)
			}
		})
		if open {
			if inline {
				column := n.Column(c.Key("review-column")).FillHeight().Shrink(0).Clip()
				if !v.resizingInspector {
					column.Transition(columnMotion(c, panelWidth))
				}
				column.Children(func() { owner.inspector(c, &t, false, panelWidth, limit) })
			} else {
				owner.inspector(c, &t, true, panelWidth, limit)
			}
		}
	})
	if v.selected != m.SessionID || v.selectedDevice != m.ConnectionID() {
		v.selected = m.SessionID
		v.selectedDevice = m.ConnectionID()
		m.FoldToolHistory[m.SessionID] = true
		if v.Controller != nil {
			v.Controller.SelectDevice(m.ConnectionID(), m.SessionID)
		}
		c.After(0)
	}
	if owner != v {
		m.InspectorWidth = owner.Model.InspectorWidth
		m.InspectorLayout = owner.Model.InspectorLayout
		m.InspectorSplitHeight = owner.Model.InspectorSplitHeight
	}
	v.updateVisibleSessions()
	v.pruneHistoryCaches()
	v.deviceDialogs(c, &t)
	v.managementDialogs(c, &t)
	v.attachmentDialog(c, &t)
}

func (v *View) home() {
	v.workspaceEditor = nil
	v.Model.SessionID = ""
	v.Model.FocusSecondary = false
	v.devices = false
	v.runOpen = false
	v.permissionOpen = false
	v.branchOpen = false
	v.searchOpen = false
}
func iconButton(c *n.Context, icon *n.SVG, label string) n.Element {
	return control(c).Size(28, 28).Radius(6).Label(label).Tooltip(label).TextColor(c.Theme().TextMuted).Children(func() { n.Icon(c, icon).Size(16, 16) })
}
func ghost(c *n.Context, label string, radius ...float32) n.Element {
	r := c.Theme().Radius
	if len(radius) > 0 {
		r = radius[0]
	}
	return controlFeedback(c, n.Button(c, label).Radius(r), r).FontWeight(400).Justify(n.Start).Background(n.Transparent).Border(0, n.Transparent).Height(28)
}
func (v *View) sidebar(c *n.Context, t *n.Theme) {
	m := v.Model
	width, _ := c.Size()
	sidebarWidth := float32(256)
	if width <= 980 {
		sidebarWidth = 224
	} else if width <= 1160 {
		sidebarWidth = 236
	}
	sidebarColor := t.Surface
	if t.Dark {
		sidebarColor = n.Hex("#161616")
	}
	n.Column(c).Width(sidebarWidth).FillHeight().Padding(8, 0, 8, 8).Children(func() {
		n.Column(c).Fill().Padding(8).Gap(4).Radius(14).Background(sidebarColor).Border(1, t.Border).Children(func() {
			n.Row(c).Height(26).AlignItems(n.Start).DragWindow().Children(func() {
				n.Spacer(c).Grow(1)
				v.searchControl(c, t)
			})
			if navButton(c, plus, "新建会话").Background(func() n.Color {
				if m.SessionID == "" && !v.devices {
					return t.SurfacePressed
				}
				return n.Transparent
			}()).Clicked() {
				v.home()
			}
			if navButton(c, attentionIcon, "待我处理").Clicked() {
				v.nextAttention()
			}
			n.Row(c).Height(32).Padding(8, 2, 0, 8).AlignItems(n.Center).Children(func() {
				n.Text(c, "工作区").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).Grow(1)
				iconButton(c, moreActionsIcon, "工作区菜单").Size(24, 24).Menu(func(menu *n.Menu) {
					if menu.Item("显示已归档").Checked(v.showArchived).Chosen() {
						v.showArchived = !v.showArchived
					}
				})
				if iconButton(c, plus, "创建工作区").Size(24, 24).Disabled(!m.Connected).Clicked() {
					v.createWorkspace()
				}
			})
			type navRow struct {
				presence  float32
				workspace *domain.Workspace
				session   *domain.Session
			}
			rows := []navRow{}
			for i := range m.Snapshot.Workspaces {
				w := &m.Snapshot.Workspaces[i]
				presence := v.navigationPresence(c, "workspace:"+w.ID, !w.Archived || v.showArchived)
				children := []navRow{}
				for j := range m.Snapshot.Sessions {
					s := &m.Snapshot.Sessions[j]
					if s.WorkspaceID == w.ID {
						childPresence := v.navigationPresence(c, "session:"+s.ID, (!w.Archived && !s.Archived) || v.showArchived)
						if childPresence > 0 {
							children = append(children, navRow{session: s, presence: min(presence, childPresence)})
						}
					}
				}
				if presence <= 0 {
					continue
				}
				rows = append(rows, navRow{workspace: w, presence: presence})
				if !v.collapsedWorkspaces[w.ID] {
					rows = append(rows, children...)
				}
			}
			v.nav.Key = func(i int) any {
				if rows[i].workspace != nil {
					return "w:" + rows[i].workspace.ID
				}
				return "s:" + rows[i].session.ID
			}
			v.nav.Reveals = v.nav.Reveals[:0]
			for i, r := range rows {
				progress := r.presence
				if r.session != nil {
					progress = min(progress, v.sessionEntryProgress(c, r.session.ID, false))
				}
				if progress < 1 {
					v.nav.Reveals = append(v.nav.Reveals, n.ListReveal{First: i, End: i + 1, Progress: progress})
				}
			}
			n.List(c, &v.nav, len(rows), func(i int) {
				r := rows[i]
				if r.workspace != nil {
					w := r.workspace
					workspaceRow := n.Row(c).Height(30).Gap(3).AlignItems(n.Center).Opacity(r.presence)
					workspaceRow.Children(func() {
						if control(c.Key("workspace:" + w.ID)).Height(24).Grow(1).Justify(n.Start).Label(w.Name).Children(func() {
							n.Row(c).FillWidth().Gap(3).AlignItems(n.Baseline).Children(func() {
								angle := float32(0)
								if v.collapsedWorkspaces[w.ID] {
									angle = -90
								}
								inlineSymbol(c, disclosureDown, w.Name, n.Font{Family: "system-ui", Size: 12, Weight: 500}, 11, t.TextMuted, angle).Width(20).TextAlign(n.Center)
								n.Text(c, w.Name).SingleLine().Grow(1).FontSize(12).FontWeight(500).FixedLineHeight(20).Tooltip(w.Name)
							})
						}).ContextMenu(func(menu *n.Menu) { v.workspaceMenu(menu, *w) }).Clicked() {
							v.collapsedWorkspaces[w.ID] = !v.collapsedWorkspaces[w.ID]
						}
						n.Text(c, m.DeviceName(w.DeviceID)).FontSize(11).TextColor(t.TextMuted).PaddingX(6)
					})
					return
				}
				s := r.session
				selected := s.ID == v.focusedSessionID() && !v.devices && v.workspaceEditor == nil
				finishingOutput := s.Status == "completed" && v.sessionOutputAnimating(s.ID)
				if finishingOutput {
					c.AnimationFrame()
				}
				row := control(c, 8).FillWidth().Height(28).MarginY(1).Padding(4, 8, 4, 20).Radius(8).Label(s.Title)
				if progress := min(r.presence, v.sessionEntryProgress(c, s.ID, false)); progress < 1 {
					row.Opacity(progress).Transform(n.Translation(-6*(1-progress), 0))
				}
				if selected {
					row.Background(t.SurfacePressed)
				}
				row.ContextMenu(func(menu *n.Menu) { v.sessionMenu(menu, *s) })
				row.Children(func() {
					n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
						if v.pendingCount(s.ID) > 0 {
							n.Column(c).Width(10).AlignItems(n.Center).AlignSelf(n.Center).Children(func() { n.Column(c).Size(4, 4).Radius(2).Background(t.Warning) })
						} else if s.Status == "running" || s.Status == "starting" || finishingOutput {
							n.Column(c).Width(10).AlignItems(n.Center).AlignSelf(n.Center).Children(func() { n.Column(c).Size(4, 4).Radius(2).Background(t.Success) })
						} else if s.Status == "completed" {
							inlineSymbol(c, check, s.Title, n.Font{Family: "system-ui", Size: 13, Weight: 400}, 10, t.Success)
						} else if s.Status == "interrupted" || s.Status == "abandoned" {
							inlineSymbol(c, check, s.Title, n.Font{Family: "system-ui", Size: 13, Weight: 400}, 10, t.TextMuted).Label("已停止")
						} else {
							glyph := "Ⅱ"
							label := "已暂停"
							if s.Status == "failed" {
								glyph = "×"
								label = "执行失败"
							}
							if s.Status == "unknown" {
								glyph = "?"
								label = "执行状态待核实"
							}
							n.Text(c, glyph).Width(10).FontSize(11).TextColor(t.TextMuted).Label(label)
						}
						n.Text(c, s.Title).SingleLine().Grow(1).FontSize(13).FixedLineHeight(20).TextColor(func() n.Color {
							if selected {
								return t.Text
							}
							return t.TextMuted
						}())
						if count := v.pendingCount(s.ID); count > 0 {
							n.Text(c, fmt.Sprint(count)).Font("Menlo").FontSize(11).Width(20).TextAlign(n.End)
						}
					})
				})
				if v.canManage(m.WorkspaceConnection(s.WorkspaceID)) && (row.DoubleClicked() || row.Focused() && c.Shortcut(0, n.KeyF2)) {
					v.rename = &sessionRename{Session: *s, Title: s.Title, Connection: m.WorkspaceConnection(s.WorkspaceID), Open: true}
				}
				if row.Clicked() {
					v.workspaceEditor = nil
					v.navigateSession(s.ID, s.WorkspaceID)
					v.devices = false
				}
			}).Grow(1).FillHeight().Label("工作区与会话")
			n.Column(c).Height(1).FillWidth().Background(t.Border)
			n.Row(c).Height(32).Children(func() {
				if navButton(c, monitor, "设备").Grow(1).Clicked() {
					v.devices = true
				}
				iconButton(c, settingsIcon, "设置").Disabled(true)
			})
		})
	})
}
func (v *View) toolbar(c *n.Context, t *n.Theme) {
	m := v.Model
	rightInset := float32(24)
	if m.SessionID != "" && !v.devices {
		// The scrollbar gutter and icon inset complete the visible right margin.
		rightInset = 10
	}
	n.Row(c).Height(48).FillWidth().Padding(0, rightInset, 0, 24).Gap(6).AlignItems(n.Center).DragWindow().Label("会话工具栏").Children(func() {
		if v.workspaceEditor != nil && !v.devices {
			if ghost(c, "返回").Clicked() {
				v.workspaceEditor = nil
			}
			n.Text(c, "工作区设置").FontSize(13).Grow(1)
			return
		} else if v.devices {
			n.Text(c, "设备").FontSize(13).Grow(1)
			add := iconButton(c, plus, "添加设备")
			v.choiceMenu(c, t, "add-device", add, []menuChoice{{id: "ssh", name: "直接 SSH", choose: func() { v.sshOpen = true; v.sshError = "" }}, {id: "tailnet", name: "从 Tailnet 添加", disabled: true}})
			return
		} else if m.SessionID != "" {
			heading := n.Row(c.Key("session-heading:" + m.SessionID)).Grow(1).MinWidth(0).Gap(6).AlignItems(n.Center).Label("会话名称")
			if progress := v.sessionEntryProgress(c, m.SessionID, true); progress < 1 {
				heading.Opacity(progress).Transform(n.Translation(0, 4*(1-progress)))
			}
			heading.Children(func() {
				for _, s := range m.Snapshot.Sessions {
					if s.ID == m.SessionID {
						v.toolbarWorkspace(c, t)
						n.Column(c).Size(1, 14).Background(t.Border).MarginX(8)
						n.Text(c, s.Title).SingleLine().Grow(1).FontSize(13).FontWeight(400)
					}
				}
			})
		} else {
			n.Spacer(c).Grow(1)
		}
		device := control(c).Height(26).PaddingX(6).Radius(6).Label("当前设备")
		device.Children(func() {
			n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
				color := t.TextMuted
				if m.Connected {
					color = t.Success
				}
				n.Column(c).Size(4, 4).Radius(2).Background(color).AlignSelf(n.Center)
				font := n.Font{Family: "system-ui", Size: 11, Weight: 400}
				inlineIconText(c, monitor, m.CurrentDeviceName(), font, 14, 6, t.TextMuted)
				inlineSymbol(c, chevron, m.CurrentDeviceName(), font, 12, t.TextMuted)
			})
		})
		status := "已连接"
		if !m.Connected {
			status = "未连接"
		}
		v.choiceMenu(c, t, "current-device", device, []menuChoice{{id: "connectivity", name: status, disabled: true}, {id: "runtime", name: "Codex · " + m.Readiness.State, disabled: true}, {id: "details", name: "设备详情", choose: func() { v.deviceSelected = m.ConnectionID(); v.deviceList = false; v.devices = true }}})
		if m.SessionID != "" && !v.devices {
			label := "展开更改侧栏"
			if m.Inspectors[m.SessionID] {
				label = "收起更改侧栏"
			}
			if iconButton(c, sidebarIcon, label).Clicked() {
				m.Inspectors[m.SessionID] = !m.Inspectors[m.SessionID]
			}
		}

	})
}
func (v *View) workspaceName() string {
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == v.Model.WorkspaceID {
			return w.Name
		}
	}
	return "工作区"
}
func (v *View) homeView(c *n.Context, t *n.Theme) {
	n.Column(c).Fill().PaddingX(36).AlignItems(n.Center).Children(func() {
		n.Spacer(c).Grow(.84)
		if len(v.Model.Snapshot.Workspaces) == 0 {
			n.Text(c, "从一个工作区开始").FontSize(28)
			if ghost(c, "创建工作区").Margin(18, 0, 0, 0).Disabled(!v.Model.Connected).Clicked() {
				v.createWorkspace()
			}
		} else {
			n.Row(c).Gap(8).Margin(0, 0, 28, 0).AlignItems(n.Center).Children(func() {
				n.Text(c, "想在").FixedLineHeight(48).FontSize(32).TextColor(t.TextMuted)
				picker := control(c).Height(48).MaxWidth(360).Label("选择工作区").Children(func() {
					n.Text(c, v.workspaceName()).FixedLineHeight(48).FontSize(32).FontWeight(400).Underline().SingleLine()
				})
				choices := []menuChoice{}
				for _, w := range v.Model.Snapshot.Workspaces {
					if w.Archived {
						continue
					}
					w := w
					choices = append(choices, menuChoice{id: w.ID, name: w.Name, detail: v.Model.DeviceName(w.DeviceID), checked: w.ID == v.Model.WorkspaceID, choose: func() { v.Model.WorkspaceID = w.ID }})
				}

				v.choiceMenu(c, t, "home-workspace", picker, choices)
				n.Text(c, "做些什么？").FixedLineHeight(48).FontSize(32).TextColor(t.TextMuted)
			})
			v.composer(c, t, true)
		}
		v.notice(c, t)
		n.Spacer(c).Grow(1.16)
	})
}
func (v *View) notice(c *n.Context, t *n.Theme) {
	v.archivedNotice(c, t)
	message := v.Model.Error
	if message == "" && !v.Model.Connected {
		message = "连接执行服务中…"
	}
	if message == "" && v.Model.Readiness.State != "ready" {
		message = v.Model.Readiness.Error
	}
	if message != "" {
		n.Text(c, message).FontSize(12).TextColor(t.TextMuted).MaxWidth(736).Margin(10, 0, 0, 0)
	}
}
func (v *View) composer(c *n.Context, t *n.Theme, home bool) {
	m := v.Model
	key := m.DraftKey()
	binding := textBinding(v.draftBindings, key, m.Drafts[key])
	draft := binding.Text
	cfg := m.Config()
	running := false
	for _, s := range m.Snapshot.Sessions {
		if s.ID == m.SessionID {
			running = s.Status == "running" || s.Status == "starting"
		}
	}
	bg := t.Background
	if t.Dark {
		bg = t.Surface
	}
	maxWidth := float32(768)
	inputHeight := float32(54)
	inputSize, inputLeading := float32(14), float32(22)
	if home {
		maxWidth = 736
		inputHeight = 72
		inputSize, inputLeading = 15, 26
	}
	stack := n.Column(c.Key("composer-stack:" + key)).Label("消息编辑区").FillWidth().MaxWidth(maxWidth).Gap(0).Children(func() {
		v.queueDisclosure(c, t)
		binding = textBinding(v.draftBindings, key, m.Drafts[key])

		editID := m.QueueEdits[m.SessionID].ID
		if v.composerHeightKey != key {
			v.composerHeightKey, v.composerNaturalHeight, v.composerEditID = key, 0, editID
			v.composerHeightUntil = time.Time{}
			v.composerInputHeight = inputHeight
		}
		if v.composerEditID != editID {
			v.composerEditID = editID
			v.composerHeightUntil = c.Now().Add(360 * time.Millisecond)
		}
		heightFrame := n.Column(c.Key("composer-height:"+key)).Label("消息编辑器").FillWidth().Clip().Background(bg).Border(1, t.Border).Radius(16).Shadow(0, 4, 14, 0, n.RGBA(0, 0, 0, .04))
		resize := c.Now().Before(v.composerHeightUntil) || editID != ""
		shownHeight := v.composerNaturalHeight
		if v.composerNaturalHeight > 0 {
			if resize {
				shownHeight = heightFrame.AnimateTo("composer-height", v.composerNaturalHeight, n.Animation{Duration: 360 * time.Millisecond, Ease: composerDescentEase})
				heightFrame.Height(shownHeight)
			} else {
				heightFrame.Jump("composer-height", v.composerNaturalHeight)
			}
		}
		heightFrame.Children(func() {
			var input n.Element
			composer := n.Column(c.Key("composer:"+key)).FillWidth().Padding(10, 14, 8, 6).Gap(4).Children(func() {
				if m.QueueEdits[m.SessionID].ID != "" {
					n.Column(c).Height(28)
				}
				binding = textBinding(v.draftBindings, key, m.Drafts[key])
				v.branchStrip(c, t, home)
				v.draftAttachmentStrip(c, t)
				n.Column(c.Key("composer-input-space")).Height(v.composerInputHeight).Shrink(0)
				// A zero-height origin fixed above the footer keeps outgoing editor
				// snapshots still while the composer changes its natural height.
				n.Column(c.Key("composer-input-anchor")).Absolute().Left(6).Right(14).Bottom(40).Height(0).Children(func() {
					inputKey := "input"
					if editID != "" {
						inputKey += ":edit:" + editID
					}
					input = n.TextAreaBase(c.Key(inputKey), &binding.Text).Absolute().Left(0).Right(0).Bottom(0).Label(v.composerLabel()).Placeholder("描述任务…").PlaceholderColor(subtle(t)).Padding(0, 9).FillWidth().Lines(1, 7).MinHeight(inputHeight).FontSize(inputSize).FixedLineHeight(inputLeading).OnTextKey(func(k n.TextKey) bool {
						if k.Key == n.KeyEnter && (k.Mods == 0 || k.Mods == n.Cmd) {
							m.Drafts[key] = k.Text()
							if strings.TrimSpace(k.Text()) != "" || len(m.Attachments[key]) > 0 {
								v.sendMessage(k.Mods == n.Cmd)
							}
							return true
						}
						return false
					})
					input.OnPaste(func(text string) bool { return v.pasteAttachments(key, text) })
					input.Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 180*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})

					if v.focusComposer {
						input.Focus()
						v.focusComposer = false
					}
					input.Changed()
					v.inputBounds = input.Bounds()
					if s := v.sent; s != nil && s.source.W > 0 && s.session == m.SessionID && !s.queued && !home && !c.Preferences().ReduceMotion {
						input.PlaceholderColor(subtle(t).Alpha(v.sendProgress(c)))
					}
					if s := v.sent; s != nil && s.origin == "" && s.session == m.SessionID && !home && !s.focused {
						if v.composerHadFocus {
							input.Focus()
						}
						s.focused = true
					}
					draft = binding.Text
					m.Drafts[key] = draft
					binding.LastModel = draft
				})
				n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
					if iconButton(c, plus, "添加附件").Size(28, 28).Disabled(v.Controller == nil || v.StatePath == "" || m.WorkerMinor < 6 || v.preparingAttachments(m.DraftKey())).Clicked() {
						v.pickAttachments()
					}
					permission := control(c).Height(28).PaddingX(6).Radius(6).Label("Permission").Children(func() {
						inlineIconText(c, shieldIcon, permissionLabel(cfg.Permission), n.Font{Family: "system-ui", Size: 12, Weight: 400}, 13, 6, t.TextMuted)
					})
					choices := []menuChoice{}
					for _, id := range m.Readiness.Permissions {
						id := id
						detail := map[string]string{"read-only": "仅查看", "default": "按需请求批准", "auto-approve": "沙盒内自动批准", "yolo": "不限制沙盒或批准"}[id]
						choices = append(choices, menuChoice{id: id, name: permissionLabel(id), detail: detail, checked: cfg.Permission == id, choose: func() { cfg.Permission = id; m.Configs[key] = cfg }})
					}
					v.choiceMenu(c, t, "permission:"+key, permission, choices)
					n.Spacer(c).Grow(1)
					v.runControl(c, t, key, cfg)
					empty := strings.TrimSpace(draft) == "" && len(m.Attachments[key]) == 0
					icon := arrow
					label := "发送"
					editing := m.QueueEdits[m.SessionID].ID != ""
					queuing := running || len(m.Queue().Items) > 0
					disabled := !m.Connected || m.Readiness.State != "ready" || empty || m.ArchivedContext() != "" || v.preparingAttachments(m.DraftKey())
					if queuing {
						icon = queueAddIcon
						label = "加入队列"
						disabled = disabled || m.WorkerMinor < 2
					}
					if editing {
						icon = check
						label = "保存编辑"
						disabled = disabled || v.queueBusy[m.SessionID]
					}
					if running && empty && !editing {
						icon = stop
						label = "停止当前轮"
						disabled = !m.Connected
					}
					fill := t.Accent
					if disabled {
						fill = subtle(t)
					}
					send := controlFeedback(c, n.ButtonBase(c), 14, true).Size(28, 28).Radius(14).Background(fill).TextColor(t.AccentText).Label(label).Disabled(disabled).Children(func() {
						n.Icon(c.Key("primary-icon:"+label), icon).Size(20, 20).Transition(n.ElementTransition{Duration: motionAnimation(c, 120*time.Millisecond).Duration, Ease: n.EaseOut, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
					})
					if send.Clicked() {
						if running && empty && !editing {
							v.interrupt()
						} else {
							m.Drafts[key] = draft
							v.submit()
						}
					}
				})
			})

			if v.composerNaturalHeight > 0 && resize {
				composer.Absolute().Left(0).Right(0).Bottom(0)
			}
			invalidate := c.Services().Invalidate
			composer.DrawOver(func(_ *n.Painter, r n.Rect) {
				// Measure from the live editor's layout even at zero opacity;
				// outgoing editor snapshots must never feed the dock's height.
				if input.Valid() {
					bounds := input.LayoutBounds()
					if bounds.H > 0 && math.Abs(float64(v.composerInputHeight-bounds.H)) > .5 {
						v.composerInputHeight = bounds.H
						invalidate()
					}
				}
				if v.composerHeightKey == key && math.Abs(float64(v.composerNaturalHeight-r.H-2)) > .5 {
					v.composerNaturalHeight = r.H + 2
					invalidate()
				}
			})
			if editID != "" {
				// Keep expanding editor contents below the pinned edit controls.
				n.Column(c.Key("queue-edit-header-surface")).Absolute().Left(1).Right(1).Top(1).Height(37).Radius(15, 15, 0, 0).Background(bg).IgnoreInput(true)
			}
			v.queueEditBar(c, t)
		})
	})
	if files := stack.DroppedFiles(); len(files) > 0 {
		v.addAttachmentFiles(key, files)
	}
	v.composerSendTransition(c, stack, home)
	v.composerHadFocus = stack.FocusWithin()
}
func permissionLabel(id string) string {
	switch id {
	case "read-only":
		return "只读"
	case "auto-approve":
		return "自动批准"
	case "yolo":
		return "YOLO"
	default:
		return "默认权限"
	}
}
func (v *View) runControl(c *n.Context, t *n.Theme, key string, cfg domain.RunConfig) {
	summary := cfg.Model
	if cfg.Effort != "" {
		summary += " · " + cfg.Effort
	}
	if cfg.Plan {
		summary += " · Plan"
	}
	if cfg.Fast {
		summary += " · Fast"
	}
	trigger := control(c).Height(28).MaxWidth(390).MinWidth(0).PaddingX(6).Radius(6).Label("运行配置").Tooltip("Codex · " + summary).Children(func() {
		inlineIconText(c, openAIBlossom, summary, n.Font{Family: "system-ui", Size: 12, Weight: 400}, 20, 3, t.TextMuted)
	})
	if trigger.Clicked() {
		v.runOpen = !v.runOpen
		v.runPage = ""
	}
	popover(c.Key("run-panel:"+key), trigger, &v.runOpen, func(panel n.Element) {
		panel.Width(240).Padding(4).Radius(9).Background(raised(t)).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
			if v.runPage != "" {
				if ghost(c, "‹  返回").FillWidth().TextAlign(n.Start).Clicked() {
					v.runPage = ""
				}
				n.Scroll(c).MaxHeight(300).FillWidth().Children(func() {
					if v.runPage == "model" {
						for _, model := range v.Model.Readiness.Models {
							label := model.Name
							if label == "" {
								label = model.ID
							}
							if ghost(c, label).FillWidth().TextAlign(n.Start).Tooltip(model.ID).Clicked() {
								cfg.Model = model.ID
								v.Model.Configs[key] = cfg
								v.Model.Config()
								v.runPage = ""
							}
						}
					} else {
						for _, model := range v.Model.Readiness.Models {
							if model.ID == cfg.Model {
								for _, effort := range model.Efforts {
									if ghost(c, effort).FillWidth().TextAlign(n.Start).Clicked() {
										cfg.Effort = effort
										v.Model.Configs[key] = cfg
										v.runPage = ""
									}
								}
							}
						}
					}
				})
				return
			}
			n.Row(c).Height(28).PaddingX(8).AlignItems(n.Center).Children(func() { n.Text(c, "Agent").Grow(1); n.Text(c, "Codex") })
			if settingRow(c, "模型", cfg.Model+" ›").Clicked() {
				v.runPage = "model"
			}
			if settingRow(c, "推理强度", cfg.Effort+" ›").Clicked() {
				v.runPage = "effort"
			}
			n.Column(c).Height(1).FillWidth().Background(t.Border).MarginY(4)
			plan := cfg.Plan
			if controlFeedback(c, n.Checkbox(c, &plan, "Plan").Height(28).Disabled(!v.Model.Readiness.Plan), 4).Changed() {
				cfg.Plan = plan
				v.Model.Configs[key] = cfg
			}
			fastAvailable := false
			for _, model := range v.Model.Readiness.Models {
				if model.ID == cfg.Model {
					fastAvailable = model.Fast
				}
			}
			if fastAvailable {
				fast := cfg.Fast
				if controlFeedback(c, n.Checkbox(c, &fast, "Fast").Height(28), 4).Changed() {
					cfg.Fast = fast
					v.Model.Configs[key] = cfg
				}
			}
		})
	})
}
func settingRow(c *n.Context, name, value string) n.Element {
	return control(c, 5).FillWidth().Height(28).PaddingX(8).Radius(5).Label(name).Children(func() {
		n.Row(c).FillWidth().AlignItems(n.Center).Children(func() {
			n.Text(c, name)
			n.Spacer(c).Grow(1)
			n.Text(c, value).SingleLine().MaxWidth(160).Tooltip(value)
		})
	})
}
func (v *View) conversation(c *n.Context, t *n.Theme) {
	m := v.Model
	// An empty detail keeps the dock mounted through first-turn hydration.
	// Its zero Session.ID also keeps the history-fold barrier unconsumed.
	detail := m.Details[m.SessionID]
	state := v.lists[m.SessionID]
	if state == nil {
		state = &n.ListState{FollowEnd: true}
		v.lists[m.SessionID] = state
	}
	if m.FoldToolHistory[m.SessionID] {
		state.ReleaseRow()
	}
	rows := v.transcriptRows(detail.Items)
	turns := make(map[string]domain.Turn, len(detail.Turns))
	for _, turn := range detail.Turns {
		turns[turn.ID] = turn
	}
	rows = withTurnFooters(rows, detail.Items, turns)
	if v.toolsAnimating {
		c.AnimationFrame()
	}
	state.Reveals = state.Reveals[:0]
	if !v.reduceMotion {
		for i, row := range rows {
			if row.toolParent == "" || row.opacity >= 1 {
				continue
			}
			if len(state.Reveals) > 0 && i > 0 && rows[i-1].toolParent == row.toolParent {
				state.Reveals[len(state.Reveals)-1].End = i + 2
			} else {
				state.Reveals = append(state.Reveals, n.ListReveal{First: i + 1, End: i + 2, Progress: row.opacity})
			}
		}
	}
	state.Key = func(i int) any {
		if i == 0 {
			return "history-top"
		}
		if i == len(rows)+1 {
			return "history-bottom"
		}
		return rows[i-1].key
	}
	v.anchorSentMessage(c, state, rows)
	reserve := v.dockHeight
	if reserve == 0 {
		reserve = 108
	}
	history := n.List(c.Key("history:"+m.SessionID), state, len(rows)+2, func(row int) {
		if row == 0 {
			n.Column(c).Height(80)
			return
		}
		if row == len(rows)+1 {
			n.Column(c).Height(reserve + 32)
			return
		}
		entry := rows[row-1]
		v.currentTranscriptRow = row
		i := entry.end
		item := entry.item
		bottom := float32(8)
		if item.Kind == "user" {
			bottom = 32
		}
		if i >= 0 && (i == len(detail.Items)-1 || (item.TurnID != "" && detail.Items[i+1].TurnID != item.TurnID)) {
			bottom = 32
			if _, ok := turns[item.TurnID]; ok {
				bottom = 8
			}
		}
		if entry.footer != "" {
			bottom = 32
		}
		n.Column(c).FillWidth().PaddingX(18).AlignItems(n.Center).Children(func() {
			content := n.Column(c).FillWidth().MaxWidth(768).Gap(8).Padding(0, 0, bottom, 0).Selectable()
			if entry.footer == "" && entry.group == "" && (item.Kind == "user" || item.Kind == "assistant") {
				n.SelectionItem(content, item.ID)
			}
			v.sentMessagePose(c, content, item)
			if entry.toolParent != "" {
				content.Padding(0, 0, bottom, 24).Draw(func(p *n.Painter, r n.Rect) {
					height := r.H
					if entry.toolResult != "" {
						height = 10
					}
					p.Fill(n.Rect{X: r.X + 7.5, Y: r.Y, W: 1, H: height}, t.Border, 0)
				})
				content.Opacity(entry.opacity)
				if !v.reduceMotion {
					content.Transform(n.Translation(0, -4*(1-entry.opacity)))
				}
			}
			content.Children(func() {
				if entry.footer != "" {
					v.turnFooter(c, t, turns[entry.footer])
				} else if entry.group != "" {
					v.toolGroup(c, t, entry, row)
				} else if entry.toolParent != "" {
					v.toolTimelineItem(c, t, entry)
				} else {
					v.item(c, t, item)
				}
			})
		})
	}).Absolute().Left(0).Right(0).Top(0).Bottom(0).Label("对话记录")
	n.VirtualTextSelection(history, func(key string) (string, bool) {
		for _, item := range v.Model.Details[v.Model.SessionID].Items {
			if item.ID == key {
				return item.Text, true
			}
		}
		return "", false
	})
	v.conversationOutline(c, t, state, rows)
	v.returnToLatest(c, t, state)
	n.Column(c).Absolute().Left(0).Right(8).Top(0).Height(48).Background(t.Background).Children(func() { v.toolbar(c, t) })
	dock := n.Column(c.Key("conversation-dock")).Absolute().Left(0).Right(0).Bottom(8).PaddingX(18).AlignItems(n.Center).Gap(8).Label("会话底部面板")
	// Native wrapping and question disclosure determine the complete dock height.
	invalidate := c.Services().Invalidate
	dock.DrawOver(func(_ *n.Painter, r n.Rect) {
		if math.Abs(float64(v.dockHeight-r.H)) > .5 {
			v.dockHeight = r.H
			invalidate()
		}
	})
	dock.Children(func() { v.recovery(c, t, detail); v.pending(c, t, detail); v.composer(c, t, false); v.notice(c, t) })
}

func (v *View) item(c *n.Context, t *n.Theme, item domain.Item) {
	switch item.Kind {
	case "user":
		n.Row(c).FillWidth().Justify(n.End).Children(func() {
			bubble := n.Column(c).MaxWidth(676).Padding(12, 16).Gap(8).Background(userBubble(t)).Radius(14).Children(func() {
				if item.Text != "" {
					n.Text(c, item.Text).FontSize(14).FixedLineHeight(24).Selectable()
				}
				if len(item.Attachments) > 0 {
					n.Row(c).Wrap().Gap(8).Children(func() {
						for _, a := range item.Attachments {
							var source n.Rect
							button := control(c.Key(a.ID)).Height(64).MaxWidth(220).Padding(6, 8).Radius(7).Background(codeSurface(t)).Label("预览附件：" + a.Name).Children(func() {
								n.Row(c).Gap(8).AlignItems(n.Center).Children(func() {
									source = v.attachmentThumbnail(c, t, a, v.Model.WorkspaceID, 48).Bounds()
									n.Column(c).Gap(3).Children(func() {
										n.Text(c, a.Name).FontSize(12).SingleLine().MaxWidth(140)
										n.Text(c, attachmentSize(a.Size)).FontSize(11).TextColor(t.TextMuted)
									})
								})
							})
							if button.Clicked() {
								if !strings.HasPrefix(a.MIME, "image/") {
									source = button.Bounds()
								}
								v.openAttachment(a, v.Model.WorkspaceID, source)
							}
						}
					})
				}
			})
			v.sentMessageBubble(c, t, bubble, item)
		})
	case "assistant":
		v.assistantReply(c, item)
	case "tool":
		if item.Presentation != "" && item.Presentation != "command" {
			v.activityItem(c, t, item)
			return
		}
		label := item.Name
		if label == "" {
			label = "工具活动"
		}
		if control(c).FillWidth().MinHeight(20).Justify(n.Start).Label(label).Children(func() {
			n.Text(c, label).Font("Menlo").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
		}).Clicked() {
			v.Model.Collapsed["tool:"+item.ID] = !v.Model.Collapsed["tool:"+item.ID]
		}
		if !v.Model.Collapsed["tool:"+item.ID] && item.Text != "" {
			n.Scroll(c.Key("output:" + item.ID)).FillWidth().MaxHeight(160).Children(func() {
				n.Text(c, item.Text).FillWidth().Padding(8, 10).Background(codeSurface(t)).Radius(6).Font("Menlo").FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Selectable()
			})
		}
	case "activity":
		v.activityItem(c, t, item)
	case "request_result":
		v.requestResult(c, t, item)
	case "request":
		label := item.Text
		switch item.Status {
		case "answered":
			label += " · 已处理"
		case "resolved":
			label += " · 已结束"
		case "invalidated":
			label += " · 已失效"
		case "submitting":
			label += " · 提交中"
		}
		icon := questionIcon
		for _, request := range v.Model.Details[v.Model.SessionID].Requests {
			if item.ID == "request:"+request.ID && (request.Kind == "approval" || request.Kind == "permissions") {
				icon = shieldIcon
				break
			}
		}
		active := item.Status == "pending" || item.Status == "submitting"
		var source n.Element
		if active {
			source = control(c).AlignSelf(n.Start).MinHeight(20).Justify(n.Start).Label(label)
		} else {
			source = n.Row(c).FillWidth().MinHeight(20).Label(label)
		}
		source.Children(func() {
			n.Row(c).Gap(7).AlignItems(n.Baseline).Children(func() {
				font := n.Font{Family: "system-ui", Size: 11, Weight: 400}
				inlineSymbol(c, icon, label, font, 12, subtle(t))
				v.requestSourceLabel(c, t, item, label)
				if item.Status == "pending" || item.Status == "submitting" {
					inlineSymbol(c, disclosureRight, label, font, 10, subtle(t))
				}
			})
		})
		if active && source.Clicked() {
			v.openSourceRequest(item)
		}
	default:
		n.Text(c, item.Text).TextColor(t.TextMuted).FontSize(12)
	}
}
func (v *View) turnFooter(c *n.Context, t *n.Theme, turn domain.Turn) {
	if turn.Status == "running" && !v.Model.Connected {
		n.Text(c, "连接已断开，执行状态待核对").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
		return
	}
	n.Row(c).Label("回复信息").FillWidth().Wrap().Gap(12).MinHeight(24).AlignItems(n.Baseline).Children(func() {
		label := turn.Status
		switch turn.Status {
		case "running":
			label = "进行中"
		case "interrupted":
			label = "已停止"
		case "failed":
			label = "执行失败"
		case "abandoned":
			label = "已手动结束 · 结果未确认"
		case "unknown":
			label = "执行状态待核实"
		}
		if turn.Status == "completed" {
			copy := n.ButtonBase(c.Key("copy-reply:"+turn.ID)).Size(20, 24).Justify(n.Start).Label("复制回复")
			copiedUntil := n.Local(copy, "copied-until", func() time.Time { return time.Time{} })
			if copy.Clicked() {
				var parts []string
				for _, item := range v.Model.Details[v.Model.SessionID].Items {
					if item.TurnID == turn.ID && item.Kind == "assistant" && strings.TrimSpace(item.Text) != "" {
						parts = append(parts, item.Text)
					}
				}
				if len(parts) > 0 {
					c.WriteClipboard(strings.Join(parts, "\n\n"))
					*copiedUntil = c.Now().Add(2 * time.Second)
				}
			}
			glyph, glyphKey, tooltip := copyReplyIcon, "copy-icon", "复制回复"
			if c.Now().Before(*copiedUntil) {
				glyph, glyphKey, tooltip = check, "copied-icon", "已复制"
				c.After(copiedUntil.Sub(c.Now()))
			}
			copy.Tooltip(tooltip)
			color := textHoverColor(c, copy, t.TextMuted)
			copy.Children(func() {
				inlineSymbol(c.Key(glyphKey), glyph, "回复", n.Font{Family: "system-ui", Size: 11, Weight: 400}, 12, color).
					Label(tooltip).Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 100*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
			})
			ended, exact := "—", "结束时间未知"
			if turn.CompletedAt != nil {
				local := turn.CompletedAt.Local()
				ended = local.Format("15:04")
				exact = local.Format("2006-01-02 15:04:05")
			}
			n.Text(c, ended).Label("结束时间：" + ended).Tooltip(exact).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			duration := "用时 —"
			if turn.DurationMS != nil {
				seconds := *turn.DurationMS / 1000
				duration = fmt.Sprintf("用时 %d分%d秒", seconds/60, seconds%60)
			}
			n.Text(c, duration).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
		} else if turn.Status == "running" {
			n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
				statusDot(c, label, n.Font{Family: "system-ui", Size: 11, Weight: 400}, t.Success, true)
				text := n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				shimmer(c, text, t.TextMuted, t.Text)
			})
		} else {
			n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
		}
		if turn.Status != "completed" && turn.DurationMS != nil {
			n.Text(c, fmt.Sprintf("%.1f 秒", float64(*turn.DurationMS)/1000)).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
		}
		if turn.Status == "completed" {
			usage := n.ButtonBase(c).MinHeight(24).Label("本轮用量")
			color := textHoverColor(c, usage, t.TextMuted)
			usage.Children(func() {
				n.Text(c, fmt.Sprintf("输入 %s · 输出 %s · 缓存 %s", count(turn.InputTokens), count(turn.OutputTokens), count(turn.CachedInputTokens))).
					FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(color)
			})
			choices := []menuChoice{}
			for _, field := range []struct {
				name  string
				value *int64
			}{{"输入", turn.InputTokens}, {"输出", turn.OutputTokens}, {"缓存", turn.CachedInputTokens}} {
				value := "—"
				if field.value != nil {
					value = fmt.Sprint(*field.value)
				}
				choices = append(choices, menuChoice{id: field.name, name: field.name + " " + value, disabled: true})
			}
			v.choiceMenu(c, t, "usage:"+turn.ID, usage, choices)
		}
	})
}
func count(value *int64) string {
	if value == nil {
		return "—"
	}
	if *value > 1000 {
		return fmt.Sprintf("%.1fk", float64(*value)/1000)
	}
	return fmt.Sprint(*value)
}
func (v *View) pendingCount(session string) int {
	count := 0
	for _, r := range v.Model.Details[session].Requests {
		if r.State == "pending" || r.State == "submitting" {
			count++
		}
	}
	return count
}

const requestRadius = 8

func (v *View) respond(r domain.PendingRequest, answer domain.Answer) {
	if v.Controller == nil {
		return
	}
	v.do(app.Job{Method: "request.respond", Input: protocol.AnswerInput{SessionID: r.SessionID, Answer: answer}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if err != nil {
			m.Error = err.Error()
		}
	}})
}
func (v *View) submit() { v.sendMessage(false) }
func (v *View) sendMessage(steer bool) {
	if v.parent != nil && v.parent.Model.SecondarySessionID != v.Model.SessionID {
		return
	}
	if v.preparingAttachments(v.Model.DraftKey()) {
		v.Model.Error = "正在准备附件，请稍候"
		return
	}
	if v.Model.QueueEdits[v.Model.SessionID].ID != "" {
		v.saveQueued()
		return
	}
	key, _ := v.gitQuery()
	if v.gitPending[key] && !v.Model.Active() && len(v.Model.Queue().Items) == 0 {
		v.Model.Error = "正在核对设备上的分支，请稍后提交"
		return
	}
	if v.Controller == nil {
		return
	}
	m := v.Model
	in, method, turn, err := m.BeginMessage(steer)
	if err != nil {
		m.Error = err.Error()
		return
	}
	if method != "session.submit" {
		v.submitActive(in, method, turn)
		return
	}
	sent := v.beginSendMotion(in)
	var prepare func() error
	if v.Saver != nil {
		state, _ := v.persistenceModel().EncodeState()
		v.Saver.Queue(state)
		v.lastQueued = state
		prepare = v.Saver.FlushLatest
	}
	if !v.do(app.Job{Prepare: prepare, Method: "session.submit", Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if err != nil {
			if v.sent == sent {
				v.sent = nil
			}
			m.SubmissionError(in, err)
			return
		}

		var op domain.Operation
		if err = json.Unmarshal(b, &op); err != nil {
			if v.sent == sent {
				v.sent = nil
			}
			m.Error = err.Error()
			return
		}
		m.Accepted(in, op)
		if op.State == "rejected" || op.State == "indeterminate" {
			if v.sent == sent {
				v.sent = nil
			}
			return
		}
		if v.sent == sent {
			sent.session = op.SessionID
			if sent.origin == "" && op.SessionID != "" {
				v.sessionEntry = &sessionEntryMotion{session: op.SessionID, operation: in.OperationID, workspace: in.WorkspaceID, epoch: sent.epoch}
			}
		}
	}}) {
		if v.sent == sent {
			v.sent = nil
		}
		m.Reject(in, "操作繁忙，请重试")
	}
}
func (v *View) interrupt() {
	if v.Controller == nil {
		return
	}
	d := v.Model.Details[v.Model.SessionID]
	for _, turn := range d.Turns {
		if turn.Status == "running" {
			v.do(app.Job{Method: "turn.interrupt", Input: map[string]string{"sessionId": d.Session.ID, "turnId": turn.ID}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
				if err != nil {
					m.Error = err.Error()
				}
			}})
			return
		}
	}
}
func (v *View) createWorkspace() { v.beginWorkspace(v.Model.ConnectionID()) }

func (v *View) nextAttention() {
	for _, s := range v.Model.Snapshot.Sessions {
		if v.pendingCount(s.ID) > 0 {
			v.navigateSession(s.ID, s.WorkspaceID)
			v.collapsedWorkspaces[s.WorkspaceID] = false
			v.Model.Collapsed[s.ID] = false
			v.devices = false
			return
		}
	}
	v.Model.Error = "暂无待处理事项"
}
func (v *View) do(job app.Job) bool {
	if v.parent != nil && v.parent.Model.SecondarySessionID != v.Model.SessionID {
		return false
	}
	if v.Controller == nil {
		return false
	}
	if job.ConnectionID == "" {
		job.ConnectionID = v.Model.ConnectionID()
	}
	job.ConnectionEpoch = v.Model.ConnectionEpoch
	if state := v.Model.DeviceStates[job.ConnectionID]; state != nil {
		job.ConnectionEpoch = state.ConnectionEpoch
	}
	if v.parent != nil && job.Apply != nil {
		apply := job.Apply
		session := v.Model.SessionID
		job.Apply = func(model *app.Model, body json.RawMessage, err error) {
			previous := model.Error
			model.Error = ""
			apply(model, body, err)
			if v.Model.SessionID == session {
				v.Model.Error = model.Error
			}
			model.Error = previous
		}
	}
	return v.Controller.Do(job)
}

func textBinding(bindings map[string]*editBinding, key, value string) *editBinding {
	b := bindings[key]
	if b == nil {
		b = &editBinding{Text: value, LastModel: value}
		bindings[key] = b
	} else if value != b.LastModel {
		b.Text = value
		b.LastModel = value
	}
	return b
}

func raised(t *n.Theme) n.Color {
	if t.Dark {
		return n.Hex("#2d2d2d")
	}
	return n.Hex("#ffffff")
}
func questionBodyHeight(c *n.Context) float32 { _, h := c.Size(); return min(240, h*.28) }
