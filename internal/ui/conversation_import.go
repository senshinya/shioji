package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type conversationImportPhase uint8

const (
	conversationLoading conversationImportPhase = iota
	conversationReady
	conversationFailed
	conversationPreviewLoading
	conversationPreviewReady
	conversationImporting
	conversationPartial
	conversationFinished
	conversationClosing
)

type conversationImportUI struct {
	key, device, epoch, harness    string
	workspace                      domain.Workspace
	welcome                        bool
	phase                          conversationImportPhase
	query, filter, cursor, message string
	items                          []domain.ConversationCandidate
	arrived                        map[string]time.Time
	selected                       map[string]bool
	failures                       map[string]string
	list                           n.ListState
	previewSource                  string
	preview                        *domain.ConversationPreview
	previewScroll                  n.ScrollState
	markdown                       markdown.Options
	listRequest, previewRequest    uint64
	pages                          int
	seenCursors                    map[string]bool
	queue                          []domain.ConversationCandidate
	total, done, imported          int
	closed                         time.Time
}

var conversationImportIcon = svg("M10 2v10m-4-4 4 4 4-4M3 13v5h14v-5")
var conversationBackIcon = svg("m12 5-5 5 5 5")

func (v *View) importHarnesses(key string) []domain.HarnessInfo {
	state := v.deviceState(key)
	if state == nil || state.WorkerMinor < 32 {
		return nil
	}
	var out []domain.HarnessInfo
	for _, h := range state.Readiness.Harnesses {
		if h.Capabilities.ConversationImport {
			out = append(out, h)
		}
	}
	return out
}
func (v *View) canImportConversations(key string) bool {
	return v.Controller != nil && v.deviceState(key).Connected && len(v.importHarnesses(key)) > 0
}
func (v *View) openConversationImport(key string, w domain.Workspace, welcome bool) {
	if v.conversationImport != nil && v.conversationImport.phase == conversationImporting {
		return
	}
	state := v.deviceState(key)
	u := &conversationImportUI{key: key, device: v.Model.ConnectionDeviceID(key), epoch: state.ConnectionEpoch, workspace: w, welcome: welcome, phase: conversationReady, filter: "all", selected: map[string]bool{}, failures: map[string]string{}, seenCursors: map[string]bool{}}
	u.arrived = map[string]time.Time{}
	for _, h := range v.importHarnesses(key) {
		if u.harness == "" || h.ID == v.Model.HarnessID() {
			u.harness = h.ID
		}
	}
	v.conversationImport = u
	if u.harness == "" {
		u.phase = conversationFailed
		u.message = "当前 Agent 不支持导入历史"
		return
	}
	v.readConversations(u, false)
}
func (v *View) conversationImportCurrent(u *conversationImportUI) bool {
	if v.conversationImport != u || u.phase == conversationClosing || !v.deviceState(u.key).Connected || v.Model.ConnectionDeviceID(u.key) != u.device || v.deviceState(u.key).ConnectionEpoch != u.epoch {
		return false
	}
	for _, w := range v.deviceState(u.key).Snapshot.Workspaces {
		if w.ID == u.workspace.ID {
			return !w.Archived && w.Revision == u.workspace.Revision
		}
	}
	return false
}
func (u *conversationImportUI) queryFor(c domain.ConversationCandidate) protocol.ConversationQuery {
	return protocol.ConversationQuery{WorkspaceID: u.workspace.ID, Revision: u.workspace.Revision, HarnessID: u.harness, SourceRef: c.SourceRef, Directory: c.Directory}
}
func (v *View) readConversations(u *conversationImportUI, more bool) {
	if !v.conversationImportCurrent(u) || u.phase == conversationImporting || u.harness == "" {
		return
	}
	if !more {
		u.items = nil
		u.cursor = ""
		u.pages = 0
		u.seenCursors = map[string]bool{}
		u.list = n.ListState{}
	}
	u.phase = conversationLoading
	u.message = ""
	u.listRequest++
	request := u.listRequest
	q := u.queryFor(domain.ConversationCandidate{})
	q.Cursor = u.cursor
	if !v.do(app.Job{ConnectionID: u.key, Method: "conversation.list", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if !v.conversationImportCurrent(u) || request != u.listRequest {
			return
		}
		if err != nil {
			u.phase = conversationFailed
			u.message = err.Error()
			return
		}
		var page domain.ConversationCatalog
		if json.Unmarshal(b, &page) != nil || len(page.Items) > 50 || len(page.Cursor) > 4096 {
			u.phase = conversationFailed
			u.message = "历史目录无效"
			return
		}
		for _, item := range page.Items {
			if item.HarnessID != u.harness || !slices.Contains(u.workspace.Folders, item.Directory) || item.SourceRef == "" {
				u.phase = conversationFailed
				u.message = "历史来源不属于此工作区"
				return
			}
			index := slices.IndexFunc(u.items, func(c domain.ConversationCandidate) bool { return c.SourceRef == item.SourceRef })
			if index < 0 {
				u.items = append(u.items, item)
				if _, seen := u.arrived[item.SourceRef]; !seen {
					u.arrived[item.SourceRef] = v.frameTime
				}
			} else {
				u.items[index] = item
			}
			if item.ImportedSessionID != "" {
				delete(u.selected, item.SourceRef)
			}
		}
		u.pages++
		u.phase = conversationReady
		u.cursor = page.Cursor
		if page.Cursor != "" {
			if u.seenCursors[page.Cursor] {
				u.phase = conversationFailed
				u.message = "历史分页未前进"
				u.cursor = ""
				return
			}
			u.seenCursors[page.Cursor] = true
			// A source index may need several pages before matching this workspace.
			// Bound each scan; the explicit More control can continue it later.
			if len(u.items) == 0 && u.pages < 20 {
				v.readConversations(u, true)
			}
		}
		if len(u.items) >= 1000 {
			u.items = u.items[:1000]
			u.cursor = ""
			u.message = "仅展示最近的 1000 个会话"
		}
	}}) {
		u.phase = conversationFailed
		u.message = "设备操作繁忙，请重试"
	}
}
func (v *View) previewConversation(u *conversationImportUI, candidate domain.ConversationCandidate) {
	if !v.conversationImportCurrent(u) || u.phase == conversationImporting {
		return
	}
	u.previewSource = candidate.SourceRef
	u.preview = nil
	u.previewScroll = n.ScrollState{}
	u.previewRequest++
	request := u.previewRequest
	u.phase = conversationPreviewLoading
	u.message = ""
	if !v.do(app.Job{ConnectionID: u.key, Method: "conversation.preview", Input: u.queryFor(candidate), Apply: func(_ *app.Model, b json.RawMessage, err error) {
		if !v.conversationImportCurrent(u) || request != u.previewRequest || u.previewSource != candidate.SourceRef {
			return
		}
		u.phase = conversationPreviewReady
		if err != nil {
			u.message = err.Error()
			return
		}
		var p domain.ConversationPreview
		if json.Unmarshal(b, &p) != nil || p.Candidate.SourceRef != candidate.SourceRef || p.Candidate.HarnessID != u.harness || p.Candidate.Directory != candidate.Directory || len(p.Items) > 200 {
			u.message = "会话预览无效"
			return
		}
		u.preview = &p
	}}) {
		u.phase = conversationPreviewReady
		u.message = "设备操作繁忙，请重试"
	}
}
func (v *View) importConfiguration(u *conversationImportUI) domain.RunConfig {
	ready, _ := v.deviceState(u.key).Readiness.ForHarness(u.harness)
	var cfg domain.RunConfig
	for _, m := range ready.Models {
		if cfg.Model == "" || m.Default {
			cfg.Model = m.ID
			cfg.Effort = m.DefaultEffort
		}
	}
	if slices.Contains(ready.Permissions, "default") {
		cfg.Permission = "default"
	} else if len(ready.Permissions) > 0 {
		cfg.Permission = ready.Permissions[0]
	}
	return cfg
}
func (v *View) beginConversationImport(u *conversationImportUI) {
	if !v.conversationImportCurrent(u) || u.phase == conversationImporting {
		return
	}
	u.queue = nil
	for _, c := range u.items {
		if u.selected[c.SourceRef] && c.ImportedSessionID == "" {
			u.queue = append(u.queue, c)
		}
	}
	if len(u.queue) == 0 {
		return
	}
	u.total = len(u.queue)
	u.done = 0
	// A pending catalog page is unrelated to this immutable selection. Its
	// eventual reply must not unlock or replace the running batch's state.
	u.listRequest++
	u.failures = map[string]string{}
	u.phase = conversationImporting
	u.message = ""
	v.importNextConversation(u)
}
func (v *View) importNextConversation(u *conversationImportUI) {
	if !v.conversationImportCurrent(u) {
		return
	}
	if len(u.queue) == 0 {
		if len(u.failures) > 0 {
			u.phase = conversationPartial
			u.message = fmt.Sprintf("已导入 %d 个，%d 个未能完成", u.imported, len(u.failures))
		} else {
			u.phase = conversationFinished
			u.message = fmt.Sprintf("已导入 %d 个会话", u.imported)
			if u.welcome {
				v.closeConversationImport(u, true)
			}
		}
		return
	}
	candidate := u.queue[0]
	u.queue = u.queue[1:]
	input := protocol.ConversationImportInput{ConversationQuery: u.queryFor(candidate), Config: v.importConfiguration(u)}
	if !v.do(app.Job{ConnectionID: u.key, Method: "conversation.import", Input: input, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if !v.conversationImportCurrent(u) {
			return
		}
		var s domain.Session
		if err == nil && (json.Unmarshal(b, &s) != nil || s.ID == "" || s.WorkspaceID != u.workspace.ID || s.HarnessID != u.harness) {
			err = fmt.Errorf("导入结果无效")
		}
		u.done++
		if err != nil {
			u.failures[candidate.SourceRef] = err.Error()
		} else {
			u.imported++
			delete(u.selected, candidate.SourceRef)
			for i := range u.items {
				if u.items[i].SourceRef == candidate.SourceRef {
					u.items[i].ImportedSessionID = s.ID
				}
			}
			// The reply is authoritative, but never advances a journal cursor. The
			// ordinary event stream and detail loader still own hydration.
			state := v.deviceState(u.key)
			index := slices.IndexFunc(state.Snapshot.Sessions, func(old domain.Session) bool { return old.ID == s.ID })
			if index < 0 {
				state.Snapshot.Sessions = append(state.Snapshot.Sessions, s)
			} else if state.Snapshot.Sessions[index].Revision <= s.Revision {
				state.Snapshot.Sessions[index] = s
			}
			m.RefreshDevices()
		}
		v.importNextConversation(u)
	}}) {
		u.failures[candidate.SourceRef] = "设备操作繁忙，请重试"
		u.done++
		v.importNextConversation(u)
	}
}
func (v *View) closeConversationImport(u *conversationImportUI, finish bool) {
	if v.conversationImport != u || u.phase == conversationImporting {
		return
	}
	u.listRequest++
	u.previewRequest++
	if u.welcome {
		v.conversationImport = nil
		if finish {
			v.finishWelcome()
		} else {
			v.welcomeStep(welcomeWorkspace, -1)
		}
		return
	}
	u.phase = conversationClosing
	u.closed = v.frameTime
}
func (u *conversationImportUI) shown() []domain.ConversationCandidate {
	var out []domain.ConversationCandidate
	for _, c := range u.items {
		if (u.filter == "all" || u.filter == "imported" && c.ImportedSessionID != "" || u.filter == "available" && c.ImportedSessionID == "") && strings.Contains(strings.ToLower(c.Title), strings.ToLower(strings.TrimSpace(u.query))) {
			out = append(out, c)
		}
	}
	return out
}

func (v *View) conversationImportDialog(c *n.Context, t *n.Theme) {
	u := v.conversationImport
	if u == nil || u.welcome {
		return
	}
	closing := u.phase == conversationClosing
	if closing && (c.Preferences().ReduceMotion || c.Now().Sub(u.closed) >= 180*time.Millisecond) {
		v.conversationImport = nil
		return
	}
	open := true
	n.DialogBase(c.Key("conversation-import-dialog"), &open, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .16)).Transition(fadeMotion(c, 180*time.Millisecond))
		w, h := c.Size()
		panel.Width(min(624, w-48)).Height(min(690, h-72)).Padding(24, 32).Radius(14).Clip().Background(raised(t)).Border(1, t.Border).Shadow(0, 8, 28, 0, n.RGBA(0, 0, 0, .16)).Label("导入已有会话").Transition(popupMotion(c))
		n.Inert(panel, closing)
		panel.Children(func() {
			n.Row(c).FillWidth().Height(28).Margin(0, 0, 20).Gap(8).AlignItems(n.Baseline).Children(func() {
				n.Text(c, "导入会话").FontSize(18).FontWeight(400).FixedLineHeight(28).Grow(1)
				inlineIconText(c, monitor, v.welcomeDeviceName(u.key), n.Font{Family: t.Font, Size: 12}, 14, 8, t.TextMuted)
				if iconButton(c, closeIcon, "关闭会话导入").MarginEnd(-8).Disabled(u.phase == conversationImporting).Clicked() {
					v.closeConversationImport(u, false)
				}
			})
			v.conversationImportBody(c, t, u)
		})
		instance := n.Local(panel, "import-instance", func() *conversationImportUI { return nil })
		if *instance != u {
			*instance = u
			panel.Jump("import-visible", 0)
		}
		target := float32(1)
		if closing {
			target = 0
			c.After(180*time.Millisecond - c.Now().Sub(u.closed))
		}
		alpha := panel.AnimateTo("import-visible", target, motionAnimation(c, 180*time.Millisecond))
		panel.Opacity(alpha)
		backdrop.Opacity(alpha)
	})
	if !open {
		v.closeConversationImport(u, false)
	}
}

func (v *View) conversationImportBody(c *n.Context, t *n.Theme, u *conversationImportUI) {
	valid := v.conversationImportCurrent(u) && v.deviceState(u.key).Connected
	if !valid && u.phase != conversationClosing {
		u.message = "设备或工作区已变化，请重新打开导入"
		u.phase = conversationFailed
		u.queue = nil
	}
	n.Column(c.Key("conversation-import-module")).FillWidth().MinHeight(0).Grow(1).Children(func() {
		n.Row(c.Key("scope")).FillWidth().Height(28).Margin(0, 0, 16).Gap(8).AlignItems(n.Baseline).Children(func() {
			inlineIconText(c, workspaceIcon, u.workspace.Name, n.Font{Family: t.Font, Size: 13}, 16, 8, t.Text).Grow(1).MinWidth(0)
			var choices []menuChoice
			for _, h := range v.importHarnesses(u.key) {
				h := h
				choices = append(choices, menuChoice{id: h.ID, name: h.Name, checked: h.ID == u.harness, choose: func() {
					u.harness = h.ID
					u.selected = map[string]bool{}
					u.previewSource = ""
					u.previewRequest++
					u.failures = map[string]string{}
					v.readConversations(u, false)
				}})
			}
			label := u.harness
			for _, h := range v.importHarnesses(u.key) {
				if h.ID == u.harness {
					label = h.Name
				}
			}
			if len(choices) > 1 {
				trigger := configurationAction(c, label).MarginEnd(-8).Label("导入来源 Agent").TextColor(t.TextMuted).Disabled(u.phase == conversationImporting || !valid)
				v.choiceMenu(c, t, "import-harness", trigger, choices)
			} else {
				n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			}
		})
		page, index := "list", 0
		if u.previewSource != "" {
			page = "preview:" + u.previewSource
			index = 1
		}
		configurationTabPanel(c.Key("import-page"), page, index, configurationPanelFill, func() {
			if u.previewSource != "" {
				v.conversationPreviewView(c, t, u)
			} else {
				v.conversationImportList(c, t, u, valid)
			}
		}, false)
		n.Row(c.Key("import-feedback")).FillWidth().Height(28).AlignItems(n.Baseline).Children(func() {
			caption := u.message
			if caption != "" {
				n.Text(c.Key("message"), caption).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).SingleLine().Grow(1).Tooltip(caption).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			}
		})
		v.conversationImportFooter(c, t, u, valid)
	})
}
func (v *View) conversationImportList(c *n.Context, t *n.Theme, u *conversationImportUI, valid bool) {
	busy := u.phase == conversationImporting
	n.Row(c).FillWidth().Height(28).Margin(0, 0, 16).Gap(8).AlignItems(n.Center).Children(func() {
		textField(c.Key("search"), &u.query).Height(28).MinWidth(0).Grow(1).FontSize(12).Placeholder("搜索会话").Label("搜索导入会话").Disabled(busy).Background(t.Background).AutoFocus()
		refresh := control(c.Key("refresh"), 5).Size(28, 28).MarginEnd(-6).Label("重新扫描会话").Disabled(busy || !valid || u.phase == conversationLoading).Children(func() {
			if u.phase == conversationLoading {
				serviceSpinner(c.Key("spinner"), "重新扫描", n.Font{Family: t.Font, Size: 12}, t.TextMuted)
			} else {
				n.Icon(c, refreshIcon).Size(14, 14).TextColor(t.TextMuted)
			}
		})
		if refresh.Clicked() {
			v.readConversations(u, false)
		}
	})
	u.filter = configurationTabs(c, u.filter, []configurationTabItem{{ID: "all", Label: "全部"}, {ID: "available", Label: "可导入"}, {ID: "imported", Label: "已导入"}}, configurationUnderline, busy)
	filterIndex := slices.Index([]string{"all", "available", "imported"}, u.filter)
	configurationTabPanel(c.Key("import-filter"), u.filter, filterIndex, configurationPanelFill, func() { v.conversationImportFilteredList(c, t, u, valid, busy) }, false)
}
func (v *View) conversationImportFilteredList(c *n.Context, t *n.Theme, u *conversationImportUI, valid, busy bool) {
	shown := u.shown()
	n.Row(c).FillWidth().Height(36).Gap(8).AlignItems(n.Center).Children(func() {
		all, available := true, 0
		for _, s := range shown {
			if s.ImportedSessionID == "" {
				available++
				all = all && u.selected[s.SourceRef]
			}
		}
		all = all && available > 0
		selectAll := configurationCheckbox(c, &all, "选择当前显示", n.Font{Family: t.Font, Size: 12}).Disabled(busy || !valid || available == 0)
		if selectAll.Changed() {
			for _, s := range shown {
				if s.ImportedSessionID == "" {
					if all {
						u.selected[s.SourceRef] = true
					} else {
						delete(u.selected, s.SourceRef)
					}
				}
			}
		}
		n.Spacer(c).Grow(1)
		count := ""
		if len(u.selected) > 0 {
			count = fmt.Sprintf("已选 %d 个", len(u.selected))
		}
		n.Text(c.Key("selected-count"), count).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
	})
	if len(shown) == 0 {
		n.Column(c.Key("empty")).FillWidth().Grow(1).Justify(n.Center).AlignItems(n.Center).Gap(12).Transition(fadeMotion(c, 160*time.Millisecond)).Children(func() {
			caption := "没有找到已有会话"
			if u.query != "" {
				caption = "没有匹配的会话"
			} else if u.phase == conversationLoading {
				caption = "正在查找会话"
			} else if u.phase == conversationFailed {
				caption = "暂时无法读取会话"
			}
			if u.phase == conversationLoading {
				serviceSpinner(c, "正在查找会话", n.Font{Family: t.Font, Size: 13}, t.TextMuted)
			} else {
				n.Icon(c, searchIcon).Size(20, 20).TextColor(t.TextMuted)
			}
			n.Text(c.Key("empty-caption"), caption).FontSize(13).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		})
	} else {
		u.list.Key = func(i int) any { return shown[i].SourceRef }
		n.List(c.Key("source-list:"+u.filter), &u.list, len(shown), func(i int) { v.conversationImportRow(c, t, u, shown[i], busy || !valid) }).FillWidth().Grow(1).MinHeight(0).Label("可导入的会话列表")
	}
	if u.cursor != "" {
		if configurationAction(c, "继续扫描").Label("继续扫描历史").AlignSelf(n.Center).Disabled(busy || u.phase == conversationLoading || !valid || len(u.items) >= 1000).Clicked() {
			v.readConversations(u, true)
		}
	}
}
func (v *View) conversationImportRow(c *n.Context, t *n.Theme, u *conversationImportUI, s domain.ConversationCandidate, disabled bool) {
	alpha := float32(1)
	if at, ok := u.arrived[s.SourceRef]; ok && !c.Preferences().ReduceMotion {
		alpha = n.EaseOut(min(1, max(0, float32(c.Now().Sub(at))/float32(180*time.Millisecond))))
		if alpha < 1 {
			c.AnimationFrame()
		}
	}
	n.Row(c.Key(s.SourceRef)).FillWidth().Height(60).Opacity(alpha).PaddingY(8).Gap(12).BorderWidth(0, 0, .5, 0).BorderColor(t.Border).AlignItems(n.Start).Children(func() {
		checked := u.selected[s.SourceRef] || s.ImportedSessionID != ""
		if configurationCheckbox(c, &checked, "", n.Font{Family: t.Font, Size: 13}, s.Title).Label("选择会话：" + s.Title).Disabled(disabled || s.ImportedSessionID != "").Changed() {
			if checked {
				u.selected[s.SourceRef] = true
			} else {
				delete(u.selected, s.SourceRef)
			}
		}
		trigger := control(c.Key("preview"), 4).Grow(1).MinWidth(0).Height(44).MarginX(-8).Padding(0, 8).AlignItems(n.Start).Label("预览会话：" + s.Title).Disabled(disabled).Children(func() {
			n.Column(c).FillWidth().Gap(2).Children(func() {
				n.Text(c, s.Title).FillWidth().SingleLine().FontSize(13).FixedLineHeight(20).Tooltip(s.Title)
				date := "时间未知"
				if !s.UpdatedAt.IsZero() {
					date = s.UpdatedAt.Local().Format("1 月 2 日 15:04")
				}
				n.Text(c, date).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
			})
		})
		if trigger.Clicked() {
			v.previewConversation(u, s)
		}
		status := ""
		if s.ImportedSessionID != "" {
			status = "已导入"
		} else if s.Active {
			status = "外部运行中"
		} else if u.failures[s.SourceRef] != "" {
			status = "导入失败"
		}
		if status != "" {
			n.Row(c.Key("source-status")).Gap(5).AlignItems(n.Baseline).Transition(fadeMotion(c, 160*time.Millisecond)).Children(func() {
				if s.ImportedSessionID != "" {
					inlineSymbol(c, check, status, n.Font{Family: t.Font, Size: 11}, 12, t.TextMuted)
				}
				n.Text(c, status).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).Tooltip(u.failures[s.SourceRef]).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			})
		}
		if iconButton(c, disclosureRight, "查看会话："+s.Title).Margin(-4, 0, 0, 0).TextColor(t.TextMuted).Disabled(disabled).Clicked() {
			v.previewConversation(u, s)
		}
	})
}
func (v *View) conversationPreviewView(c *n.Context, t *n.Theme, u *conversationImportUI) {
	var candidate domain.ConversationCandidate
	for _, s := range u.items {
		if s.SourceRef == u.previewSource {
			candidate = s
		}
	}
	n.Row(c).FillWidth().MinHeight(28).Margin(0, 0, 16).Gap(6).AlignItems(n.Baseline).Children(func() {
		if iconButton(c, conversationBackIcon, "返回列表").MarginStart(-7).Clicked() {
			u.previewSource = ""
			u.previewRequest++
			u.preview = nil
			u.phase = conversationReady
			u.message = ""
		}
		n.Text(c, candidate.Title).Grow(1).MinWidth(0).SingleLine().FontSize(14).FixedLineHeight(20)
	})
	if u.phase == conversationPreviewLoading {
		n.Row(c).FillWidth().Grow(1).Justify(n.Center).AlignItems(n.Center).Children(func() {
			serviceSpinner(c, "正在读取历史", n.Font{Family: t.Font, Size: 13}, t.TextMuted)
			n.Text(c, "正在读取历史").FontSize(13).TextColor(t.TextMuted)
		})
		return
	}
	if u.preview == nil {
		n.Spacer(c).Grow(1)
		return
	}
	u.markdown.Theme = &markdown.Theme{Font: t.Font, FontSize: 13, LineHeight: 22.0 / 13, BlockSpacing: 8.0 / 13}
	u.markdown.WrapCode = true
	n.Scroll(c.Key("preview-scroll")).TrackScroll(&u.previewScroll).FillWidth().Grow(1).MinHeight(0).Label("会话历史预览").Transition(fadeMotion(c, 160*time.Millisecond)).Children(func() {
		n.Column(c).FillWidth().Gap(20).PaddingY(4).Children(func() {
			for _, item := range u.preview.Items {
				item := item
				n.Column(c.Key(item.ID)).FillWidth().Children(func() {
					if item.Kind == "user" {
						n.Column(c).MaxWidth(480).AlignSelf(n.End).Padding(10, 14).Radius(14).Background(t.Surface).Children(func() { n.Text(c, item.Text).FontSize(13).FixedLineHeight(22).Selectable() })
					} else if item.Kind == "assistant" {
						markdown.View(c, item.Text, &u.markdown).FillWidth().FontSize(13)
					} else {
						n.Text(c, strings.TrimSpace(item.Name+"\n"+item.Text)).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
					}
				})
			}
		})
	})
	caption := "可接续"
	if u.preview.ResumeRef == "" {
		caption = "仅查看"
	}
	n.Row(c).FillWidth().Height(32).BorderWidth(.5, 0, 0, 0).BorderColor(t.Border).AlignItems(n.Baseline).Children(func() {
		n.Text(c, caption).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Tooltip(u.preview.Reason)
		n.Spacer(c).Grow(1)
		if u.preview.Truncated {
			n.Text(c, "部分历史").FontSize(12).TextColor(t.TextMuted)
		}
	})
}
func (v *View) conversationImportFooter(c *n.Context, t *n.Theme, u *conversationImportUI, valid bool) {
	busy := u.phase == conversationImporting
	n.Row(c.Key("import-footer")).FillWidth().Height(52).Padding(20, 0, 0).Gap(12).AlignItems(n.Center).Children(func() {
		if u.previewSource != "" {
			if configurationAction(c, "返回列表").MarginStart(-8).Clicked() {
				u.previewSource = ""
				u.previewRequest++
				u.preview = nil
				u.phase = conversationReady
				u.message = ""
			}
			n.Spacer(c).Grow(1)
			var candidate domain.ConversationCandidate
			for _, s := range u.items {
				if s.SourceRef == u.previewSource {
					candidate = s
				}
			}
			if candidate.ImportedSessionID != "" {
				if n.PrimaryButton(c, "打开已导入会话").Height(32).PaddingX(14).Radius(6).FontSize(13).Disabled(!valid).Clicked() {
					id := candidate.ImportedSessionID
					v.closeConversationImport(u, true)
					v.navigateSession(id, u.workspace.ID)
				}
			} else {
				label := "选择此会话"
				if u.selected[u.previewSource] {
					label = "取消选择"
				}
				if n.PrimaryButton(c, label).Height(32).PaddingX(14).Radius(6).FontSize(13).Disabled(u.preview == nil || !valid).Clicked() {
					if u.selected[u.previewSource] {
						delete(u.selected, u.previewSource)
					} else {
						u.selected[u.previewSource] = true
					}
					u.previewSource = ""
					u.previewRequest++
					u.preview = nil
					u.phase = conversationReady
					u.message = ""
				}
			}
			return
		}
		back := "取消"
		if u.welcome {
			back = "返回"
		}
		if configurationAction(c, back).Label("取消导入").MarginStart(-8).Disabled(busy).Clicked() {
			v.closeConversationImport(u, false)
		}
		n.Spacer(c).Grow(1)
		if u.welcome {
			if configurationAction(c, "跳过").Label("跳过导入").TextColor(t.TextMuted).Disabled(busy).Clicked() {
				v.closeConversationImport(u, true)
			}
		}
		label := "导入"
		if u.welcome {
			label = "导入并开始"
		}
		if len(u.selected) > 0 {
			label = fmt.Sprintf("导入 %d 个", len(u.selected))
			if u.welcome {
				label += "并开始"
			}
		}
		if u.phase == conversationPartial {
			label = fmt.Sprintf("重试 %d 个会话", len(u.selected))
		}
		if u.phase == conversationFinished {
			label = "完成"
		}
		if busy {
			label = fmt.Sprintf("正在导入 %d/%d", u.done, u.total)
		}
		button := n.PrimaryButton(c, "").Height(32).PaddingX(14).Radius(6).Label("导入所选会话").Disabled(busy || !valid || len(u.selected) == 0 && u.phase != conversationFinished).Children(func() {
			n.Row(c).Gap(7).AlignItems(n.Baseline).Children(func() {
				if busy {
					serviceSpinner(c.Key("import-spinner"), label, n.Font{Family: t.Font, Size: 13}, t.AccentText).Transition(fadeMotion(c, 160*time.Millisecond))
				}
				n.Text(c.Key("import-caption"), label).FontSize(13).FixedLineHeight(20).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			})
		})
		button.Transition(n.ElementTransition{Position: true, Size: true, Duration: motionAnimation(c, 160*time.Millisecond).Duration, Ease: n.EaseOut})
		if button.Clicked() {
			if u.phase == conversationFinished {
				v.closeConversationImport(u, true)
			} else {
				v.beginConversationImport(u)
			}
		}
	})
}
