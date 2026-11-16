package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"sort"
	"strings"
	"time"
)

type hookManagementUI struct {
	slot, key, device, harness, workspace, session, epoch, identity string
	phase                                                           skillLoad
	catalog                                                         domain.HookCatalog
	query, message, detailID                                        string
	pending                                                         *domain.HookRequest
	unconfirmed                                                     []domain.HookRequest
	checking                                                        bool
	expanded                                                        bool
	listScroll, detailScroll                                        n.ScrollState
}

func (v *View) hookManagementFor(key, harness, workspace, session string) *hookManagementUI {
	if v.hookManagement == nil {
		v.hookManagement = map[string]*hookManagementUI{}
	}
	state := v.deviceState(key)
	device := v.signInDevice(key)
	slot := device + "/" + harness + "/" + workspace + "/" + session
	// Configuration, authoritative workspace revision and accepted session context
	// all invalidate writes and late responses, while preserving reading state.
	identity := struct {
		Installation *domain.RuntimeInstallation
		Revision     int64
		Session      domain.Session
	}{}
	for _, h := range state.Readiness.Harnesses {
		if h.ID == harness {
			identity.Installation = h.Installation
		}
	}
	for _, w := range v.Model.Snapshot.Workspaces {
		if w.ID == workspace {
			identity.Revision = w.Revision
		}
	}
	for _, s := range v.Model.Snapshot.Sessions {
		if s.ID == session {
			identity.Session = domain.Session{ID: s.ID, ThreadID: s.ThreadID, WorkspaceID: s.WorkspaceID, HarnessID: s.HarnessID, Context: s.Context, Archived: s.Archived}
		}
	}
	b, _ := json.Marshal(identity)
	u := v.hookManagement[slot]
	if u == nil || u.key != key || u.epoch != state.ConnectionEpoch || u.identity != string(b) {
		next := &hookManagementUI{slot: slot, key: key, device: device, harness: harness, workspace: workspace, session: session, epoch: state.ConnectionEpoch, identity: string(b)}
		if u != nil {
			next.catalog = u.catalog
			next.catalog.RuntimeKnown = false
			next.query = u.query
			next.detailID = u.detailID
			next.listScroll = u.listScroll
			next.detailScroll = u.detailScroll
			next.expanded = u.expanded
			next.pending = u.pending
			next.unconfirmed = append([]domain.HookRequest(nil), u.unconfirmed...)
		}
		v.hookManagement[slot] = next
		u = next
	}
	if !state.Connected {
		u.catalog.RuntimeKnown = false
	}
	return u
}
func (v *View) hookManagementCurrent(u *hookManagementUI) bool {
	return v.hookManagement[u.slot] == u && v.hookManagementFor(u.key, u.harness, u.workspace, u.session) == u
}
func (v *View) queryHooks(u *hookManagementUI) {
	if u.phase == skillLoading || u.checking || v.Controller == nil || !v.deviceState(u.key).Connected {
		return
	}
	if v.deviceState(u.key).WorkerMinor < 46 {
		u.phase = skillFailed
		u.message = "更新设备执行服务后可管理 Hooks"
		return
	}
	if u.pending != nil {
		v.queryHookOperation(u)
		return
	}
	v.readHookCatalog(u, false)
}
func (v *View) readHookCatalog(u *hookManagementUI, reconcile bool) {
	if u.phase == skillLoading || u.checking || v.Controller == nil || !v.deviceState(u.key).Connected {
		return
	}
	u.phase = skillLoading
	u.message = ""
	in := domain.HookRequest{HarnessID: u.harness, WorkspaceID: u.workspace, SessionID: u.session}
	if !v.do(app.Job{ConnectionID: u.key, Method: "runtime.hooks.list", Input: in, Apply: func(_ *app.Model, b json.RawMessage, err error) {
		if !v.hookManagementCurrent(u) {
			return
		}
		var next domain.HookCatalog
		if err == nil {
			err = json.Unmarshal(b, &next)
		}
		if err != nil {
			u.phase = skillFailed
			u.message = "无法读取 Hooks，显示上次结果。请刷新重试"
			return
		}
		u.catalog = next
		if reconcile && u.pending != nil {
			// Retain the original receipt identity, never replay its approval. A new
			// operation requires an explicit action against this freshly read catalog.
			u.unconfirmed = append(u.unconfirmed, *u.pending)
			u.pending = nil
			u.message = "上次结果未确认，请核对当前定义"
		}
		u.phase = skillLoaded
	}}) {
		u.phase = skillFailed
		u.message = "设备操作繁忙，请重试"
	}
}
func (v *View) queryHookOperation(u *hookManagementUI) {
	if u.pending == nil || u.checking || v.Controller == nil || !v.deviceState(u.key).Connected {
		return
	}
	in := *u.pending
	u.checking = true
	if !v.do(app.Job{ConnectionID: u.key, Method: "runtime.hooks.operation", Input: in, Apply: func(_ *app.Model, b json.RawMessage, err error) {
		if !v.hookManagementCurrent(u) {
			return
		}
		var remote *protocol.RemoteError
		if errors.As(err, &remote) && remote.Code == "notFound" {
			u.checking = false
			u.pending = nil
			u.phase = skillIdle
			v.queryHooks(u)
			u.message = "操作未保存，请重新审核"
			return
		}
		v.settleHookChange(u, in, b, err)
		if u.pending != nil && !u.checking {
			v.readHookCatalog(u, true)
		}
	}}) {
		u.checking = false
		u.phase = skillFailed
		u.message = "结果未确认，请刷新核对"
	}
}
func (v *View) settleHookChange(u *hookManagementUI, in domain.HookRequest, b json.RawMessage, err error) {
	u.checking = false
	var result domain.HookOperation
	if err == nil {
		err = json.Unmarshal(b, &result)
	}
	if err != nil || result.Input != in || result.State != domain.OperationConfirmed && result.State != domain.OperationRejected {
		u.phase = skillFailed
		u.message = "结果未确认，请刷新核对"
		return
	}
	u.pending = nil
	if result.State == domain.OperationRejected || !result.Saved {
		u.phase = skillFailed
		u.message = result.Message
		if u.message == "" {
			u.message = "保存失败，请刷新后重试"
		}
		return
	}
	u.phase = skillIdle
	v.queryHooks(u)
	if result.AdoptionPending {
		u.message = result.Message
	}
}
func (v *View) changeHook(u *hookManagementUI, h domain.HookEntry, action domain.HookAction) {
	owned := u.workspace == "" && h.Scope != domain.HookProject || u.workspace != "" && h.Scope == domain.HookProject
	if !owned || u.phase != skillLoaded || u.pending != nil || !v.deviceState(u.key).Connected || action == domain.HookSetEnabled && !h.CanEnable || action == domain.HookTrustDefinition && !h.CanTrust {
		return
	}
	in := domain.HookRequest{HarnessID: u.harness, WorkspaceID: u.workspace, SessionID: u.session, HookChange: domain.HookChange{OperationID: domain.NewID(), ID: h.ID, Revision: u.catalog.Revision, DefinitionRevision: h.DefinitionRevision, Action: action, Enabled: !h.Enabled}}
	// Trust never carries an implicit enable request.
	if action == domain.HookTrustDefinition {
		in.Enabled = false
	}
	u.pending = &in
	u.checking = true
	u.message = ""
	if !v.do(app.Job{ConnectionID: u.key, Method: "runtime.hooks.change", Input: in, Apply: func(_ *app.Model, b json.RawMessage, err error) {
		if v.hookManagementCurrent(u) {
			v.settleHookChange(u, in, b, err)
		}
	}}) {
		// The job did not enter the controller, so nothing was sent.
		u.pending = nil
		u.checking = false
		u.phase = skillFailed
		u.message = "设备操作繁忙，请刷新重试"
	}
}
func hookTrustLabel(trust domain.HookTrust) string {
	switch trust {
	case domain.HookTrusted:
		return "已信任"
	case domain.HookModified:
		return "已修改"
	case domain.HookPolicy:
		return "策略管理"
	default:
		return "待审核"
	}
}
func hookDefinition(h domain.HookEntry) string {
	if h.Kind == "command" {
		return h.Command
	}
	if h.Kind == "tool" && h.Service != "" && h.Tool != "" {
		return h.Service + " / " + h.Tool
	}
	return "无法读取完整定义"
}
func hookOwned(u *hookManagementUI, h domain.HookEntry) bool {
	return u.workspace == "" && h.Scope != domain.HookProject || u.workspace != "" && h.Scope == domain.HookProject
}
func (v *View) hookWritable(u *hookManagementUI) bool {
	return u.phase == skillLoaded && u.pending == nil && v.deviceState(u.key).Connected
}
func (v *View) agentHooks(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo) {
	if !toolServiceDraftCurrent(e, info.ID) {
		n.Text(c, "保存配置后查看 Hooks").FontSize(12).TextColor(t.TextMuted)
		return
	}
	v.managedHooksView(c, t, v.hookManagementFor(key, info.ID, "", ""))
}
func (v *View) workspaceHooks(c *n.Context, t *n.Theme, e *workspaceEditor) {
	var agents []domain.HarnessInfo
	for _, info := range v.deviceState(e.Connection).Readiness.Harnesses {
		if info.Configured && info.Capabilities.HookManagement {
			agents = append(agents, info)
		}
	}
	if len(agents) == 0 {
		n.Text(c, "此设备暂无支持 Hooks 管理的 Agent").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if !containsHarness(agents, e.Harness) {
		e.Harness = agents[0].ID
	}
	if len(agents) > 1 {
		n.Row(c).FillWidth().Height(32).Justify(n.End).Children(func() {
			label := e.Harness
			var choices []menuChoice
			for _, a := range agents {
				if a.ID == e.Harness {
					label = a.Name
				}
				choices = append(choices, menuChoice{id: a.ID, name: a.Name, checked: a.ID == e.Harness, choose: func() { e.Harness = a.ID }})
			}
			v.choiceMenu(c, t, "workspace-hooks-agent", ghost(c, label).Height(28).FontSize(12), choices)
		})
	}
	v.managedHooksView(c, t, v.hookManagementFor(e.Connection, e.Harness, e.Workspace.ID, ""))
}
func (v *View) hookFeedback(c *n.Context, t *n.Theme, u *hookManagementUI) {
	message := u.message
	if message == "" && u.session == "" && u.workspace != "" && v.workspaceEditor == nil && v.Model.GitDrafts[u.workspace].NewWorktree {
		message = "主目录预览 · 提交后核对 worktree"
	}
	if !v.deviceState(u.key).Connected {
		message = "设备未连接，显示上次结果"
	} else if u.checking {
		message = "正在核对保存结果"
	} else if u.phase == skillLoading {
		message = "正在读取 Hooks"
	}
	if u.session != "" && u.phase == skillLoaded && !u.catalog.RuntimeKnown && message == "" {
		message = "配置来源 · 当前会话尚未加载"
	}
	frame := n.Column(c.Key("hook-feedback")).FillWidth().Clip()
	retained := n.Local(frame, "text", func() string { return message })
	if message != "" {
		*retained = message
	}
	animatedContentHeight(c, frame, message != "", 180*time.Millisecond, func() float32 {
		n.Text(c, *retained).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(4)
		return 0
	})
}
func (v *View) managedHooksView(c *n.Context, t *n.Theme, u *hookManagementUI) {
	if u.phase == skillIdle && v.deviceState(u.key).Connected {
		v.queryHooks(u)
	}
	page, index := "list", 0
	if u.detailID != "" {
		page, index = "detail", 1
	}
	configurationTabPanel(c.Key("hooks-navigation:"+u.slot), page, index, configurationPanelFill, func() {
		if u.detailID != "" {
			v.hookDetail(c, t, u)
			return
		}
		n.Column(c).FillWidth().FillHeight().Grow(1).Children(func() {
			n.Row(c).FillWidth().Height(36).Padding(0, 8, 8, 0).Gap(8).AlignItems(n.Center).Children(func() {
				textField(c.Key("search"), &u.query).Height(28).MaxWidth(300).Grow(1).FontSize(12).Placeholder("搜索 Hooks").Label("搜索 Hooks").Background(t.Background)
				n.Spacer(c).Grow(1)
				if u.workspace != "" && u.session == "" && v.workspaceEditor != nil {
					for _, info := range v.deviceState(u.key).Readiness.Harnesses {
						if info.ID == u.harness {
							inlineIconText(c, harnessIcon(domain.HarnessDescriptor{ID: info.ID, Name: info.Name, Icon: info.Icon}), info.Name, n.Font{Family: t.Font, Size: 12}, 14, 6, t.TextMuted)
							break
						}
					}
				}
				if iconButton(c, refreshIcon, "刷新 Hooks").Disabled(!v.deviceState(u.key).Connected || u.phase == skillLoading || u.checking).Clicked() {
					v.queryHooks(u)
				}
			})
			v.hookFeedback(c, t, u)
			n.Scroll(c.Key("hook-list:" + u.slot)).FillWidth().FillHeight().Grow(1).TrackScroll(&u.listScroll).PaddingEnd(hookScrollGutter).Label("Hooks 列表").Children(func() { v.hookRows(c, t, u) })
		})
	}, false)
}

// Stable slots share the first line. The independent 28 DIP switches and arrow
// buttons occupy that line, never the geometric centre of the two-line row.
func (v *View) hookRows(c *n.Context, t *n.Theme, u *hookManagementUI) {
	entries := append([]domain.HookEntry(nil), u.catalog.Entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		groupOrder := func(scope domain.HookScope) int {
			if scope == domain.HookProject {
				return 0
			}
			if scope == domain.HookUnknown {
				return 2
			}
			return 1
		}
		if groupOrder(a.Scope) != groupOrder(b.Scope) {
			return groupOrder(a.Scope) < groupOrder(b.Scope)
		}
		if a.Event != b.Event {
			return a.Event < b.Event
		}
		return a.ID < b.ID
	})
	query := strings.ToLower(strings.TrimSpace(u.query))
	count := 0
	group := ""
	for _, h := range entries {
		if u.workspace == "" && h.Scope == domain.HookProject {
			continue
		}
		if !strings.Contains(strings.ToLower(h.Event+" "+h.Source+" "+h.SourcePath+" "+hookDefinition(h)+" "+hookTrustLabel(h.Trust)), query) {
			continue
		}
		count++
		inherited := u.workspace != "" && h.Scope != domain.HookProject && h.Scope != domain.HookUnknown
		if u.workspace != "" {
			name := "工作区"
			if h.Scope == domain.HookUnknown {
				name = "其他来源"
			}
			if inherited {
				name = "全局 · 继承"
			}
			if group != name {
				group = name
				n.Row(c.Key("group:" + name)).FillWidth().Height(36).AlignItems(n.Center).Children(func() {
					n.Text(c, name).FontSize(11).TextColor(t.TextMuted)
					n.Spacer(c).Grow(1)
					if inherited {
						if ghost(c, "管理").Height(28).FontSize(12).Label("在设备设置中管理 Hooks").Clicked() {
							v.openHookDeviceManagement(u, "")
						}
					}
				})
			}
		}
		n.Row(c.Key(h.ID)).FillWidth().Height(hookRowHeight).Padding(8, 0, 8, 0).Gap(8).AlignItems(n.Start).BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Children(func() {
			open := control(c.Key("open"), 5).Grow(1).MinWidth(0).Height(48).Padding(0, 0).Justify(n.Start).Label("查看 Hook：" + h.Event)
			open.Children(func() {
				n.Column(c).FillWidth().MinWidth(0).Children(func() {
					n.Row(c).FillWidth().Height(28).Gap(12).AlignItems(n.Baseline).Children(func() {
						n.Text(c, h.Event).FontSize(13).FixedLineHeight(20).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").TooltipWhenTruncated(h.Event)
						hookColumnText(c, t, h.Event, h.Source, 60)
						hookColumnText(c, t, h.Event, hookTrustLabel(h.Trust), 60)
					})
					n.Text(c, hookDefinition(h)).Font("monospace").FontSize(12).FixedLineHeight(20).FillWidth().SingleLine().Ellipsis("…").TextColor(t.TextMuted).TooltipWhenTruncated(hookDefinition(h))
				})
			})
			if open.Clicked() {
				u.detailID = h.ID
				u.expanded = false
				u.detailScroll = n.ScrollState{}
			}
			n.Column(c).Width(48).Height(28).Justify(n.Center).AlignItems(n.End).Transform(n.Translation(0, hookFirstLineControlOffset(h.Event, t))).Children(func() {
				if hookOwned(u, h) && h.CanEnable {
					enabled := h.Enabled
					if configurationSwitch(c.Key("enabled"), &enabled).Label("启停 Hook：" + h.Event).Disabled(!v.hookWritable(u)).Changed() {
						v.changeHook(u, h, domain.HookSetEnabled)
					}
				} else {
					label := "已停用"
					if h.Enabled {
						label = "已启用"
					}
					n.Text(c, label).FontSize(11).TextColor(t.TextMuted).Tooltip(h.ReadOnlyReason)
				}
			})
			if iconButton(c, rightChevron, "审核 Hook："+h.Event).Transform(n.Translation(0, hookFirstLineControlOffset(h.Event, t))).Clicked() {
				u.detailID = h.ID
				u.expanded = false
				u.detailScroll = n.ScrollState{}
			}
		})
	}
	if count == 0 && u.phase != skillLoading {
		label := "没有 Hook"
		if query != "" {
			label = "没有匹配的 Hook"
		}
		n.Text(c, label).FontSize(12).TextColor(t.TextMuted).PaddingY(24)
	}
	for i, warning := range u.catalog.Warnings {
		n.Text(c.Key(i), warning).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(4)
	}
}
func hookColumnText(c *n.Context, t *n.Theme, primary, label string, width float32) {
	font := n.Font{Family: t.Font, Size: 13, Weight: 400}
	offset := inlineFontMetrics(label, font).center - inlineFontMetrics(primary, font).center
	n.Text(c, label).FontSize(13).FixedLineHeight(20).Width(width).SingleLine().Ellipsis("…").TextColor(t.TextMuted).Transform(n.Translation(0, offset))
}
func (v *View) hookDetail(c *n.Context, t *n.Theme, u *hookManagementUI) {
	var h *domain.HookEntry
	for i := range u.catalog.Entries {
		if u.catalog.Entries[i].ID == u.detailID {
			h = &u.catalog.Entries[i]
			break
		}
	}
	n.Column(c).FillWidth().FillHeight().Grow(1).Children(func() {
		n.Row(c).FillWidth().Height(28).PaddingEnd(8).Margin(0, 0, 16, 0).Gap(8).AlignItems(n.Center).Children(func() {
			if ghost(c, "‹ Hooks").Height(28).FontSize(13).Label("返回 Hooks 列表").Clicked() {
				u.detailID = ""
			}
			n.Spacer(c).Grow(1)
			if iconButton(c, refreshIcon, "刷新 Hooks").Disabled(!v.deviceState(u.key).Connected || u.phase == skillLoading || u.checking).Clicked() {
				v.queryHooks(u)
			}
		})
		v.hookFeedback(c, t, u)
		n.Scroll(c.Key("hook-detail:" + u.slot)).FillWidth().FillHeight().Grow(1).TrackScroll(&u.detailScroll).PaddingEnd(hookScrollGutter).Label("Hook 定义").Children(func() {
			if h == nil {
				n.Text(c, "定义已变化，请返回列表重新审核").FontSize(12).TextColor(t.TextMuted)
				return
			}
			n.Column(c).FillWidth().PaddingEnd(0).Gap(16).Children(func() {
				n.Row(c).FillWidth().MinHeight(28).Gap(12).AlignItems(n.Baseline).Children(func() {
					n.Text(c, h.Event).FontSize(18).FixedLineHeight(28).Grow(1).MinWidth(0)
					n.Text(c, hookTrustLabel(h.Trust)).FontSize(12).TextColor(t.TextMuted).Transform(n.Translation(0, inlineFontMetrics(hookTrustLabel(h.Trust), n.Font{Family: t.Font, Size: 12, Weight: 400}).center-inlineFontMetrics(h.Event, n.Font{Family: t.Font, Size: 18, Weight: 400}).center))
				})
				n.Text(c, hookDefinition(*h)).Font("monospace").FontSize(13).FixedLineHeight(21).FillWidth().Padding(12).Radius(5).Background(t.Surface).Selectable()
				n.Text(c, h.SourcePath).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
				if h.Matcher != "" {
					hookMetadata(c, t, "匹配", h.Matcher)
				}
				if h.StatusMessage != "" {
					n.Text(c, h.StatusMessage).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
				}
				disclosure := "› 详细信息"
				if u.expanded {
					disclosure = "⌄ 详细信息"
				}
				if ghost(c, disclosure).Height(28).AlignSelf(n.Start).FontSize(12).Label("详细信息").Clicked() {
					u.expanded = !u.expanded
				}
				frame := n.Column(c.Key("metadata")).FillWidth().Clip()
				animatedContentHeight(c, frame, u.expanded, 180*time.Millisecond, func() float32 {
					n.Column(c).FillWidth().Gap(8).Children(func() {
						hookMetadata(c, t, "设备", v.Model.DeviceName(u.device))
						if u.catalog.Directory != "" {
							hookMetadata(c, t, "执行目录", u.catalog.Directory)
						}
						kind := "未知"
						if h.Kind == "command" {
							kind = "命令"
						} else if h.Kind == "tool" {
							kind = "工具"
						}
						hookMetadata(c, t, "类型", kind)
						hookMetadata(c, t, "超时", fmt.Sprintf("%d 秒", h.TimeoutSeconds))
						mode := "同步"
						if h.Async {
							mode = "异步"
						}
						hookMetadata(c, t, "执行方式", mode)
						if h.Plugin != "" {
							hookMetadata(c, t, "插件", h.Plugin)
						}
						hookMetadata(c, t, "指纹", h.Fingerprint)
					})
					return 0
				})
			})
		})
		if h != nil {
			n.Row(c).FillWidth().MinHeight(48).Padding(12, 8, 0, 0).Gap(8).AlignItems(n.Center).BorderWidth(.5, 0, 0, 0).BorderColor(t.Border).Children(func() {
				if !hookOwned(u, *h) {
					if h.Scope == domain.HookUnknown {
						n.Text(c, h.ReadOnlyReason).FontSize(12).TextColor(t.TextMuted)
						return
					}
					n.Spacer(c).Grow(1)
					if ghost(c, "在设备设置中管理").Height(28).FontSize(12).Clicked() {
						v.openHookDeviceManagement(u, h.ID)
					}
					return
				}
				if h.CanEnable {
					enabled := h.Enabled
					if configurationSwitch(c, &enabled).Label("启停 Hook：" + h.Event).Disabled(!v.hookWritable(u)).Changed() {
						v.changeHook(u, *h, domain.HookSetEnabled)
					}
					n.Text(c, "启用").FontSize(12)
				} else {
					n.Text(c, h.ReadOnlyReason).FontSize(12).TextColor(t.TextMuted)
				}
				n.Spacer(c).Grow(1)
				if h.CanTrust {
					if configurationAction(c, "信任此版本").Disabled(!v.hookWritable(u)).Clicked() {
						v.changeHook(u, *h, domain.HookTrustDefinition)
					}
				}
			})
		}
	})
}
func hookMetadata(c *n.Context, t *n.Theme, label, value string) {
	n.Row(c.Key(label)).FillWidth().Gap(12).AlignItems(n.Baseline).Children(func() {
		n.Text(c, label).Width(64).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		n.Text(c, value).FontSize(12).FixedLineHeight(20).Grow(1).MinWidth(0).Selectable()
	})
}
func (v *View) openHookDeviceManagement(u *hookManagementUI, id string) {
	composer := v.workspaceEditor == nil
	if composer {
		for _, w := range v.Model.Snapshot.Workspaces {
			if w.ID == u.workspace {
				v.workspaceEditor = &workspaceEditor{Workspace: w, Connection: u.key, Harness: u.harness}
				break
			}
		}
	}
	if !v.openWorkspaceAgentManagement(u.key, u.harness, "Hooks") {
		if composer {
			v.workspaceEditor = nil
		}
		return
	}
	if composer {
		v.skillReturn.composer = true
		v.skillReturn.session = u.session
		v.workspaceEditor = nil
		v.runMenu = runMenuClosed
	}
	target := v.hookManagementFor(u.key, u.harness, "", "")
	target.detailID = id
	target.phase = skillIdle
	v.queryHooks(target)
}

func (v *View) composerHooks() *hookManagementUI {
	m := v.Model
	workspace := m.WorkspaceID
	if m.SessionID != "" {
		for _, s := range m.Snapshot.Sessions {
			if s.ID == m.SessionID {
				workspace = s.WorkspaceID
				break
			}
		}
	}
	return v.hookManagementFor(m.ConnectionID(), m.HarnessID(), workspace, m.SessionID)
}
func hookPendingCount(u *hookManagementUI) int {
	count := 0
	for _, h := range u.catalog.Entries {
		if h.Enabled && (h.Trust == domain.HookUntrusted || h.Trust == domain.HookModified) {
			count++
		}
	}
	return count
}

// A 20 DIP text line keeps its native baseline. Its optical centre comes from
// the shared H/国 ink metrics; controls keep their full 28 DIP hit area.
func hookFirstLineControlOffset(label string, t *n.Theme) float32 {
	return (20-configurationControlHeight)/2 - inlineCenterOffset(label, n.Font{Family: t.Font, Size: 13, Weight: 400})
}
