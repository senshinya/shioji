package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"math"
	"path"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"sort"
	"strconv"
	"strings"
	"time"
)

type workspaceToolsUI struct {
	slot, key, device, harness, workspace, epoch, installation string
	revision                                                   int64
	directory, session, query, detailID, message               string
	sourceContext                                              string
	phase                                                      toolServiceLoad
	request                                                    uint64
	catalog                                                    domain.ToolServiceInspection
	listScroll, detailScroll                                   n.ScrollState
	copiedUntil                                                time.Time
}

func (v *View) workspaceToolsFor(e *workspaceEditor, harness string) *workspaceToolsUI {
	if v.workspaceTools == nil {
		v.workspaceTools = map[string]*workspaceToolsUI{}
	}
	state := v.deviceState(e.Connection)
	device := v.signInDevice(e.Connection)
	slot := device + "/" + harness + "/" + e.Workspace.ID
	revision := int64(0)
	main := ""
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == e.Workspace.ID {
			revision = w.Revision
			if len(w.Folders) > 0 {
				main = w.Folders[0]
			}
		}
	}
	installation := ""
	for _, info := range state.Readiness.Harnesses {
		if info.ID == harness {
			b, _ := json.Marshal(info.Installation)
			installation = string(b)
		}
	}
	u := v.workspaceTools[slot]
	if u == nil || u.epoch != state.ConnectionEpoch || u.key != e.Connection || u.revision != revision || u.installation != installation {
		next := &workspaceToolsUI{slot: slot, key: e.Connection, device: device, harness: harness, workspace: e.Workspace.ID, epoch: state.ConnectionEpoch, revision: revision, installation: installation, directory: main}
		if u != nil {
			for _, w := range v.Model.Snapshot.Workspaces {
				if w.ID == u.workspace {
					for _, dir := range w.Folders {
						if dir == u.directory {
							next.directory = dir
						}
					}
				}
			}
			next.session = u.session
			next.query = u.query
			next.catalog = u.catalog
			next.detailID = u.detailID
			next.listScroll = u.listScroll
			next.detailScroll = u.detailScroll
			next.catalog.RuntimeKnown = false
		}
		v.workspaceTools[slot] = next
		u = next
	}
	if !state.Connected {
		u.catalog.RuntimeKnown = false
	}
	return u
}

func (v *View) workspaceToolSessionContext(u *workspaceToolsUI) string {
	if u.session == "" {
		return ""
	}
	for _, s := range v.Model.Snapshot.Sessions {
		if s.ID == u.session && s.WorkspaceID == u.workspace && s.HarnessID == u.harness && !s.Archived {
			b, _ := json.Marshal(struct {
				Ref     string
				Context domain.LaunchContext
			}{s.ThreadID, s.Context})
			return string(b)
		}
	}
	return "unavailable"
}
func (v *View) workspaceToolsCurrent(u *workspaceToolsUI) bool {
	if v.workspaceTools[u.slot] != u || v.deviceState(u.key).ConnectionEpoch != u.epoch || v.signInDevice(u.key) != u.device {
		return false
	}
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == u.workspace && w.Revision == u.revision {
			for _, info := range v.deviceState(u.key).Readiness.Harnesses {
				if info.ID == u.harness {
					b, _ := json.Marshal(info.Installation)
					return string(b) == u.installation
				}
			}
		}
	}
	return false
}
func (v *View) selectWorkspaceToolScope(u *workspaceToolsUI, session, directory string) {
	u.request++
	u.session = session
	if directory != "" {
		u.directory = directory
	}
	u.phase = toolServiceIdle
	u.catalog = domain.ToolServiceInspection{}
	u.detailID = ""
	u.message = ""
	u.listScroll = n.ScrollState{}
	u.detailScroll = n.ScrollState{}
}
func (v *View) queryWorkspaceTools(u *workspaceToolsUI) {
	if u.phase == toolServiceLoading || v.Controller == nil || !v.deviceState(u.key).Connected {
		return
	}
	if v.deviceState(u.key).WorkerMinor < 26 {
		u.phase = toolServiceLoadFailed
		u.message = "更新设备执行服务后才能查看工作区 MCP"
		return
	}
	u.request++
	request := u.request
	sourceContext := v.workspaceToolSessionContext(u)
	u.sourceContext = sourceContext
	u.phase = toolServiceLoading
	u.message = ""
	in := protocol.ToolInspectionInput{HarnessID: u.harness, WorkspaceID: u.workspace, Directory: u.directory, SessionID: u.session}
	if in.SessionID != "" {
		in.Directory = ""
	}
	if !v.do(app.Job{ConnectionID: u.key, Method: "runtime.tools.inspect", Input: in, Apply: func(_ *app.Model, b json.RawMessage, err error) {
		if !v.workspaceToolsCurrent(u) || u.request != request || v.workspaceToolSessionContext(u) != sourceContext {
			return
		}
		var result domain.ToolServiceInspection
		if err == nil {
			err = json.Unmarshal(b, &result)
		}
		if err != nil {
			u.phase = toolServiceLoadFailed
			u.message = "无法核对 MCP，显示上次结果。请刷新重试"
			return
		}
		u.catalog = result
		u.phase = toolServiceLoaded
	}}) {
		u.phase = toolServiceLoadFailed
		u.message = "设备操作繁忙，请重试"
	}
}

func (v *View) workspaceToolsView(c *n.Context, t *n.Theme, e *workspaceEditor) {
	if v.deviceState(e.Connection).WorkerMinor < 26 {
		n.Text(c, "更新设备执行服务后才能查看工作区 MCP").FontSize(12).TextColor(t.TextMuted)
		return
	}
	var agents []domain.HarnessInfo
	for _, info := range v.deviceState(e.Connection).Readiness.Harnesses {
		if info.Configured && info.Capabilities.ToolServiceInspection {
			agents = append(agents, info)
		}
	}
	if len(agents) == 0 {
		n.Text(c, "此设备暂无支持 MCP 来源检查的 Agent").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if !containsHarness(agents, e.Harness) {
		e.Harness = agents[0].ID
	}
	u := v.workspaceToolsFor(e, e.Harness)
	if context := v.workspaceToolSessionContext(u); context != u.sourceContext {
		u.sourceContext = context
		u.request++
		u.phase = toolServiceIdle
		u.catalog.RuntimeKnown = false
	}
	if u.phase == toolServiceIdle {
		v.queryWorkspaceTools(u)
	}
	page, index := "list", 0
	if u.detailID != "" {
		page, index = "detail", 1
	}
	configurationTabPanel(c.Key("workspace-tools:"+u.slot), page, index, configurationPanelFill, func() {
		if u.detailID != "" {
			v.workspaceToolDetail(c, t, u)
			return
		}
		n.Column(c).FillWidth().FillHeight().Grow(1).Children(func() {
			n.Row(c).FillWidth().Height(36).Padding(0, 0, 8).Gap(8).AlignItems(n.Center).Children(func() {
				textField(c.Key("search"), &u.query).Height(28).MaxWidth(300).Grow(1).FontSize(12).Placeholder("搜索 MCP").Label("搜索 MCP").Background(t.Background)
				n.Spacer(c).Grow(1)
				for _, info := range agents {
					if info.ID == e.Harness {
						trigger := control(c.Key("harness"), 5).Height(28).Padding(0).Label("MCP Agent").Children(func() {
							inlineIconText(c, harnessIcon(domain.HarnessDescriptor{ID: info.ID, Name: info.Name, Icon: info.Icon}), info.Name, n.Font{Family: t.Font, Size: 12}, 14, 6, t.TextMuted)
						})
						if len(agents) > 1 {
							var choices []menuChoice
							for _, a := range agents {
								choices = append(choices, menuChoice{id: a.ID, name: a.Name, checked: a.ID == e.Harness, choose: func() { e.Harness = a.ID }})
							}
							v.choiceMenu(c, t, "workspace-tools-harness", trigger, choices)
						}
					}
				}
				refresh := control(c.Key("refresh"), 6).Size(28, 28).MarginEnd(-6).Label("刷新工作区 MCP").Disabled(u.phase == toolServiceLoading || !v.deviceState(u.key).Connected).Children(func() {
					glyph := n.Icon(c.Key("glyph"), refreshIcon).Size(16, 16)
					started := n.Local(glyph, "rotation-start", func() time.Time { return c.Now() })
					if u.phase == toolServiceLoading && !c.Preferences().ReduceMotion {
						angle := float32(math.Mod(c.Now().Sub(*started).Seconds()/1.3, 1) * 360)
						glyph.Rotate(angle)
						c.After(time.Second / 60)
					} else {
						*started = c.Now()
					}
				})
				if refresh.Clicked() {
					v.queryWorkspaceTools(u)
				}
			})
			v.workspaceToolScope(c, t, u)
			v.workspaceToolFeedback(c, t, u)
			n.Scroll(c.Key("list:" + u.slot)).FillWidth().FillHeight().Grow(1).TrackScroll(&u.listScroll).Label("工作区 MCP 列表").Children(func() { v.workspaceToolRows(c, t, u) })
		})
	}, false)
}
func (v *View) workspaceToolScope(c *n.Context, t *n.Theme, u *workspaceToolsUI) {
	n.Row(c).FillWidth().Height(36).Margin(0, 0, 8, 0).Gap(8).AlignItems(n.Center).Children(func() {
		label := "配置来源"
		if u.session != "" {
			label = "会话已不可用"
		}
		var choices []menuChoice
		choices = append(choices, menuChoice{id: "config", name: "配置来源", checked: u.session == "", choose: func() { v.selectWorkspaceToolScope(u, "", "") }})
		for _, s := range v.Model.Snapshot.Sessions {
			if s.WorkspaceID == u.workspace && s.HarnessID == u.harness && !s.Archived {
				if s.ID == u.session {
					label = s.Title
				}
				choices = append(choices, menuChoice{id: s.ID, name: s.Title, checked: s.ID == u.session, choose: func() { v.selectWorkspaceToolScope(u, s.ID, "") }})
			}
		}
		trigger := control(c.Key("view"), 5).Height(28).Padding(0).Label("MCP 查看范围").MaxWidth(300).Children(func() {
			n.Row(c).Gap(4).AlignItems(n.Baseline).Children(func() {
				n.Text(c, label).FontSize(12).FixedLineHeight(20).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				inlineSymbol(c, chevron, label, n.Font{Family: t.Font, Size: 12}, 12, t.TextMuted)
			})
		})
		v.choiceMenu(c, t, "workspace-tools-view:"+u.slot, trigger, choices)
		n.Spacer(c).Grow(1)
		if u.session == "" {
			label := path.Base(u.directory)
			trigger := control(c.Key("directory"), 5).Height(28).MaxWidth(240).Padding(0).Label("MCP 查询目录").Tooltip(u.directory).Children(func() {
				n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
					inlineIconText(c, workspaceIcon, label, n.Font{Family: t.Font, Size: 12}, 14, 6, t.TextMuted).MinWidth(0).Shrink(1).SingleLine().Ellipsis("…")
					inlineSymbol(c, chevron, label, n.Font{Family: t.Font, Size: 12}, 12, t.TextMuted)
				})
			})
			var dirs []menuChoice
			for _, w := range v.Model.Snapshot.Workspaces {
				if w.ID == u.workspace {
					for _, folder := range w.Folders {
						dirs = append(dirs, menuChoice{id: folder, name: path.Base(folder), detail: folder, checked: folder == u.directory, choose: func() { v.selectWorkspaceToolScope(u, "", folder) }})
					}
				}
			}
			v.choiceMenu(c, t, "workspace-tools-directory:"+u.slot, trigger, dirs)
		} else {
			n.Text(c, u.catalog.Directory).FontSize(11).TextColor(t.TextMuted).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Tooltip(u.catalog.Directory)
		}
	})
}
func (v *View) workspaceToolFeedback(c *n.Context, t *n.Theme, u *workspaceToolsUI) {
	message := u.message
	if !v.deviceState(u.key).Connected {
		message = "设备未连接，显示上次结果"
	}
	if message == "" && len(u.catalog.Warnings) > 0 {
		message = strings.Join(u.catalog.Warnings, "\n")
	}
	toolConfigurationFeedback(c, t, message)
}
func workspaceToolScopeName(scope domain.ToolServiceScope) string {
	switch scope {
	case domain.ToolServiceProject:
		return "项目级"
	case domain.ToolServiceUser:
		return "用户级"
	case domain.ToolServiceManaged:
		return "受管理"
	default:
		return "其他来源"
	}
}
func workspaceToolState(u *workspaceToolsUI, e domain.InspectedToolService) string {
	if e.IgnoredReason != "" {
		return "未加载"
	}
	if u.session == "" {
		if e.Configuration != nil && !e.Configuration.Enabled {
			return "已停用"
		}
		return "已配置"
	}
	if u.phase != toolServiceLoaded || !u.catalog.RuntimeKnown {
		return "状态未知"
	}
	switch e.State {
	case domain.ToolServiceReady:
		return "已连接"
	case domain.ToolServiceDisabled, domain.ToolServiceNotLoaded:
		return "未加载"
	case domain.ToolServiceAuthorizationRequired:
		return "需要授权"
	case domain.ToolServiceConnecting:
		return "连接中"
	case domain.ToolServiceFailed:
		return "连接失败"
	default:
		return "状态未知"
	}
}
func toolConnectionLabel(e domain.InspectedToolService) string {
	cfg := e.Configuration
	if cfg == nil {
		return ""
	}
	if cfg.Transport == "http" {
		return "HTTP · " + cfg.URL
	}
	if cfg.Transport == "command" {
		return "stdio · " + strings.Join(append([]string{cfg.Command}, cfg.Arguments...), " ")
	}
	return "连接类型未报告"
}
func (v *View) workspaceToolRows(c *n.Context, t *n.Theme, u *workspaceToolsUI) {
	entries := append([]domain.InspectedToolService(nil), u.catalog.Entries...)
	order := func(s domain.ToolServiceScope) int {
		switch s {
		case domain.ToolServiceProject:
			return 0
		case domain.ToolServiceUser:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Scope != b.Scope {
			return order(a.Scope) < order(b.Scope)
		}
		if a.ConfigurationPath != b.ConfigurationPath {
			return a.ConfigurationPath < b.ConfigurationPath
		}
		return a.Name < b.Name
	})
	group := ""
	count := 0
	query := strings.ToLower(strings.TrimSpace(u.query))
	for _, entry := range entries {
		if !strings.Contains(strings.ToLower(entry.Name+" "+entry.ConfigurationPath+" "+toolConnectionLabel(entry)), query) {
			continue
		}
		count++
		if u.session == "" && string(entry.Scope)+entry.ConfigurationPath != group {
			group = string(entry.Scope) + entry.ConfigurationPath
			n.Row(c.Key("group:"+group)).FillWidth().Height(36).Margin(12, 0, 0, 0).Gap(7).AlignItems(n.Baseline).Children(func() {
				inlineSymbol(c, workspaceIcon, entry.ConfigurationPath, n.Font{Family: t.Font, Size: 11}, 14, t.TextMuted)
				n.Text(c, entry.ConfigurationPath).FontSize(11).FixedLineHeight(20).MinWidth(0).Shrink(1).SingleLine().Ellipsis("…").Tooltip(entry.ConfigurationPath)
				n.Spacer(c).Grow(1)
				name := workspaceToolScopeName(entry.Scope)
				if entry.Scope != domain.ToolServiceProject {
					name += " · 继承"
				}
				n.Text(c, name).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				if entry.Scope == domain.ToolServiceUser {
					if ghost(c, "管理").Label("在设备设置中管理 MCP").Height(28).FontSize(11).Padding(0).Disabled(!v.deviceState(u.key).Connected).Clicked() {
						v.openWorkspaceAgentManagement(u.key, u.harness, "MCP")
					}
				}
			})
		}
		n.Row(c.Key(entry.EntryID)).FillWidth().Height(56).Gap(12).AlignItems(n.Center).BorderWidth(.5, 0, 0, 0).BorderColor(t.Border).Children(func() {
			open := control(c.Key("open"), 5).Grow(1).MinWidth(0).Height(52).Padding(0).Justify(n.Start).Label("查看 MCP：" + entry.Name)
			open.Children(func() {
				n.Column(c).FillWidth().MinWidth(0).Children(func() {
					inlineIconText(c, toolServiceIcon, entry.Name, n.Font{Family: t.Font, Size: 13}, 16, 8, t.Text).FillWidth().Ellipsis("…")
					detail := toolConnectionLabel(entry)
					if u.session != "" && u.catalog.RuntimeKnown && entry.State == domain.ToolServiceReady {
						detail = strconv.Itoa(entry.Tools) + " 个工具"
					}
					n.Text(c, detail).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).SingleLine().Ellipsis("…").PaddingStart(24).Tooltip(detail)
				})
			})
			if open.Clicked() {
				u.detailID = entry.EntryID
				u.detailScroll = n.ScrollState{}
			}
			n.Text(c, workspaceToolState(u, entry)).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		})
	}
	if count == 0 && u.phase != toolServiceLoading {
		label := "此目录没有 MCP 配置"
		if query != "" {
			label = "没有匹配的 MCP"
		}
		n.Text(c, label).FontSize(12).TextColor(t.TextMuted).PaddingY(24)
	}
}

var toolServiceIcon = svg("M7 3v5M13 3v5M4 8h12v3a6 6 0 0 1-12 0zM10 17v2")

func (v *View) workspaceToolDetail(c *n.Context, t *n.Theme, u *workspaceToolsUI) {
	var entry *domain.InspectedToolService
	for i := range u.catalog.Entries {
		if u.catalog.Entries[i].EntryID == u.detailID {
			entry = &u.catalog.Entries[i]
			break
		}
	}
	n.Column(c).FillWidth().FillHeight().Grow(1).Children(func() {
		n.Row(c).FillWidth().Height(28).Margin(0, 0, 20, 0).Gap(8).AlignItems(n.Center).Children(func() {
			back := control(c.Key("back"), 5).Height(28).Padding(0).Label("返回 MCP 列表").Children(func() { inlineIconText(c, deviceBackIcon, "MCP", n.Font{Family: t.Font, Size: 13}, 14, 6, t.TextMuted) })
			if back.Clicked() {
				u.detailID = ""
			}
			n.Spacer(c).Grow(1)
			if entry != nil {
				n.Text(c, workspaceToolScopeName(entry.Scope)).FontSize(11).TextColor(t.TextMuted)
				if entry.Scope == domain.ToolServiceUser {
					if ghost(c, "在设备设置中管理").Label("在设备设置中管理 MCP").Height(28).Padding(0).FontSize(12).Disabled(!v.deviceState(u.key).Connected).Clicked() {
						v.openWorkspaceAgentManagement(u.key, u.harness, "MCP")
					}
				}
			}
		})
		v.workspaceToolFeedback(c, t, u)
		n.Scroll(c.Key("detail:" + u.slot)).FillWidth().FillHeight().Grow(1).TrackScroll(&u.detailScroll).Label("MCP 来源详情").Children(func() {
			if entry == nil {
				n.Text(c, "服务已不在当前来源中，请返回列表").FontSize(12).TextColor(t.TextMuted)
				return
			}
			n.Row(c).FillWidth().MinHeight(28).Gap(8).AlignItems(n.Baseline).Margin(0, 0, 24, 0).Children(func() {
				inlineIconText(c, toolServiceIcon, entry.Name, n.Font{Family: t.Font, Size: 20}, 20, 8, t.Text).Grow(1).MinWidth(0)
				n.Text(c, workspaceToolState(u, *entry)).FontSize(12).TextColor(t.TextMuted)
			})
			field := func(label, value string, copyPath bool) {
				n.Row(c.Key(label)).FillWidth().MinHeight(48).PaddingY(10).Gap(16).AlignItems(n.Baseline).BorderWidth(.5, 0, 0, 0).BorderColor(t.Border).Children(func() {
					width, _ := c.Size()
					column := float32(112)
					if width < 620 {
						column = 68
					}
					n.Text(c, label).Width(column).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
					n.Text(c, value).FontSize(12).FixedLineHeight(20).Grow(1).MinWidth(0).Selectable()
					if copyPath {
						glyph := copyReplyIcon
						if c.Now().Before(u.copiedUntil) {
							glyph = check
							c.After(u.copiedUntil.Sub(c.Now()))
						}
						if iconButton(c, glyph, "复制 MCP 配置路径").MarginEnd(-6).Clicked() {
							c.WriteClipboard(value)
							u.copiedUntil = c.Now().Add(2500 * time.Millisecond)
						}
					}
				})
			}
			field("配置文件", entry.ConfigurationPath, true)
			if entry.Configuration != nil {
				cfg := entry.Configuration
				transport := "stdio"
				endpoint := strings.Join(append([]string{cfg.Command}, cfg.Arguments...), " ")
				if cfg.Transport == "http" {
					transport = "HTTP"
					endpoint = cfg.URL
				}
				field("连接方式", transport, false)
				field("命令 / 地址", endpoint, false)
				label := "已启用"
				if !cfg.Enabled {
					label = "已停用"
				}
				field("启用状态", label, false)
			}
			if entry.IgnoredReason != "" {
				field("未加载原因", entry.IgnoredReason, false)
			}
			if u.session != "" {
				field("会话状态", workspaceToolState(u, *entry), false)
			}
			if u.session != "" && u.catalog.RuntimeKnown && u.phase == toolServiceLoaded && entry.State == domain.ToolServiceReady {
				n.Text(c, "工具 · "+strconv.Itoa(entry.Tools)).FontSize(12).TextColor(t.TextMuted).Margin(28, 0, 8, 0)
				for _, tool := range entry.Definitions {
					n.Column(c.Key(tool.Name)).FillWidth().PaddingY(10).Gap(3).BorderWidth(.5, 0, 0, 0).BorderColor(t.Border).Children(func() {
						n.Text(c, tool.Name).FontSize(12).Selectable()
						if tool.Description != "" {
							n.Text(c, tool.Description).FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
						}
					})
				}
			}
		})
	})
}
