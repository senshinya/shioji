package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"sort"
	"strings"
	"time"
)

type skillLoad uint8

const (
	skillIdle skillLoad = iota
	skillLoading
	skillLoaded
	skillFailed
)

type skillManagementUI struct {
	slot, key, device, harness, workspace, epoch, configuration string
	workspaceRevision                                           int64
	phase                                                       skillLoad
	catalog                                                     domain.SkillInventory
	query, message, pending, detailID, detailError              string
	detailLoading                                               bool
	document                                                    domain.SkillDocument
	documentRequest                                             uint64
	listScroll, detailScroll                                    n.ScrollState
	copiedUntil                                                 time.Time
	markdown                                                    markdown.Options
}
type skillManagementReturn struct {
	tab       string
	workspace *workspaceEditor
	editor    *deviceAgentEditor
	harness   string
	key       string
}

func (v *View) skillManagementFor(key, harness, workspace string) *skillManagementUI {
	if v.skillManagement == nil {
		v.skillManagement = map[string]*skillManagementUI{}
	}
	device := v.signInDevice(key)
	state := v.deviceState(key)
	slot := device + "/" + harness + "/" + workspace
	revision := int64(0)
	configuration := ""
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == workspace {
			revision = w.Revision
		}
	}
	for _, h := range state.Readiness.Harnesses {
		if h.ID == harness {
			b, _ := json.Marshal(h.Installation)
			configuration = string(b)
		}
	}
	u := v.skillManagement[slot]
	if u == nil || u.epoch != state.ConnectionEpoch || u.key != key || u.workspaceRevision != revision || u.configuration != configuration {
		next := &skillManagementUI{slot: slot, key: key, device: device, harness: harness, workspace: workspace, epoch: state.ConnectionEpoch, workspaceRevision: revision, configuration: configuration, markdown: markdown.Options{ShareSelection: true, WrapCode: true}}
		if u != nil {
			next.catalog, next.query, next.listScroll, next.detailScroll, next.detailID, next.document = u.catalog, u.query, u.listScroll, u.detailScroll, u.detailID, u.document
		}
		v.skillManagement[slot] = next
		u = next
	}
	return u
}
func (v *View) skillManagementCurrent(u *skillManagementUI) bool {
	return v.skillManagement[u.slot] == u && v.deviceState(u.key).ConnectionEpoch == u.epoch && v.signInDevice(u.key) == u.device && v.skillManagementFor(u.key, u.harness, u.workspace) == u
}
func (v *View) querySkillInventory(u *skillManagementUI) {
	if u.phase == skillLoading || u.pending != "" || v.Controller == nil || !v.deviceState(u.key).Connected {
		return
	}
	u.phase = skillLoading
	u.message = ""
	u.documentRequest++
	u.detailLoading = false
	if !v.do(app.Job{ConnectionID: u.key, Method: "runtime.skills.list", Input: protocol.SkillManagementInput{WorkspaceID: u.workspace, HarnessID: u.harness}, Apply: func(_ *app.Model, b json.RawMessage, err error) {
		if !v.skillManagementCurrent(u) {
			return
		}
		if err == nil {
			var next domain.SkillInventory
			err = json.Unmarshal(b, &next)
			if err == nil {
				u.catalog = next
			}
		}
		if err != nil {
			u.phase = skillFailed
			u.message = "无法读取 Skills，显示上次结果。请刷新重试"
			return
		}
		u.phase = skillLoaded
		if u.detailID != "" {
			found := false
			for _, entry := range u.catalog.Entries {
				if entry.ID == u.detailID {
					found = true
					break
				}
			}
			if found {
				v.readManagedSkill(u, u.detailID)
			} else {
				u.document = domain.SkillDocument{}
				u.detailError = "Skill 已不在当前目录中，请返回列表"
				u.documentRequest++
			}
		}
	}}) {
		u.phase = skillFailed
		u.message = "设备操作繁忙，请重试"
	}
}
func (v *View) readManagedSkill(u *skillManagementUI, id string) {
	u.detailID = id
	u.detailError = ""
	u.detailLoading = true
	u.documentRequest++
	request := u.documentRequest
	if v.Controller == nil || !v.deviceState(u.key).Connected {
		u.detailLoading = false
		if u.document.ID != id {
			u.detailError = "连接设备后可读取内容"
		}
		return
	}
	if !v.do(app.Job{ConnectionID: u.key, Method: "runtime.skills.read", Input: protocol.SkillManagementInput{WorkspaceID: u.workspace, HarnessID: u.harness, SkillChange: domain.SkillChange{ID: id}}, Apply: func(_ *app.Model, b json.RawMessage, err error) {
		if !v.skillManagementCurrent(u) || u.documentRequest != request || u.detailID != id {
			return
		}
		u.detailLoading = false
		var doc domain.SkillDocument
		if err == nil {
			err = json.Unmarshal(b, &doc)
			if doc.ID != id {
				err = errors.New("Skill 内容与请求不一致")
			}
		}
		if err != nil {
			u.detailError = "无法读取 Skill 内容，请重试"
			return
		}
		u.document = doc
	}}) {
		u.detailLoading = false
		u.detailError = "设备操作繁忙，请重试"
	}
}
func (v *View) changeManagedSkill(u *skillManagementUI, entry domain.ManagedSkill) {
	if !entry.CanEnable || u.workspace != "" && entry.Scope != domain.SkillProject || u.phase != skillLoaded || u.pending != "" || !v.deviceState(u.key).Connected {
		return
	}
	u.pending = entry.ID
	u.message = ""
	in := protocol.SkillManagementInput{WorkspaceID: u.workspace, HarnessID: u.harness, SkillChange: domain.SkillChange{ID: entry.ID, Revision: u.catalog.Revision, Enabled: !entry.Enabled}}
	if !v.do(app.Job{ConnectionID: u.key, Method: "runtime.skills.change", Input: in, Apply: func(_ *app.Model, b json.RawMessage, err error) {
		if !v.skillManagementCurrent(u) {
			return
		}
		u.pending = ""
		var result domain.SkillChangeResult
		if err == nil {
			err = json.Unmarshal(b, &result)
			if !result.Saved {
				err = errors.New("配置未确认")
			}
		}
		if err != nil {
			u.phase = skillFailed
			u.message = "保存未确认，原状态保留。请刷新核对"
			return
		}
		u.phase = skillIdle
		v.querySkillInventory(u)
	}}) {
		u.pending = ""
		u.phase = skillFailed
		u.message = "设备操作繁忙，请刷新重试"
	}
}
func skillScopeName(scope domain.SkillScope) string {
	switch scope {
	case domain.SkillProject:
		return "项目级"
	case domain.SkillUser:
		return "用户级"
	case domain.SkillBuiltIn:
		return "内置"
	case domain.SkillManaged:
		return "受管理"
	default:
		return "其他来源"
	}
}
func skillScopeOrder(scope domain.SkillScope) int {
	switch scope {
	case domain.SkillProject:
		return 0
	case domain.SkillUser:
		return 1
	case domain.SkillBuiltIn:
		return 2
	case domain.SkillManaged:
		return 3
	default:
		return 4
	}
}
func (v *View) agentSkills(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo) {
	if !toolServiceDraftCurrent(e, info.ID) {
		n.Text(c, "保存配置后查看 Skills").FontSize(12).TextColor(t.TextMuted)
		return
	}
	v.managedSkillsView(c, t, v.skillManagementFor(key, info.ID, ""))
}
func (v *View) managedSkillsView(c *n.Context, t *n.Theme, u *skillManagementUI) {
	connected := v.deviceState(u.key).Connected
	if u.phase == skillIdle && connected {
		v.querySkillInventory(u)
	}
	page, index := "list", 0
	if u.detailID != "" {
		page, index = "detail", 1
	}
	configurationTabPanel(c.Key("skills-navigation:"+u.slot), page, index, configurationPanelFill, func() {
		if u.detailID != "" {
			v.managedSkillDetail(c, t, u)
			return
		}
		n.Column(c).FillWidth().FillHeight().Grow(1).Children(func() {
			n.Row(c).FillWidth().Height(36).Padding(0, 0, 8).Gap(8).AlignItems(n.Center).Children(func() {
				textField(c.Key("search"), &u.query).Height(28).MaxWidth(300).Grow(1).FontSize(12).Placeholder("搜索 Skills").Label("搜索 Skills").Background(t.Background)
				n.Spacer(c).Grow(1)
				if u.workspace != "" {
					for _, info := range v.deviceState(u.key).Readiness.Harnesses {
						if info.ID == u.harness {
							inlineIconText(c, harnessIcon(domain.HarnessDescriptor{ID: info.ID, Name: info.Name, Icon: info.Icon}), info.Name, n.Font{Family: t.Font, Size: 12}, 14, 6, t.TextMuted)
							break
						}
					}
				}
				if iconButton(c, refreshIcon, "刷新 Skills").Disabled(!connected || u.phase == skillLoading || u.pending != "").Clicked() {
					v.querySkillInventory(u)
				}
			})
			v.skillManagementFeedback(c, t, u, false)
			n.Scroll(c.Key("list:" + u.slot)).FillWidth().FillHeight().Grow(1).TrackScroll(&u.listScroll).Label("Skills 列表").Children(func() { v.managedSkillRows(c, t, u) })
		})
	}, false)
}
func (v *View) skillManagementFeedback(c *n.Context, t *n.Theme, u *skillManagementUI, detail bool) {
	message := u.message
	if detail && u.detailError != "" {
		message = u.detailError
	}
	if !v.deviceState(u.key).Connected {
		message = "设备未连接，显示上次结果"
	} else if u.phase == skillLoading {
		message = "正在读取 Skills"
	} else if u.pending != "" {
		message = "正在保存"
	} else if detail && u.detailLoading {
		message = "正在读取内容"
		if u.document.ID == u.detailID {
			message = "正在更新内容，显示上次结果"
		}
	}
	frame := n.Column(c.Key("notice")).FillWidth().Clip()
	retained := n.Local(frame, "text", func() string { return message })
	if message != "" {
		*retained = message
	}
	animatedContentHeight(c, frame, message != "", 180*time.Millisecond, func() float32 {
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
			n.Text(c, *retained).FontSize(12).FixedLineHeight(20).PaddingY(4).TextColor(t.TextMuted)
			if detail && u.detailError != "" {
				if ghost(c, "重试").Disabled(!v.deviceState(u.key).Connected || u.detailLoading).Clicked() {
					v.querySkillInventory(u)
				}
			}
		})
		return 0
	})
}

func (v *View) managedSkillRows(c *n.Context, t *n.Theme, u *skillManagementUI) {
	entries := append([]domain.ManagedSkill(nil), u.catalog.Entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Scope != b.Scope {
			return skillScopeOrder(a.Scope) < skillScopeOrder(b.Scope)
		}
		if a.Directory != b.Directory {
			return a.Directory < b.Directory
		}
		return a.Name < b.Name
	})
	query := strings.ToLower(strings.TrimSpace(u.query))
	count := 0
	group := ""
	managementShown := false
	for _, entry := range entries {
		if u.workspace == "" && entry.Scope == domain.SkillProject {
			continue
		}
		if !strings.Contains(strings.ToLower(entry.Name+" "+entry.Description+" "+entry.Directory+" "+skillScopeName(entry.Scope)), query) {
			continue
		}
		count++
		inherited := u.workspace != "" && entry.Scope != domain.SkillProject
		groupID := string(entry.Scope) + "\x00" + entry.Directory
		if groupID != group {
			group = groupID
			n.Row(c.Key("group:"+groupID)).FillWidth().MinHeight(28).Margin(12, 0, 0, 0).Gap(8).AlignItems(n.Baseline).Children(func() {
				inlineSymbol(c, workspaceIcon, entry.Directory, n.Font{Family: t.Font, Size: 11}, 14, t.TextMuted)
				n.Text(c, entry.Directory).FontSize(11).FixedLineHeight(20).Shrink(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(entry.Directory)
				label := skillScopeName(entry.Scope)
				if inherited {
					label += " · 继承"
				}
				n.Text(c, label).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).PaddingX(4).Radius(4).Background(t.Surface)
				n.Spacer(c).Grow(1)
				if inherited && !managementShown {
					if ghost(c, "管理").Label("在设备设置中管理 Skills").Height(24).FontSize(11).Clicked() {
						v.openSkillDeviceManagement(u, "")
					}
					managementShown = true
				}
			})
		}
		n.Row(c.Key(entry.ID)).FillWidth().Height(56).PaddingY(6).Gap(12).AlignItems(n.Center).BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Children(func() {
			open := control(c.Key("open"), 5).Grow(1).MinWidth(0).Height(44).Padding(0, configurationHoverInset).Justify(n.Start).Label("查看 Skill：" + entry.Name)
			open.Children(func() {
				n.Column(c).FillWidth().MinWidth(0).Children(func() {
					inlineIconText(c, skillIcon, entry.Name, n.Font{Family: t.Font, Size: 13}, 16, 8, t.Text).FillWidth().Ellipsis("…")
					n.Text(c, entry.Description).FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).SingleLine().Ellipsis("…").PaddingStart(24).Tooltip(entry.Description)
				})
			})
			if open.Clicked() {
				u.detailScroll = n.ScrollState{}
				u.document = domain.SkillDocument{}
				v.readManagedSkill(u, entry.ID)
			}
			if entry.CanEnable && !inherited {
				value := entry.Enabled
				toggle := configurationSwitch(c.Key("enabled"), &value).MarginEnd(configurationHoverInset).Label("启停 Skill：" + entry.Name).Disabled(u.phase != skillLoaded || u.pending != "" || !v.deviceState(u.key).Connected)
				if toggle.Changed() {
					v.changeManagedSkill(u, entry)
				}
			} else {
				label := "已停用"
				if entry.Enabled {
					label = "已启用"
				}
				n.Text(c, label).MarginEnd(configurationHoverInset).FontSize(11).TextColor(t.TextMuted).Tooltip(entry.ReadOnlyReason)
			}
		})
	}
	if count == 0 && u.phase != skillLoading {
		label := "未发现 Skills"
		if query != "" {
			label = "没有匹配的 Skills"
		}
		n.Text(c, label).FontSize(12).TextColor(t.TextMuted).PaddingY(24)
	}
	for i, warning := range u.catalog.Warnings {
		n.Text(c.Key(i), warning).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(4).Transition(disclosureMotion(c, 180*time.Millisecond))
	}
}
func (v *View) managedSkillDetail(c *n.Context, t *n.Theme, u *skillManagementUI) {
	var entry *domain.ManagedSkill
	for i := range u.catalog.Entries {
		if u.catalog.Entries[i].ID == u.detailID {
			entry = &u.catalog.Entries[i]
			break
		}
	}
	n.Column(c).FillWidth().FillHeight().Grow(1).Children(func() {
		n.Row(c).FillWidth().Height(28).Margin(0, 0, 12, 0).Gap(8).AlignItems(n.Center).Children(func() {
			if ghost(c, "‹ Skills").Label("返回 Skills 列表").Height(28).FontSize(13).Clicked() {
				u.detailID = ""
				u.documentRequest++
				u.detailLoading = false
			}
			n.Spacer(c).Grow(1)
			if entry != nil {
				n.Text(c, skillScopeName(entry.Scope)).FontSize(12).TextColor(t.TextMuted)
			}
			if iconButton(c, refreshIcon, "刷新 Skills").Disabled(!v.deviceState(u.key).Connected || u.phase == skillLoading || u.pending != "").Clicked() {
				v.querySkillInventory(u)
			}
			copyIcon := copyReplyIcon
			if c.Now().Before(u.copiedUntil) {
				copyIcon = check
				c.After(u.copiedUntil.Sub(c.Now()))
			}
			if iconButton(c, copyIcon, "复制 Skill 说明").Disabled(u.document.ID != u.detailID).Clicked() {
				c.WriteClipboard(u.document.Text)
				u.copiedUntil = c.Now().Add(2500 * time.Millisecond)
			}
		})
		v.skillManagementFeedback(c, t, u, true)
		n.Scroll(c.Key("document:" + u.slot)).FillWidth().FillHeight().Grow(1).TrackScroll(&u.detailScroll).Label("Skill 说明").Children(func() {
			if entry != nil {
				n.Row(c).FillWidth().MinHeight(28).Gap(12).AlignItems(n.Baseline).Children(func() {
					inlineIconText(c, skillIcon, entry.Name, n.Font{Family: t.Font, Size: 16, Weight: 500}, 16, 8, t.Text).Grow(1).MinWidth(0)
					if u.workspace != "" && entry.Scope != domain.SkillProject {
						if ghost(c, "在设备设置中管理").Height(28).FontSize(12).Clicked() {
							v.openSkillDeviceManagement(u, entry.ID)
						}
					} else if entry.CanEnable {
						value := entry.Enabled
						if configurationSwitch(c, &value).Label("启停 Skill：" + entry.Name).Disabled(u.phase != skillLoaded || u.pending != "" || !v.deviceState(u.key).Connected).Changed() {
							v.changeManagedSkill(u, *entry)
						}
					}
				})
				n.Text(c, entry.Description).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Margin(8, 0, 0, 0)
				n.Text(c, entry.Path).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Margin(8, 0, 0, 0).Selectable()
			}
			if u.document.ID == u.detailID {
				markdown.View(c.Key("body:"+u.document.ID), u.document.Text, &u.markdown).FillWidth().FontSize(13).Margin(24, 0, 0, 0).Transition(disclosureMotion(c, 180*time.Millisecond))
			}
		})
	})
}
func (v *View) openSkillDeviceManagement(u *skillManagementUI, id string) {
	if !v.openWorkspaceAgentManagement(u.key, u.harness, "Skills") {
		return
	}
	target := v.skillManagementFor(u.key, u.harness, "")
	target.detailID = id
	target.phase = skillIdle
	v.querySkillInventory(target)
}

func (v *View) openWorkspaceAgentManagement(key, harness, tab string) bool {
	if v.workspaceEditor == nil {
		return false
	}
	state := v.deviceState(key)
	for _, info := range state.Readiness.Harnesses {
		if info.ID != harness {
			continue
		}
		if tab == "MCP" && !info.Capabilities.ToolServices {
			return false
		}
		e := agentEditor(state.Readiness.Harnesses)
		e.edit(info)
		e.pageID, e.tab = info.ID, tab
		e.epoch = state.ConnectionEpoch
		v.skillReturn = &skillManagementReturn{workspace: v.workspaceEditor, editor: e, harness: info.ID, key: key, tab: tab}
		v.devices = true
		v.deviceSelected = key
		return true
	}
	return false
}
func (v *View) returnSkillWorkspace() {
	if v.skillReturn == nil {
		return
	}
	ret := v.skillReturn
	v.workspaceEditor = ret.workspace
	v.devices = false
	if ret.tab == "MCP" {
		u := v.workspaceToolsFor(ret.workspace, ret.harness)
		u.request++
		u.phase = toolServiceIdle
		v.queryWorkspaceTools(u)
		return
	}
	u := v.skillManagementFor(ret.key, ret.harness, ret.workspace.Workspace.ID)
	u.phase = skillIdle
	v.querySkillInventory(u)
}
func (v *View) workspaceSkills(c *n.Context, t *n.Theme, e *workspaceEditor) {
	var agents []domain.HarnessInfo
	for _, info := range v.deviceState(e.Connection).Readiness.Harnesses {
		if info.Configured && info.Capabilities.SkillManagement {
			agents = append(agents, info)
		}
	}
	if len(agents) == 0 {
		n.Text(c, "此设备暂无支持 Skills 管理的 Agent").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if !containsHarness(agents, e.Harness) {
		e.Harness = agents[0].ID
	}
	if len(agents) > 1 {
		n.Row(c).FillWidth().Height(32).Justify(n.End).Children(func() {
			label := e.Harness
			for _, a := range agents {
				if a.ID == e.Harness {
					label = a.Name
				}
			}
			trigger := ghost(c, label).Height(28).FontSize(12)
			var choices []menuChoice
			for _, a := range agents {
				choices = append(choices, menuChoice{id: a.ID, name: a.Name, checked: a.ID == e.Harness, choose: func() { e.Harness = a.ID }})
			}
			v.choiceMenu(c, t, "workspace-skills-agent", trigger, choices)
		})
	}
	v.managedSkillsView(c, t, v.skillManagementFor(e.Connection, e.Harness, e.Workspace.ID))
}
func containsHarness(agents []domain.HarnessInfo, id string) bool {
	for _, a := range agents {
		if a.ID == id {
			return true
		}
	}
	return false
}

func (v *View) workspaceSettingsHeader(c *n.Context, t *n.Theme) {
	e := v.workspaceEditor
	header := n.Column(c.Key("workspace-header")).FillWidth().Height(104).Padding(24, configurationInset, 12)
	width, _ := c.Size()
	if width < 900 {
		header.Height(88).Padding(8, configurationInset, 12)
	}
	header.Children(func() {
		n.Column(c).FillWidth().Gap(8).Children(func() {
			back := control(c.Key("back"), 5).Height(28).AlignSelf(n.Start).PaddingX(4).MarginStart(-4).Label("返回")
			if width < 900 {
				back.MarginStart(60)
			}
			back.Children(func() {
				inlineIconText(c, deviceBackIcon, "返回会话", n.Font{Family: t.Font, Size: 12}, 12, 4, t.TextMuted)
			})
			if back.Clicked() {
				v.workspaceEditor = nil
				v.skillReturn = nil
			}
			n.Row(c).FillWidth().Height(32).Gap(16).AlignItems(n.Baseline).Children(func() {
				inlineIconText(c, workspaceIcon, e.Workspace.Name, n.Font{Family: t.Font, Size: 20}, 24, 8, t.Text).Grow(1).MinWidth(0)
				n.Text(c, v.Model.DeviceName(e.Workspace.DeviceID)).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			})
		})
	})
}

// The workspace return path shares the normal device editor lifecycle. Its
// navigation record is not an authoritative snapshot of Agent configuration.
func (v *View) skillDeviceManagementPage(c *n.Context, t *n.Theme, r *skillManagementReturn) {
	state := v.deviceState(r.key)
	tab := r.editor.tab
	v.syncDeviceAgents(r.key, state, &r.editor)
	for _, info := range r.editor.catalog {
		if info.ID != r.harness {
			continue
		}
		if r.editor.openID != info.ID {
			r.editor.edit(info)
			r.editor.pageID = info.ID
			r.editor.tab = tab
		}
		v.deviceAgentPage(c, t, r.key, v.Model.DeviceName(r.workspace.Workspace.DeviceID), state, r.editor, info)
		return
	}
	n.Column(c).FillWidth().Padding(configurationInset).Gap(12).Children(func() {
		label := "返回工作区 Skills"
		if r.tab == "MCP" {
			label = "返回工作区 MCP"
		}
		if ghost(c, label).Clicked() {
			v.returnSkillWorkspace()
		}
		n.Text(c, "此 Agent 已不可用").FontSize(12).TextColor(t.TextMuted)
	})
}
