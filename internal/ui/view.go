// Package ui renders Shioji with MyGo's native components; no WebView is used.
package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type editBinding struct {
	Text      string
	LastModel string
}

type View struct {
	draftBindings           map[string]*editBinding
	answerBindings          map[string]*editBinding
	customBindings          map[string]*editBinding
	Saver                   *app.StateWriter
	lastQueued              []byte
	lastState               []byte
	Model                   *app.Model
	Controller              *app.Controller
	StatePath               string
	lists                   map[string]*n.ListState
	nav                     n.ListState
	md                      markdown.Options
	runOpen, permissionOpen bool
	runPage                 string
	devices                 bool
	selected                string
	collapsedWorkspaces     map[string]bool
	dockHeight              float32
	searchOpen              bool
	searchText, searchSent  string
	searchResults           []domain.SearchResult
	searchList              n.ListState
	branchOpen              bool
	branchSearch            string
	branchList              n.ListState
	switchingBranch         bool
	gitLoaded               map[string]string
	gitPending              map[string]bool
	changes                 map[string]*changesState
	menuOpen                map[string]bool
	menuContext             string
}

func New(model *app.Model, controller *app.Controller, path string) *View {
	v := &View{draftBindings: map[string]*editBinding{}, answerBindings: map[string]*editBinding{}, customBindings: map[string]*editBinding{}, Model: model, Controller: controller, StatePath: path, lists: map[string]*n.ListState{}, collapsedWorkspaces: map[string]bool{}, md: markdown.Options{ShareSelection: true, WrapCode: true}}
	if path != "" {
		v.Saver = app.NewStateWriter(path)
	}
	return v
}
func (v *View) Flush() error {
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

	if v.Saver == nil {
		return nil
	}
	data, err := v.Model.EncodeState()
	if err != nil {
		return err
	}
	return v.Saver.Flush(data)
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
	if v.Controller != nil && v.selected != m.SessionID {
		v.selected = m.SessionID
		v.Controller.Select(m.SessionID)
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
	v.requestGit(false)
	if c.Shortcut(n.Cmd, n.KeyK) {
		v.searchOpen = !v.searchOpen
	}
	if c.Shortcut(n.Cmd, n.KeyN) {
		v.home()
	}
	n.Row(c).Fill().Background(t.Background).Children(func() {
		v.sidebar(c, &t)
		width, _ := c.Size()
		open := m.SessionID != "" && !v.devices && m.Inspectors[m.SessionID]
		inline := width > 1160
		n.Column(c).FillHeight().Grow(1).Children(func() {
			if v.devices {
				v.toolbar(c, &t)
				v.deviceView(c, &t)
			} else if m.SessionID == "" {
				v.toolbar(c, &t)
				v.homeView(c, &t)
			} else {
				v.conversation(c, &t)
			}
		})
		if open {
			if inline {
				state := v.changesFor(m.SessionID)
				if state.Path != "" {
					v.diffReader(c, &t, (width-256-280)/2)
				}
				v.inspector(c, &t, false)
			} else {
				v.inspector(c, &t, true)
			}
		}
	})
	if v.Controller != nil && v.selected != m.SessionID {
		v.selected = m.SessionID
		v.Controller.Select(m.SessionID)
		c.After(0)
	}
}

func (v *View) home() {
	v.Model.SessionID = ""
	v.devices = false
	v.runOpen = false
	v.permissionOpen = false
	v.branchOpen = false
	v.searchOpen = false
}
func iconButton(c *n.Context, icon *n.SVG, label string) n.Element {
	return n.ButtonBase(c).Size(28, 28).Radius(6).Label(label).Tooltip(label).TextColor(c.Theme().TextMuted).Children(func() { n.Icon(c, icon).Size(16, 16) })
}
func ghost(c *n.Context, label string) n.Element {
	return n.Button(c, label).FontWeight(400).Justify(n.Start).Background(n.Transparent).Border(0, n.Transparent).Height(28)
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
			n.Row(c).Height(38).DragWindow().Children(func() {
				n.Spacer(c).Grow(1)
				v.searchControl(c, t)
			})
			if navButton(c, plus, "新建会话").Margin(0, 0, 4, 0).Background(func() n.Color {
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
			n.Row(c).Height(32).Padding(8, 8, 0, 8).AlignItems(n.Center).Children(func() {
				n.Text(c, "工作区").FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted).Grow(1)
				if iconButton(c, plus, "创建工作区").Size(24, 24).Disabled(!m.Connected).Clicked() {
					v.createWorkspace()
				}
			})
			type navRow struct {
				workspace *domain.Workspace
				session   *domain.Session
			}
			rows := []navRow{}
			for i := range m.Snapshot.Workspaces {
				w := &m.Snapshot.Workspaces[i]
				rows = append(rows, navRow{workspace: w})
				if !v.collapsedWorkspaces[w.ID] {
					for j := range m.Snapshot.Sessions {
						s := &m.Snapshot.Sessions[j]
						if s.WorkspaceID == w.ID {
							rows = append(rows, navRow{session: s})
						}
					}
				}
			}
			v.nav.Key = func(i int) any {
				if rows[i].workspace != nil {
					return "w:" + rows[i].workspace.ID
				}
				return "s:" + rows[i].session.ID
			}
			n.List(c, &v.nav, len(rows), func(i int) {
				r := rows[i]
				if r.workspace != nil {
					w := r.workspace
					workspaceRow := n.Row(c).Height(30).Gap(3).AlignItems(n.Center)
					workspaceRow.Children(func() {
						if n.ButtonBase(c.Key("workspace:" + w.ID)).Height(24).Grow(1).Justify(n.Start).Label(w.Name).Children(func() {
							n.Row(c).FillWidth().Gap(3).AlignItems(n.Baseline).Children(func() {
								icon := disclosureDown
								if v.collapsedWorkspaces[w.ID] {
									icon = disclosureRight
								}
								inlineSymbol(c, icon, w.Name, n.Font{Family: "system-ui", Size: 12, Weight: 500}, 11, t.TextMuted).Width(20).TextAlign(n.Center)
								n.Text(c, w.Name).SingleLine().Grow(1).FontSize(12).FontWeight(500).FixedLineHeight(20).Tooltip(w.Name)
							})
						}).Clicked() {
							v.collapsedWorkspaces[w.ID] = !v.collapsedWorkspaces[w.ID]
						}
						if workspaceRow.Hovered() || workspaceRow.FocusWithin() {
							if iconButton(c, plus, "在 "+w.Name+" 新建会话").Size(22, 22).Clicked() {
								m.WorkspaceID = w.ID
								v.home()
							}
						} else {
							n.Text(c, "这台 Mac").FontSize(11).TextColor(t.TextMuted).PaddingX(6)
						}
					})
					return
				}
				s := r.session
				selected := s.ID == m.SessionID && !v.devices
				row := n.ButtonBase(c).FillWidth().Height(28).MarginY(1).Padding(4, 8, 4, 20).Radius(8).Label(s.Title)
				if selected {
					row.Background(t.SurfacePressed)
				}
				row.Children(func() {
					n.Row(c).FillWidth().Gap(6).AlignItems(n.Baseline).Children(func() {
						if v.pendingCount(s.ID) > 0 {
							n.Column(c).Width(10).AlignItems(n.Center).AlignSelf(n.Center).Children(func() { n.Column(c).Size(4, 4).Radius(2).Background(t.Warning) })
						} else if s.Status == "running" || s.Status == "starting" {
							n.Column(c).Width(10).AlignItems(n.Center).AlignSelf(n.Center).Children(func() { n.Column(c).Size(4, 4).Radius(2).Background(t.Success) })
						} else if s.Status == "completed" {
							inlineSymbol(c, check, s.Title, n.Font{Family: "system-ui", Size: 13, Weight: 400}, 10, t.Success)
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
							if s.Status == "interrupted" {
								label = "已停止"
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
				if row.Clicked() {
					m.SessionID = s.ID
					m.WorkspaceID = s.WorkspaceID
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
		if v.devices {
			n.Text(c, "设备").FontSize(13).Grow(1)
		} else if m.SessionID != "" {
			for _, s := range m.Snapshot.Sessions {
				if s.ID == m.SessionID {
					n.Text(c, v.workspaceName()).SingleLine().MaxWidth(150).FontSize(12).TextColor(t.TextMuted)
					n.Column(c).Size(1, 14).Background(t.Border).MarginX(8)
					n.Text(c, s.Title).SingleLine().Grow(1).FontSize(13).FontWeight(400)
				}
			}
		} else {
			n.Spacer(c).Grow(1)
		}
		device := n.ButtonBase(c).Height(26).PaddingX(6).Radius(6).Label("当前设备")
		device.Children(func() {
			n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
				color := t.TextMuted
				if m.Connected {
					color = t.Success
				}
				n.Column(c).Size(4, 4).Radius(2).Background(color).AlignSelf(n.Center)
				font := n.Font{Family: "system-ui", Size: 11, Weight: 400}
				inlineIconText(c, monitor, "这台 Mac", font, 14, 6, t.TextMuted)
				inlineSymbol(c, chevron, "这台 Mac", font, 12, t.TextMuted)
			})
		})
		status := "已连接"
		if !m.Connected {
			status = "未连接"
		}
		v.choiceMenu(c, t, "current-device", device, []menuChoice{{id: "connectivity", name: status, disabled: true}, {id: "runtime", name: "Codex · " + m.Readiness.State, disabled: true}, {id: "details", name: "设备详情", choose: func() { v.devices = true }}})
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
				picker := n.ButtonBase(c).Height(48).MaxWidth(360).Label("选择工作区").Children(func() {
					n.Text(c, v.workspaceName()).FixedLineHeight(48).FontSize(32).FontWeight(400).Underline().SingleLine()
				})
				choices := []menuChoice{}
				for _, w := range v.Model.Snapshot.Workspaces {
					w := w
					choices = append(choices, menuChoice{id: w.ID, name: w.Name, detail: "这台 Mac", checked: w.ID == v.Model.WorkspaceID, choose: func() { v.Model.WorkspaceID = w.ID }})
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
	n.Column(c.Key("composer:"+key)).FillWidth().MaxWidth(maxWidth).Padding(10, 14, 8, 6).Gap(4).Background(bg).Border(1, t.Border).Radius(16).Shadow(0, 4, 14, 0, n.RGBA(0, 0, 0, .04)).Children(func() {
		v.branchStrip(c, t, home)
		n.TextAreaBase(c.Key("input"), &binding.Text).Label("消息输入").Placeholder("描述任务…").PlaceholderColor(subtle(t)).Padding(0, 9).FillWidth().Lines(1, 7).MinHeight(inputHeight).FontSize(inputSize).FixedLineHeight(inputLeading).OnTextKey(func(k n.TextKey) bool {
			if k.Key == n.KeyEnter && (k.Mods == 0 || k.Mods == n.Cmd) {
				m.Drafts[key] = k.Text()
				if strings.TrimSpace(k.Text()) != "" {
					v.submit()
				}
				return true
			}
			return false
		}).Changed()
		draft = binding.Text
		m.Drafts[key] = draft
		binding.LastModel = draft
		n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
			iconButton(c, plus, "添加附件（当前 Codex 适配器暂不支持）").Size(28, 28).Disabled(true)
			permission := n.ButtonBase(c).Height(28).PaddingX(6).Radius(6).Label("Permission").Children(func() {
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
			empty := strings.TrimSpace(draft) == ""
			icon := arrow
			label := "发送"
			disabled := !m.Connected || m.Readiness.State != "ready" || empty || running
			if running && empty {
				icon = stop
				label = "停止当前轮"
				disabled = !m.Connected
			}
			fill := t.Accent
			if disabled {
				fill = subtle(t)
			}
			send := n.ButtonBase(c).Size(28, 28).Radius(14).Background(fill).TextColor(t.AccentText).Label(label).Disabled(disabled).Children(func() { n.Icon(c, icon).Size(20, 20) })
			if send.Clicked() {
				if running && empty {
					v.interrupt()
				} else {
					m.Drafts[key] = draft
					v.submit()
				}
			}
		})
	})
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
	trigger := n.ButtonBase(c).Height(28).MaxWidth(390).MinWidth(0).PaddingX(6).Radius(6).Label("运行配置").Tooltip("Codex · " + summary).Children(func() {
		inlineIconText(c, openAIBlossom, summary, n.Font{Family: "system-ui", Size: 12, Weight: 400}, 20, 3, t.TextMuted)
	})
	if trigger.Clicked() {
		v.runOpen = !v.runOpen
		v.runPage = ""
	}
	n.PopoverBase(c, trigger, &v.runOpen, func(panel n.Element) {
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
			if n.Checkbox(c, &plan, "Plan").Height(28).Disabled(!v.Model.Readiness.Plan).Changed() {
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
				if n.Checkbox(c, &fast, "Fast").Height(28).Changed() {
					cfg.Fast = fast
					v.Model.Configs[key] = cfg
				}
			}
		})
	})
}
func settingRow(c *n.Context, name, value string) n.Element {
	return n.ButtonBase(c).FillWidth().Height(28).PaddingX(8).Radius(5).Label(name).Children(func() {
		n.Row(c).FillWidth().AlignItems(n.Center).Children(func() {
			n.Text(c, name)
			n.Spacer(c).Grow(1)
			n.Text(c, value).SingleLine().MaxWidth(160).Tooltip(value)
		})
	})
}
func (v *View) conversation(c *n.Context, t *n.Theme) {
	m := v.Model
	detail, ok := m.Details[m.SessionID]
	if !ok {
		n.Text(c, "读取会话…").Padding(30)
		return
	}
	state := v.lists[m.SessionID]
	if state == nil {
		state = &n.ListState{FollowEnd: true}
		v.lists[m.SessionID] = state
	}
	rows := v.transcriptRows(detail.Items)
	state.Key = func(i int) any {
		if i == 0 {
			return "history-top"
		}
		if i == len(rows)+1 {
			return "history-bottom"
		}
		return rows[i-1].key
	}
	reserve := v.dockHeight
	if reserve == 0 {
		reserve = 108
	}
	n.List(c.Key("history:"+m.SessionID), state, len(rows)+2, func(row int) {
		if row == 0 {
			n.Column(c).Height(80)
			return
		}
		if row == len(rows)+1 {
			n.Column(c).Height(reserve + 32)
			return
		}
		entry := rows[row-1]
		i := entry.end
		item := entry.item
		bottom := float32(8)
		if item.Kind == "user" {
			bottom = 32
		}
		if i >= 0 && (i == len(detail.Items)-1 || (item.TurnID != "" && detail.Items[i+1].TurnID != item.TurnID)) {
			bottom = 32
		}
		n.Column(c).FillWidth().PaddingX(18).AlignItems(n.Center).Children(func() {
			n.Column(c).FillWidth().MaxWidth(768).Gap(8).Padding(0, 0, bottom, 0).Selectable().Children(func() {
				if entry.group != "" {
					v.toolGroup(c, t, entry)
				} else {
					v.item(c, t, item)
				}
				if i >= 0 && item.TurnID != "" && (i == len(detail.Items)-1 || detail.Items[i+1].TurnID != item.TurnID) {
					for _, turn := range detail.Turns {
						if turn.ID == item.TurnID {
							v.turnFooter(c, t, turn)
						}
					}
				}
			})
		})
	}).Absolute().Left(0).Right(0).Top(0).Bottom(0).Label("对话记录")
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
	dock.Children(func() { v.pending(c, t, detail); v.composer(c, t, false); v.notice(c, t) })
}

func (v *View) item(c *n.Context, t *n.Theme, item domain.Item) {
	switch item.Kind {
	case "user":
		n.Row(c).FillWidth().Justify(n.End).Children(func() {
			n.Text(c, item.Text).MaxWidth(676).Padding(12, 16).Background(userBubble(t)).Radius(14).FontSize(14).FixedLineHeight(24).Selectable()
		})
	case "assistant":
		markdown.View(c.Key(item.ID), item.Text, &v.md).FillWidth().FontSize(14)
	case "tool":
		label := item.Name
		if label == "" {
			label = "工具活动"
		}
		if n.ButtonBase(c).FillWidth().MinHeight(20).Justify(n.Start).Label(label).Children(func() {
			n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
				inlineSymbol(c, terminalIcon, label, n.Font{Family: "Menlo", Size: 11, Weight: 400}, 12, subtle(t))
				n.Text(c, label).Font("Menlo").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			})
		}).Clicked() {
			v.Model.Collapsed["tool:"+item.ID] = !v.Model.Collapsed["tool:"+item.ID]
		}
		if !v.Model.Collapsed["tool:"+item.ID] && item.Text != "" {
			n.Scroll(c.Key("output:" + item.ID)).FillWidth().MaxHeight(160).Children(func() {
				n.Text(c, item.Text).FillWidth().Padding(8, 10).Background(codeSurface(t)).Radius(6).Font("Menlo").FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Selectable()
			})
		}
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
			if item.ID == "request:"+request.ID && request.Kind == "approval" {
				icon = shieldIcon
				break
			}
		}
		if n.ButtonBase(c).FillWidth().MinHeight(28).Justify(n.Start).Label(label).Children(func() {
			n.Row(c).Gap(7).AlignItems(n.Baseline).Children(func() {
				font := n.Font{Family: "system-ui", Size: 11, Weight: 400}
				inlineSymbol(c, icon, label, font, 12, subtle(t))
				n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				if item.Status == "pending" || item.Status == "submitting" {
					inlineSymbol(c, disclosureRight, label, font, 10, subtle(t))
				}
			})
		}).Clicked() {
			v.Model.Collapsed[v.Model.SessionID] = false
		}
	default:
		n.Text(c, item.Text).TextColor(t.TextMuted).FontSize(12)
	}
}
func (v *View) turnFooter(c *n.Context, t *n.Theme, turn domain.Turn) {
	n.Row(c).FillWidth().Wrap().Gap(7).MinHeight(24).AlignItems(n.Baseline).Children(func() {
		label := turn.Status
		switch turn.Status {
		case "completed":
			n.Icon(c, check).Size(12, 12).AlignSelf(n.Center).TextColor(t.Success)
			label = "本轮完成"
		case "running":
			label = "工作中"
		case "interrupted":
			label = "已停止"
		case "failed":
			label = "执行失败"
		case "unknown":
			label = "执行状态待核实"
		}
		n.Text(c, label).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
		if turn.DurationMS != nil {
			n.Row(c).Gap(4).AlignItems(n.Baseline).Children(func() {
				n.Text(c, fmt.Sprintf("%.1f", float64(*turn.DurationMS)/1000)).Font("Menlo").FontSize(11).FixedLineHeight(20).Translate(0, 1).TextColor(t.TextMuted)
				n.Text(c, "秒").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
			})
		}
		if turn.Status == "completed" {
			usage := n.ButtonBase(c).MinHeight(24).Label("本轮用量").Children(func() {
				n.Row(c).Gap(4).AlignItems(n.Baseline).Children(func() {
					for i, field := range []struct {
						name  string
						value *int64
					}{{"输入", turn.InputTokens}, {"输出", turn.OutputTokens}, {"缓存", turn.CachedInputTokens}} {
						if i > 0 {
							n.Text(c, "·").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
						}
						n.Text(c, field.name).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
						n.Text(c, count(field.value)).Font("Menlo").FontSize(11).FixedLineHeight(20).Translate(0, 1).TextColor(t.TextMuted)
					}
				})
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

func (v *View) pending(c *n.Context, t *n.Theme, detail domain.SessionDetail) {
	type page struct {
		request  domain.PendingRequest
		question int
	}
	pages := []page{}
	for _, r := range detail.Requests {
		if r.State != "pending" && r.State != "submitting" {
			continue
		}
		if len(r.Questions) == 0 {
			pages = append(pages, page{r, -1})
		} else {
			for i := range r.Questions {
				pages = append(pages, page{r, i})
			}
		}
	}
	if len(pages) == 0 {
		return
	}
	m := v.Model
	index := m.Pager[m.SessionID]
	if index >= len(pages) {
		index = len(pages) - 1
	}
	m.Pager[m.SessionID] = index
	current := pages[index]
	r := current.request
	n.Column(c.Key("pending")).FillWidth().MaxWidth(768).Padding(12, 14).Gap(8).Radius(requestRadius).Background(t.Surface).Border(1, t.Border).Children(func() {
		n.Row(c).FillWidth().AlignItems(n.Center).Children(func() {
			title := n.ButtonBase(c).Height(24).Grow(1).Radius(requestRadius).Justify(n.Start).Label(r.Title).Children(func() {
				n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
					icon := questionIcon
					if r.Kind == "approval" {
						icon = shieldIcon
					}
					font := n.Font{Family: "system-ui", Size: 12, Weight: 500}
					inlineSymbol(c, icon, r.Title, font, 14, t.TextMuted)
					n.Text(c, r.Title).FontSize(12).FontWeight(500).FixedLineHeight(20)
					inlineSymbol(c, disclosureDown, r.Title, font, 11, t.TextMuted)
				})
			})
			if title.Clicked() {
				m.Collapsed[m.SessionID] = !m.Collapsed[m.SessionID]
			}
			if len(pages) > 1 {
				if ghost(c, "‹").Radius(requestRadius).Label("上一个请求").Disabled(index == 0).Clicked() {
					m.Pager[m.SessionID]--
				}
				n.Text(c, fmt.Sprintf("%d / %d", index+1, len(pages))).FontSize(11)
				if ghost(c, "›").Radius(requestRadius).Label("下一个请求").Disabled(index == len(pages)-1).Clicked() {
					m.Pager[m.SessionID]++
				}
			}
		})
		if m.Collapsed[m.SessionID] {
			return
		}
		n.Scroll(c.Key(r.ID)).FillWidth().MaxHeight(questionBodyHeight(c)).Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() {
				if current.question < 0 {
					n.Text(c, r.Text).Font("Menlo").FontSize(12).Selectable()
					return
				}
				q := r.Questions[current.question]
				key := m.SessionID + "/" + r.ID + "/" + q.ID
				n.Text(c, q.Text).FontSize(12).FixedLineHeight(20)
				selected := textBinding(v.answerBindings, key, m.Answers[key])
				if len(q.Options) > 0 {
					n.Column(c).FillWidth().Padding(0, 8).Gap(2).Radius(requestRadius).Background(raised(t)).Children(func() {
						for _, option := range q.Options {
							n.Radio(c.Key(option.Label), &selected.Text, option.Label, option.Label).FillWidth().MinHeight(28).FontSize(12).Tooltip(option.Description).Changed()
						}
					})
				}
				m.Answers[key] = selected.Text
				selected.LastModel = selected.Text
				if q.Custom || len(q.Options) == 0 {
					custom := textBinding(v.customBindings, key, m.Answers["custom:"+key])
					input := n.TextAreaBase(c.Key("answer:"+key), &custom.Text).FillWidth().Lines(1, 4).MinHeight(32).MaxHeight(96).Padding(5, 8).FontSize(12).FixedLineHeight(20).Background(raised(t)).Border(1, t.Border).Radius(requestRadius).Label("自定义答案").Placeholder("输入答案…").PlaceholderColor(subtle(t))
					if input.Changed() {
						m.Answers["custom:"+key] = custom.Text
						m.Answers[key] = custom.Text
						custom.LastModel = custom.Text
					}
				}
			})
		})
		n.Row(c).Gap(8).Children(func() {
			disabled := !m.Connected || r.State != "pending"
			if r.Kind == "approval" {
				if n.PrimaryButton(c, "允许本轮").Radius(requestRadius).Height(28).FontSize(12).Disabled(disabled).Clicked() {
					v.respond(r, domain.Answer{RequestID: r.ID, Revision: r.Revision, Decision: "accept"})
				}
				if ghost(c, "拒绝").Radius(requestRadius).Disabled(disabled).Clicked() {
					v.respond(r, domain.Answer{RequestID: r.ID, Revision: r.Revision, Decision: "decline"})
				}
			} else {
				answers := map[string][]string{}
				complete := true
				for _, q := range r.Questions {
					answer := m.Answers[m.SessionID+"/"+r.ID+"/"+q.ID]
					if strings.TrimSpace(answer) == "" {
						complete = false
					}
					answers[q.ID] = []string{answer}
				}
				if n.PrimaryButton(c, "提交答案").Radius(requestRadius).Height(28).FontSize(12).Disabled(disabled || !complete).Clicked() {
					v.respond(r, domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: answers})
				}
			}
		})
	})
}
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
func (v *View) submit() {
	key, _ := v.gitQuery()
	if v.gitPending[key] {
		v.Model.Error = "正在核对设备上的分支，请稍后提交"
		return
	}
	if v.Controller == nil {
		return
	}
	m := v.Model
	in, err := m.BeginSubmission()
	if err != nil {
		m.Error = err.Error()
		return
	}
	var prepare func() error
	if v.Saver != nil {
		state, _ := m.EncodeState()
		v.Saver.Queue(state)
		v.lastQueued = state
		prepare = v.Saver.FlushLatest
	}
	if !v.do(app.Job{Prepare: prepare, Method: "session.submit", Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if err != nil {
			m.SubmissionError(in, err)
			return
		}

		var op domain.Operation
		if err = json.Unmarshal(b, &op); err != nil {
			m.Error = err.Error()
			return
		}
		m.Accepted(in, op)
	}}) {
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
func (v *View) createWorkspace() {
	if v.Controller == nil {
		return
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择工作区目录", Directory: true, Multiple: true})
	if err != nil {
		v.Model.Error = err.Error()
		return
	}
	if len(paths) == 0 {
		return
	}
	v.do(app.Job{Method: "workspace.create", Input: protocol.WorkspaceInput{Name: filepath.Base(paths[0]), Folders: paths}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if err != nil {
			m.Error = err.Error()
			return
		}
		var w domain.Workspace
		if json.Unmarshal(b, &w) == nil {
			m.WorkspaceID = w.ID
			m.SessionID = ""
		}
	}})
}
func (v *View) nextAttention() {
	for _, s := range v.Model.Snapshot.Sessions {
		if v.pendingCount(s.ID) > 0 {
			v.Model.SessionID = s.ID
			v.Model.WorkspaceID = s.WorkspaceID
			v.Model.Collapsed[s.ID] = false
			v.devices = false
			return
		}
	}
	v.Model.Error = "暂无待处理事项"
}
func (v *View) deviceView(c *n.Context, t *n.Theme) {
	m := v.Model
	n.Row(c).Fill().Children(func() {
		n.Column(c).Width(200).FillHeight().Padding(12).Background(t.Surface).Children(func() {
			n.Text(c, "设备").FontSize(13).Padding(8)
			ghost(c, "这台 Mac").FillWidth().TextAlign(n.Start).Background(t.SurfacePressed)
		})
		n.Column(c).Grow(1).FillHeight().Padding(32).Gap(16).Children(func() {
			n.Text(c, "这台 Mac").FontSize(24)
			state := "未连接"
			if m.Connected {
				state = "已连接"
			}
			n.Text(c, state)
			n.Text(c, "Codex · "+m.Readiness.State).TextColor(t.TextMuted)
			if m.Readiness.Error != "" {
				n.Text(c, m.Readiness.Error).MaxWidth(560)
			}
			for _, w := range m.Snapshot.Workspaces {
				if ghost(c, w.Name).Clicked() {
					m.WorkspaceID = w.ID
					v.home()
				}
			}
		})
	})
}

func (v *View) do(job app.Job) bool {
	if v.Controller == nil {
		return false
	}
	job.ConnectionEpoch = v.Model.ConnectionEpoch
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
