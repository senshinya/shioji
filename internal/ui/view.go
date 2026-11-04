// Package ui renders Shioji with MyGo's native components; no WebView is used.
package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type View struct {
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
}

func New(model *app.Model, controller *app.Controller, path string) *View {
	return &View{Model: model, Controller: controller, StatePath: path, lists: map[string]*n.ListState{}, collapsedWorkspaces: map[string]bool{}, md: markdown.Options{ShareSelection: true, WrapCode: true}}
}
func svg(path string) *n.SVG {
	return n.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><path d="` + path + `" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
}

var plus = svg("M10 4v12M4 10h12")
var chevron = svg("M6 8l4 4 4-4")
var monitor = svg("M3 3h14v10H3zM7 17h6M10 13v4")
var check = svg("M4 10l4 4 8-9")
var attention = svg("M10 3l8 14H2zM10 7v4M10 14v.2")
var arrow = n.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><path d="M8.75 17V6.4L4.1 11.05 2.35 9.3 10 1.65l7.65 7.65-1.75 1.75-4.65-4.65V17z" fill="currentColor"/></svg>`))
var stop = n.MustParseSVG([]byte(`<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><rect x="4" y="4" width="12" height="12" rx="2" fill="currentColor"/></svg>`))

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
	t.Spacing = 3
	t.Radius = 6
	if t.Dark {
		t.Background = n.Hex("#1b1b1c")
		t.Surface = n.Hex("#272728")
		t.SurfaceHover = n.Hex("#303031")
		t.SurfacePressed = n.Hex("#39393a")
		t.Border = n.Hex("#39393a")
		t.Text = n.Hex("#eeeeef")
		t.TextMuted = n.Hex("#a1a1a4")
		t.Accent = n.Hex("#e5e5e7")
		t.AccentText = n.Hex("#202021")
		t.Selection = n.Hex("#4b4b4e")
		t.Focus = n.Hex("#8c8c90")
		t.Success = n.Hex("#32d769")
		t.Warning = n.Hex("#ff9f0a")
	} else {
		t.Background = n.Hex("#ffffff")
		t.Surface = n.Hex("#f4f4f5")
		t.SurfaceHover = n.Hex("#eaeaec")
		t.SurfacePressed = n.Hex("#e1e1e3")
		t.Border = n.Hex("#dedee1")
		t.Text = n.Hex("#28282b")
		t.TextMuted = n.Hex("#737377")
		t.Accent = n.Hex("#29292b")
		t.AccentText = n.Hex("#ffffff")
		t.Selection = n.Hex("#dcdcdf")
		t.Focus = n.Hex("#97979b")
		t.Success = n.Hex("#138a43")
		t.Warning = n.Hex("#c66a00")
	}
	t.AccentHover = t.Accent
	t.AccentPressed = t.Accent
	c.SetTheme(&t)
	if v.Controller != nil && v.StatePath != "" {
		data, err := m.EncodeState()
		if err == nil && !bytes.Equal(data, v.lastState) {
			if v.Controller.Do(app.Job{Method: "$save", StatePath: v.StatePath, State: data}) {
				v.lastState = data
			}
		}
	}

	if c.Shortcut(n.Cmd, n.KeyN) {
		v.home()
	}
	n.Row(c).Fill().Background(t.Background).Children(func() {
		v.sidebar(c, &t)
		n.Column(c).FillHeight().Grow(1).Children(func() {
			v.toolbar(c, &t)
			if v.devices {
				v.deviceView(c, &t)
			} else if m.SessionID == "" {
				v.homeView(c, &t)
			} else {
				v.conversation(c, &t)
			}
		})
	})
}
func (v *View) home() {
	v.Model.SessionID = ""
	v.devices = false
	v.runOpen = false
	v.permissionOpen = false
}
func iconButton(c *n.Context, icon *n.SVG, label string) n.Element {
	return n.ButtonBase(c).Size(28, 28).Radius(6).Label(label).Tooltip(label).Children(func() { n.Icon(c, icon).Size(16, 16) })
}
func ghost(c *n.Context, label string) n.Element {
	return n.Button(c, label).Background(n.Transparent).Border(0, n.Transparent).Height(28)
}
func (v *View) sidebar(c *n.Context, t *n.Theme) {
	m := v.Model
	n.Column(c).Width(248).FillHeight().Padding(8).Children(func() {
		n.Column(c).Fill().Padding(8).Gap(4).Radius(14).Background(t.Surface).Border(1, t.Border).Children(func() {
			n.Row(c).Height(34).DragWindow().Children(func() { n.Spacer(c).Grow(1) })
			if ghost(c, "＋  新建会话").FillWidth().TextAlign(n.Start).Background(func() n.Color {
				if m.SessionID == "" && !v.devices {
					return t.SurfacePressed
				}
				return n.Transparent
			}()).Clicked() {
				v.home()
			}
			if ghost(c, "△  待我处理").FillWidth().TextAlign(n.Start).Clicked() {
				v.nextAttention()
			}
			n.Row(c).Height(32).AlignItems(n.Center).Children(func() {
				n.Text(c, "工作区").FontSize(11).TextColor(t.TextMuted).Grow(1)
				if iconButton(c, plus, "创建工作区").Disabled(!m.Connected).Clicked() {
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
					n.Row(c).Height(30).AlignItems(n.Center).Children(func() {
						label := "⌄  " + w.Name
						if v.collapsedWorkspaces[w.ID] {
							label = "›  " + w.Name
						}
						if ghost(c, label).Grow(1).TextAlign(n.Start).Clicked() {
							v.collapsedWorkspaces[w.ID] = !v.collapsedWorkspaces[w.ID]
						}
						if iconButton(c, plus, "在 "+w.Name+" 新建会话").Clicked() {
							m.WorkspaceID = w.ID
							v.home()
						}
					})
					return
				}
				s := r.session
				selected := s.ID == m.SessionID && !v.devices
				row := n.ButtonBase(c).FillWidth().Height(30).PaddingX(10).Radius(7).Label(s.Title)
				if selected {
					row.Background(t.SurfacePressed)
				}
				row.Children(func() {
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
						if s.Status == "running" || s.Status == "starting" {
							n.Column(c).Size(4, 4).Radius(2).Background(t.Success)
						} else if v.pendingCount(s.ID) > 0 {
							n.Column(c).Size(4, 4).Radius(2).Background(t.Warning)
						} else {
							n.Icon(c, check).Size(10, 10).TextColor(t.TextMuted)
						}
						n.Text(c, s.Title).SingleLine().Grow(1).FontSize(13)
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
			n.Row(c).Height(32).Children(func() {
				if ghost(c, "设备").Grow(1).TextAlign(n.Start).Clicked() {
					v.devices = true
				}
				ghost(c, "设置").Disabled(true)
			})
		})
	})
}
func (v *View) toolbar(c *n.Context, t *n.Theme) {
	m := v.Model
	n.Row(c).Height(48).FillWidth().PaddingX(24).AlignItems(n.Center).DragWindow().Children(func() {
		if m.SessionID != "" {
			for _, s := range m.Snapshot.Sessions {
				if s.ID == m.SessionID {
					n.Text(c, s.Title).SingleLine().Grow(1)
				}
			}
		} else {
			n.Spacer(c).Grow(1)
		}
		device := n.ButtonBase(c).Height(26).PaddingX(6).Radius(6).Label("当前设备")
		device.Children(func() {
			n.Row(c).Gap(6).AlignItems(n.Center).Children(func() {
				color := t.TextMuted
				if m.Connected {
					color = t.Success
				}
				n.Column(c).Size(4, 4).Radius(2).Background(color)
				n.Icon(c, monitor).Size(14, 14)
				n.Text(c, "这台 Mac").FontSize(11).TextColor(t.TextMuted)
				n.Icon(c, chevron).Size(12, 12)
			})
		})
		device.PopoverMenuButton(func(menu *n.Menu) {
			status := "已连接"
			if !m.Connected {
				status = "未连接"
			}
			menu.Item(status).Disabled(true)
			menu.Item("Codex · " + m.Readiness.State).Disabled(true)
			menu.Separator()
			if menu.Item("设备详情").Chosen() {
				v.devices = true
			}
		})
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
		n.Spacer(c).Grow(.65)
		if len(v.Model.Snapshot.Workspaces) == 0 {
			n.Text(c, "从一个工作区开始").FontSize(28)
			if ghost(c, "创建工作区").Margin(18, 0, 0, 0).Disabled(!v.Model.Connected).Clicked() {
				v.createWorkspace()
			}
		} else {
			n.Row(c).Gap(12).Margin(0, 0, 26, 0).AlignItems(n.Center).Children(func() {
				n.Text(c, "想在").FontSize(30).TextColor(t.TextMuted)
				n.MenuButton(c, v.workspaceName()+" ⌄", func(menu *n.Menu) {
					for _, w := range v.Model.Snapshot.Workspaces {
						if menu.Item(w.Name + "   ·   这台 Mac").Radio(w.ID == v.Model.WorkspaceID).Chosen() {
							v.Model.WorkspaceID = w.ID
						}
					}
				}).FontSize(30).Background(n.Transparent).Border(0, n.Transparent)
				n.Text(c, "做些什么？").FontSize(30).TextColor(t.TextMuted)
			})
			v.composer(c, t, true)
		}
		v.notice(c, t)
		n.Spacer(c).Grow(1.2)
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
	draft := m.Drafts[key]
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
	n.Column(c.Key("composer:"+key)).FillWidth().MaxWidth(736).Padding(12, 14).Gap(8).Background(bg).Border(1, t.Border).Radius(16).Shadow(0, 4, 14, 0, n.RGBA(0, 0, 0, .04)).Children(func() {
		n.TextAreaBase(c.Key("input"), &draft).Label("消息输入").Placeholder("描述任务…").FillWidth().Lines(3, 7).MinHeight(72).FontSize(14).OnTextKey(func(k n.TextKey) bool {
			if k.Key == n.KeyEnter && (k.Mods == 0 || k.Mods == n.Cmd) {
				m.Drafts[key] = k.Text()
				if strings.TrimSpace(k.Text()) != "" {
					v.submit()
				}
				return true
			}
			return false
		})
		m.Drafts[key] = draft
		n.Row(c).FillWidth().Gap(6).AlignItems(n.Center).Children(func() {
			permission := ghost(c, permissionLabel(cfg.Permission)+" ⌄").Label("Permission")
			permission.PopoverMenuButton(func(menu *n.Menu) {
				for _, id := range m.Readiness.Permissions {
					if menu.Item(permissionLabel(id)).Radio(cfg.Permission == id).Chosen() {
						cfg.Permission = id
						m.Configs[key] = cfg
					}
				}
			})
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
			send := n.ButtonBase(c).Size(28, 28).Radius(14).Background(t.Accent).TextColor(t.AccentText).Label(label).Disabled(disabled).Children(func() { n.Icon(c, icon).Size(20, 20) })
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
		return "Read only"
	case "auto-approve":
		return "Auto Approve"
	case "yolo":
		return "YOLO"
	default:
		return "Default"
	}
}
func (v *View) runControl(c *n.Context, t *n.Theme, key string, cfg domain.RunConfig) {
	summary := "Codex · " + cfg.Model
	if cfg.Effort != "" {
		summary += " · " + cfg.Effort
	}
	if cfg.Plan {
		summary += " · Plan"
	}
	if cfg.Fast {
		summary += " · Fast"
	}
	trigger := ghost(c, summary+" ⌄").MaxWidth(390).Tooltip(summary).Label("运行配置")
	if trigger.Clicked() {
		v.runOpen = !v.runOpen
		v.runPage = ""
	}
	n.PopoverBase(c, trigger, &v.runOpen, func(panel n.Element) {
		panel.Width(240).Padding(4).Radius(9).Background(t.Surface).Border(1, t.Border).Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14)).Children(func() {
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
	state.Key = func(i int) any { return detail.Items[i].ID }
	n.Column(c).Fill().PaddingX(32).Gap(12).Children(func() {
		n.List(c.Key("history:"+m.SessionID), state, len(detail.Items), func(i int) {
			item := detail.Items[i]
			n.Column(c).FillWidth().MaxWidth(736).PaddingY(4).Gap(8).Selectable().Children(func() {
				v.item(c, t, item)
				if item.TurnID != "" && (i == len(detail.Items)-1 || detail.Items[i+1].TurnID != item.TurnID) {
					for _, turn := range detail.Turns {
						if turn.ID == item.TurnID {
							v.turnFooter(c, t, turn)
						}
					}
				}
			})
		}).FillWidth().Grow(1).FillHeight().Label("对话记录")
		n.Column(c).FillWidth().AlignItems(n.Center).Gap(8).Padding(0, 0, 20, 0).Children(func() { v.pending(c, t, detail); v.composer(c, t, false); v.notice(c, t) })
	})
}
func (v *View) item(c *n.Context, t *n.Theme, item domain.Item) {
	switch item.Kind {
	case "user":
		n.Row(c).FillWidth().Justify(n.End).Children(func() {
			n.Text(c, item.Text).MaxWidth(600).Padding(10, 14).Background(t.Surface).Radius(12).FontSize(14).Selectable()
		})
	case "assistant":
		markdown.View(c.Key(item.ID), item.Text, &v.md).FillWidth().FontSize(14)
	case "tool":
		label := "›  " + item.Name
		if label == "›  " {
			label = "›  工具活动"
		}
		if ghost(c, label).FillWidth().TextAlign(n.Start).Tooltip(item.Name).Clicked() {
			v.Model.Collapsed["tool:"+item.ID] = !v.Model.Collapsed["tool:"+item.ID]
		}
		if !v.Model.Collapsed["tool:"+item.ID] && item.Text != "" {
			n.Text(c, item.Text).FillWidth().Padding(8, 10).Background(t.Surface).Radius(6).Font("Menlo").FontSize(11).Selectable()
		}
	case "request":
		label := item.Text + " · " + item.Status
		if ghost(c, label).FillWidth().TextAlign(n.Start).Clicked() {
			v.Model.Collapsed[v.Model.SessionID] = false
		}
	default:
		n.Text(c, item.Text).TextColor(t.TextMuted).FontSize(12)
	}
}
func (v *View) turnFooter(c *n.Context, t *n.Theme, turn domain.Turn) {
	n.Row(c).Gap(6).AlignItems(n.Center).Children(func() {
		label := turn.Status
		switch turn.Status {
		case "completed":
			n.Icon(c, check).Size(12, 12).TextColor(t.Success)
			label = "本轮完成"
		case "running":
			label = "工作中"
		case "interrupted":
			label = "已停止"
		case "failed":
			label = "执行失败"
		}
		n.Text(c, label).FontSize(11).TextColor(t.TextMuted)
		if turn.DurationMS != nil {
			n.Text(c, fmt.Sprintf("%.1f 秒", float64(*turn.DurationMS)/1000)).FontSize(11).TextColor(t.TextMuted)
		}
		if turn.Status == "completed" {
			n.Text(c, "输入 "+count(turn.InputTokens)+" · 输出 "+count(turn.OutputTokens)+" · 缓存 "+count(turn.CachedInputTokens)).FontSize(11).TextColor(t.TextMuted)
		}
	})
}
func count(value *int64) string {
	if value == nil {
		return "—"
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
	n.Column(c.Key("pending")).FillWidth().MaxWidth(736).Padding(12, 14).Gap(10).Radius(12).Background(t.Surface).Children(func() {
		n.Row(c).FillWidth().AlignItems(n.Center).Children(func() {
			if ghost(c, r.Title).TextAlign(n.Start).Grow(1).Clicked() {
				m.Collapsed[m.SessionID] = !m.Collapsed[m.SessionID]
			}
			if len(pages) > 1 {
				if ghost(c, "‹").Label("上一个请求").Disabled(index == 0).Clicked() {
					m.Pager[m.SessionID]--
				}
				n.Text(c, fmt.Sprintf("%d / %d", index+1, len(pages))).FontSize(11)
				if ghost(c, "›").Label("下一个请求").Disabled(index == len(pages)-1).Clicked() {
					m.Pager[m.SessionID]++
				}
			}
		})
		if m.Collapsed[m.SessionID] {
			return
		}
		n.Scroll(c.Key(r.ID)).FillWidth().MaxHeight(180).Children(func() {
			n.Column(c).FillWidth().Padding(10).Gap(8).Background(t.Background).Radius(6).Children(func() {
				if current.question < 0 {
					n.Text(c, r.Text).Font("Menlo").FontSize(12).Selectable()
					return
				}
				q := r.Questions[current.question]
				key := m.SessionID + "/" + r.ID + "/" + q.ID
				n.Text(c, q.Text).FontSize(14)
				selected := m.Answers[key]
				for _, option := range q.Options {
					n.Radio(c.Key(option.Label), &selected, option.Label, option.Label).FillWidth().MinHeight(28).Tooltip(option.Description)
				}
				m.Answers[key] = selected
				if q.Custom || len(q.Options) == 0 {
					custom := m.Answers["custom:"+key]
					input := n.TextArea(c.Key("answer:"+key), &custom).FillWidth().Lines(1, 4).Label("自定义答案").Placeholder("输入答案…")
					if input.Changed() {
						m.Answers["custom:"+key] = custom
						m.Answers[key] = custom
					}
				}
			})
		})
		n.Row(c).Gap(8).Children(func() {
			disabled := !m.Connected || r.State != "pending"
			if r.Kind == "approval" {
				if n.Button(c, "允许本轮").Disabled(disabled).Clicked() {
					v.respond(r, domain.Answer{RequestID: r.ID, Revision: r.Revision, Decision: "accept"})
				}
				if ghost(c, "拒绝").Disabled(disabled).Clicked() {
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
				if n.Button(c, "提交答案").Disabled(disabled || !complete).Clicked() {
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
	v.Controller.Do(app.Job{Method: "request.respond", Input: protocol.AnswerInput{SessionID: r.SessionID, Answer: answer}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if err != nil {
			m.Error = err.Error()
		}
	}})
}
func (v *View) submit() {
	if v.Controller == nil {
		return
	}
	m := v.Model
	in, err := m.BeginSubmission()
	if err != nil {
		m.Error = err.Error()
		return
	}
	state, _ := m.EncodeState()
	if !v.Controller.Do(app.Job{StatePath: v.StatePath, State: state, Method: "session.submit", Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) {
				m.Reject(in, err.Error())
			} else {
				m.Error = "提交结果待核实，草稿已保留"
			}
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
			v.Controller.Do(app.Job{Method: "turn.interrupt", Input: map[string]string{"sessionId": d.Session.ID, "turnId": turn.ID}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
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
	v.Controller.Do(app.Job{Method: "workspace.create", Input: protocol.WorkspaceInput{Name: filepath.Base(paths[0]), Folders: paths}, Apply: func(m *app.Model, b json.RawMessage, err error) {
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
