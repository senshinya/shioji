package ui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

var feedbackBell = designIcon("M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4")
var feedbackInfo = designIcon("M12 8h.01M12 12v5M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0")

type feedbackSource struct{ connection, workspace, session string }

func feedbackKey(f app.Feedback) string {
	return fmt.Sprintf("%q/%q/%q/%q", f.ConnectionID, f.WorkspaceID, f.SessionID, f.ID)
}

func (v *View) feedbackSource() feedbackSource {
	return feedbackSource{v.Model.ConnectionID(), v.Model.WorkspaceID, v.Model.SessionID}
}

type conversationMessage struct {
	app.Feedback
	remaining                   time.Duration
	tick                        time.Time
	waiting, dismissed, details bool
}
type feedbackConnection struct {
	connected bool
	since     time.Time
}
type feedbackWait struct {
	since                   time.Time
	source                  feedbackSource
	title                   string
	view                    *View
	attachmentToken, gitKey string
}
type conversationFeedback struct {
	messages    []conversationMessage
	connections map[string]feedbackConnection
	waits       map[feedbackSource]feedbackWait
	errors      map[string]string
	open        bool
}

func (v *View) feedbackState() *conversationFeedback {
	root := v.splitRoot()
	if root.feedback == nil {
		root.feedback = &conversationFeedback{connections: map[string]feedbackConnection{}, waits: map[feedbackSource]feedbackWait{}, errors: map[string]string{}}
	}
	return root.feedback
}
func sameFeedback(a, b app.Feedback) bool {
	return a.ID == b.ID && a.ConnectionID == b.ConnectionID && a.WorkspaceID == b.WorkspaceID && a.SessionID == b.SessionID
}
func (s *conversationFeedback) put(f app.Feedback, now time.Time, waiting bool) {
	f.Details = feedbackDetails(f.Details)
	lifetime := time.Duration(0)
	if !waiting {
		switch f.Kind {
		case app.FeedbackSuccess:
			lifetime = 4 * time.Second
		case app.FeedbackInformation:
			lifetime = 6 * time.Second
		}
	}
	for i := range s.messages {
		if sameFeedback(s.messages[i].Feedback, f) {
			old := s.messages[i]
			if old.Feedback == f && old.waiting == waiting {
				return
			}
			s.messages[i] = conversationMessage{Feedback: f, remaining: lifetime, tick: now, waiting: waiting, dismissed: old.dismissed && old.Kind == f.Kind, details: old.details}
			return
		}
	}
	s.messages = append(s.messages, conversationMessage{Feedback: f, remaining: lifetime, tick: now, waiting: waiting})
	if len(s.messages) > 30 {
		s.messages = append([]conversationMessage(nil), s.messages[len(s.messages)-30:]...)
	}
}
func (s *conversationFeedback) endWait(id, connection string) {
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].waiting && s.messages[i].ID == id && s.messages[i].ConnectionID == connection {
			s.messages = append(s.messages[:i], s.messages[i+1:]...)
		}
	}
}
func (v *View) reportFeedback(f app.Feedback) {
	if f.ConnectionID == "" {
		f.ConnectionID = v.Model.ConnectionID()
	}
	if f.WorkspaceID == "" {
		f.WorkspaceID = v.Model.WorkspaceID
	}
	if f.SessionID == "" {
		f.SessionID = v.Model.SessionID
	}
	if f.ID == "" {
		f.ID = domain.NewID()
	}
	v.feedbackState().put(f, v.frameTime, false)
}

// Only known credential-bearing syntax is retained as a redacted diagnostic.
// Summaries never include arbitrary error strings or authorization URLs.
var feedbackURL = regexp.MustCompile(`https?://[^\s<>"']+`)
var feedbackCredential = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+)?|(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|(?:client[_-]?)?secret)\s*[:=]\s*)[^\s,;]+`)

func feedbackDetails(message string) string {
	message = feedbackURL.ReplaceAllStringFunc(message, func(raw string) string {
		u, e := url.Parse(raw)
		if e != nil {
			return "[链接已隐藏]"
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	})
	return feedbackCredential.ReplaceAllString(message, "${1}[已隐藏]")
}

func (v *View) syncConversationFeedback(c *n.Context) {
	m := v.Model
	s := v.feedbackState()
	source := v.feedbackSource()
	now := c.Now()
	for source, wait := range s.waits {
		active := false
		if wait.view != nil {
			active = wait.attachmentToken != "" && wait.view.attachmentPreparing[wait.attachmentToken] > 0 || wait.gitKey != "" && wait.view.gitLoading(wait.gitKey)
		}
		if !active {
			s.endWait("prepare:"+source.workspace+":"+source.session, source.connection)
			delete(s.waits, source)
		}
	}
	for _, f := range m.Feedbacks {
		s.put(f, now, false)
		if f.Kind == app.FeedbackSubmissionUncertain && f.ConnectionID == source.connection && f.WorkspaceID == source.workspace && f.SessionID == source.session {
			for i := range s.messages {
				if sameFeedback(s.messages[i].Feedback, f) {
					s.messages[i].dismissed = true
				}
			}
		}
	}
	m.Feedbacks = nil
	// Existing error producers are observed once. Asynchronous jobs additionally
	// capture their actual source at dispatch, rather than the selected pane later.
	if s.errors[source.connection] != m.Error {
		s.errors[source.connection] = m.Error
		if m.Error != "" && m.Error != app.ModelUnavailableMessage && m.Error != v.draftSaveError && !v.conversationErrorHasState() {
			v.reportFeedback(app.Feedback{Title: "操作未完成", Body: "操作未能完成，原有内容已保留。", Details: m.Error})
		}
	}
	for key, observation := range s.connections {
		if key == source.connection {
			continue
		}
		if device := m.DeviceStates[key]; device != nil && device.Connected && !observation.connected {
			s.endWait("connection", key)
			s.put(app.Feedback{ID: domain.NewID(), ConnectionID: key, Title: "已重新连接", Body: "草稿没有自动提交。", Kind: app.FeedbackSuccess}, now, false)
			s.connections[key] = feedbackConnection{connected: true, since: now}
		} else if !observation.connected && now.Sub(observation.since) >= 5*time.Second {
			s.endWait("connection", key)
		}
	}
	connection, known := s.connections[source.connection]
	if !known {
		connection = feedbackConnection{connected: m.Connected, since: now}
	}
	if connection.connected != m.Connected {
		if m.Connected {
			s.endWait("connection", source.connection)
			s.put(app.Feedback{ID: domain.NewID(), ConnectionID: source.connection, Title: "已重新连接", Body: "草稿没有自动提交。", Kind: app.FeedbackSuccess}, now, false)
		}
		connection = feedbackConnection{connected: m.Connected, since: now}
	}
	s.connections[source.connection] = connection
	if !m.Connected {
		elapsed := now.Sub(connection.since)
		if elapsed >= 5*time.Second {
			s.endWait("connection", source.connection)
		} else if elapsed >= 400*time.Millisecond {
			s.put(app.Feedback{ID: "connection", ConnectionID: source.connection, Title: "正在连接执行服务", Body: "草稿和队列已保留。"}, now, true)
		}
		if elapsed < 400*time.Millisecond {
			c.After(400*time.Millisecond - elapsed)
		} else if elapsed < 5*time.Second {
			c.After(5*time.Second - elapsed)
		}
	}
	waitTitle := ""
	if v.preparingAttachments(m.DraftKey()) {
		waitTitle = "正在准备附件"
	} else if key, _ := v.gitQuery(); !v.devices && v.gitLoading(key) && v.feedbackGitWait == key {
		waitTitle = "正在核对设备上的分支"
	}
	wait, hasWait := s.waits[source]
	if waitTitle == "" {
		if hasWait {
			s.endWait("prepare:"+source.workspace+":"+source.session, source.connection)
			delete(s.waits, source)
		}
	} else {
		if !hasWait || wait.title != waitTitle {
			wait = feedbackWait{since: now, source: source, title: waitTitle, view: v}
			if waitTitle == "正在准备附件" {
				wait.attachmentToken = v.attachmentPreparationKey(m.DraftKey())
			} else {
				wait.gitKey, _ = v.gitQuery()
			}
			s.waits[source] = wait
		}
		if elapsed := now.Sub(wait.since); elapsed >= 400*time.Millisecond {
			s.put(app.Feedback{ID: "prepare:" + source.workspace + ":" + source.session, ConnectionID: source.connection, WorkspaceID: source.workspace, SessionID: source.session, Title: waitTitle, Body: "完成后可再次发送，草稿保持可编辑。"}, now, true)
		} else {
			c.After(400*time.Millisecond - elapsed)
		}
	}
}

func (v *View) conversationErrorHasState() bool {
	m := v.Model
	_, hasHistoryAction := m.HistoryActions[m.SessionID]
	if hasHistoryAction || !m.Connected || m.RunReadiness().State != "ready" || v.draftSaveError != "" || m.ArchivedContext() != "" {
		return true
	}
	for _, in := range m.Pending {
		if in.WorkspaceID == m.WorkspaceID && in.SessionID == m.SessionID {
			return true
		}
	}
	return false
}

func (v *View) feedbackErrorCovered(m *app.Model, source feedbackSource) bool {
	for id, in := range m.Pending {
		owner := m.PendingDevices[id]
		if owner == "" {
			owner = "local"
		}
		if owner == source.connection && in.SessionID == source.session && in.WorkspaceID == source.workspace {
			return true
		}
	}
	_, history := m.HistoryActions[source.session]
	if history {
		return true
	}
	for _, s := range m.Snapshot.Sessions {
		if s.ID == source.session && (s.ContextChanged || s.ExecutionGuard != "") {
			return true
		}
	}
	return false
}

type conversationStatus struct {
	id, title, body, details string
	icon                     *n.SVG
	compact                  bool
	priority                 int
	actions                  []statusAction
}
type statusAction struct {
	label              string
	accessibilityLabel string
	disabled           bool
	run                func(*n.Context)
}

func (v *View) showFeedbackDevice() {
	v.deviceSelected = v.Model.ConnectionID()
	v.deviceList = false
	v.devices = true
}
func (v *View) conversationStatuses(now time.Time) []conversationStatus {
	m := v.Model
	source := v.feedbackSource()
	var out []conversationStatus
	device := statusAction{label: "设备详情", run: func(*n.Context) { v.showFeedbackDevice() }}
	if v.draftSaveError != "" {
		out = append(out, conversationStatus{id: "save", title: "草稿尚未保存", body: "本次修改仍保留在当前窗口，请重试保存，或先复制草稿。", details: v.draftSaveError, icon: fileAttachmentIcon, priority: 0, actions: []statusAction{{label: "重试保存", disabled: v.Saver == nil, run: func(*n.Context) { v.lastQueued = nil; v.queueState() }}, {label: "复制草稿", run: func(c *n.Context) {
			v.collectBindings()
			c.WriteClipboard(referencePlainText(referenceDocumentText(v.composerContent(m.DraftKey()))))
			v.reportFeedback(app.Feedback{Title: "草稿已复制", Kind: app.FeedbackSuccess})
		}}}})
	}
	ids := []string{}
	for id, in := range m.Pending {
		owner := m.PendingDevices[id]
		if owner == "" {
			owner = "local"
		}
		if owner == source.connection && in.WorkspaceID == source.workspace && in.SessionID == source.session && m.UncertainSubmissions[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		id := id
		out = append(out, conversationStatus{id: "submit:" + id, title: "提交结果待核对", body: "尚未确认消息是否已被接纳。原文与附件已保留，请先核对结果。", icon: feedbackInfo, priority: 1, actions: []statusAction{{label: "核对提交结果", disabled: !m.Connected || v.Controller == nil, run: func(*n.Context) { v.querySubmission(id, source) }}, {label: "查看原文", run: func(*n.Context) { v.feedbackOriginal = !v.feedbackOriginal }}}})
	}
	if status := v.historyConversationStatus(); status != nil {
		out = append(out, *status)
	}
	d := m.Details[m.SessionID]
	if d.Session.Status.NeedsRecovery() || d.Session.Recovery != nil && d.Session.Recovery.CanContinue && (d.Session.Status == "abandoned" || d.Session.Status == "interrupted" || d.Session.Status == "failed") {
		out = append(out, conversationStatus{id: "recovery", priority: 2})
	}
	if d.Session.ContextChanged || d.Session.ExecutionGuard != "" {
		title, body := "执行分支已改变", "请在分支选择中核对并确认当前执行上下文。"
		if d.Session.ExecutionGuard != "" {
			title = "当前操作无法继续"
			body = "执行状态需要核对，草稿与队列已保留。"
		}
		out = append(out, conversationStatus{id: "context", title: title, body: body, details: d.Session.ExecutionGuard, icon: branchIcon, priority: 3, actions: []statusAction{{label: "查看分支", disabled: !m.Connected, run: func(*n.Context) { v.branchOpen = true; v.branchSearch = ""; v.requestGit(true) }}, device}})
	}
	for _, a := range m.Attachments[m.DraftKey()] {
		if a.Error != "" {
			out = append(out, conversationStatus{id: "attachments", title: "附件暂时无法用于发送", body: "部分附件或引用尚未完成准备，正文和其他附件已保留。", details: a.Error, icon: fileAttachmentIcon, priority: 4, actions: []statusAction{{label: "重试准备", disabled: v.Controller == nil || !m.Connected, run: func(*n.Context) {
				key := m.DraftKey()
				for i, a := range m.Attachments[key] {
					if a.Error != "" {
						a.Error = ""
						m.Attachments[key][i] = a
						v.uploadDraft(key, i)
					}
				}
			}}}})
			break
		}
	}
	if m.Connected && m.RunReadiness().State != "ready" {
		out = append(out, conversationStatus{id: "runtime", title: "当前 Agent 尚未就绪", body: "请在所属设备的详情中检查 Agent 配置。草稿已保留。", details: m.RunReadiness().Error, icon: feedbackInfo, priority: 5, actions: []statusAction{device}})
	}
	if observation, ok := v.feedbackState().connections[source.connection]; !m.Connected && ok && now.Sub(observation.since) >= 5*time.Second {
		out = append(out, conversationStatus{id: "connection", title: "无法连接执行设备", body: "正在重新连接设备，草稿和队列已保留。恢复连接后不会自动发送草稿。", icon: recoveryIcon, priority: 5, actions: []statusAction{{label: "重试连接", disabled: v.Controller == nil, run: func(*n.Context) { v.do(app.Job{Method: "$reconnect"}) }}, device}})
	}
	if kind := m.ArchivedContext(); kind != "" {
		out = append(out, conversationStatus{id: "archived", title: kind + "已归档", icon: feedbackInfo, compact: true, priority: 6, actions: []statusAction{{label: "恢复" + kind, disabled: !v.canManage(source.connection), run: func(*n.Context) { v.restoreArchivedContext(kind) }}}})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].priority < out[j].priority })
	return out
}

func (v *View) querySubmission(id string, source feedbackSource) {
	m := v.Model
	in, ok := m.Pending[id]
	if !ok || source != v.feedbackSource() {
		return
	}
	v.do(app.Job{Method: "operation", Input: struct {
		ID string `json:"id"`
	}{id}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if err != nil {
			m.ReportFeedback(app.Feedback{ConnectionID: source.connection, WorkspaceID: source.workspace, SessionID: source.session, Title: "尚未核对提交结果", Details: err.Error()})
			return
		}
		var op domain.Operation
		if err := json.Unmarshal(b, &op); err != nil || op.ID != in.OperationID {
			m.ReportFeedback(app.Feedback{ConnectionID: source.connection, WorkspaceID: source.workspace, SessionID: source.session, Title: "提交回执无法核对"})
			return
		}
		m.ApplyOperation(op)
	}})
}
func (v *View) renderConversationStatus(c *n.Context, t *n.Theme, s conversationStatus, count, index int) {
	source := v.feedbackSource()
	foldKey := "feedback:" + source.connection + ":" + source.workspace + ":" + source.session
	folded := v.Model.Collapsed[foldKey] && !s.compact
	panel := processingPanelSurface(n.Column(c.Key("status:"+s.id)).FillWidth().Padding(10, processingPanelInset).Gap(6).Label("会话状态面板"), t)
	if s.compact {
		panel.Height(44).Padding(7, processingPanelInset)
	}
	panel.Children(func() {
		n.Row(c).FillWidth().Height(28).Gap(processingPanelIconGap).AlignItems(n.Baseline).Children(func() {
			font := n.Font{Family: t.Font, Size: 13, Weight: 500}
			inlineSymbol(c, s.icon, s.title, font, 14, t.TextMuted)
			n.Text(c, s.title).FontSize(13).FixedLineHeight(20).FontWeight(500).Grow(1)
			v.feedbackStatusPager(c, t, count, index)
			if s.compact {
				for _, a := range s.actions {
					if ghost(c, a.label).Height(28).FontSize(12).Disabled(a.disabled).Clicked() {
						a.run(c)
					}
				}
			} else {
				label, icon := "折叠会话状态", disclosureDown
				if folded {
					label, icon = "展开会话状态", disclosureRight
				}
				if iconButton(c, icon, label).Size(28, 28).Clicked() {
					v.Model.Collapsed[foldKey] = !folded
				}
			}
		})
		if folded || s.compact {
			return
		}
		n.Column(c).FillWidth().PaddingStart(processingPanelTextInset).Gap(12).Children(func() {
			if s.body != "" {
				n.Text(c, s.body).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			}
			n.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				for i, a := range s.actions {
					action := ghost(c, a.label).Height(28).FontSize(12).Disabled(a.disabled)
					if a.accessibilityLabel != "" {
						action.Label(a.accessibilityLabel)
					}
					if i == 0 {
						action.Background(t.Accent).TextColor(t.AccentText).PaddingX(10)
					}
					if action.Clicked() && source == v.feedbackSource() {
						a.run(c)
					}
				}
				if s.details != "" {
					if ghost(c, "查看详情").FontSize(12).Height(28).Clicked() {
						v.feedbackDetail = !v.feedbackDetail
					}
				}
			})
			if v.feedbackDetail && s.details != "" {
				processingPanelDivider(c, t)
				n.Text(c, feedbackDetails(s.details)).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
			}
			if v.feedbackOriginal && strings.HasPrefix(s.id, "submit:") {
				if in, ok := v.Model.Pending[strings.TrimPrefix(s.id, "submit:")]; ok {
					n.Text(c, in.Text).FillWidth().FontSize(12).FixedLineHeight(20).Selectable()
				}
			}
		})
	})
}
func (v *View) conversationStatusPanel(c *n.Context, t *n.Theme) {
	v.syncConversationFeedback(c)
	statuses := v.conversationStatuses(c.Now())
	scope := v.feedbackSource()
	if scope != v.feedbackScope {
		v.feedbackScope = scope
		v.feedbackSelection = 0
		v.feedbackPinnedID = ""
		v.feedbackDetail = false
		v.feedbackOriginal = false
	}
	v.feedbackStatuses = statuses
	if v.feedbackPinnedID != "" {
		found := false
		for i, s := range statuses {
			if s.id == v.feedbackPinnedID {
				v.feedbackSelection = i
				found = true
				break
			}
		}
		if !found {
			v.feedbackPinnedID = ""
			v.feedbackSelection = 0
		}
	}
	v.feedbackSelection = min(max(0, v.feedbackSelection), max(0, len(statuses)-1))
	frame := n.Column(c.Key("conversation-status")).FillWidth().MaxWidth(768).Clip()
	var selected conversationStatus
	if len(statuses) > 0 {
		selected = statuses[v.feedbackSelection]
	}
	if selected.id != v.feedbackStatusID {
		v.feedbackDetail, v.feedbackOriginal = false, false
		if v.requestBeingEdited() {
			v.Model.Collapsed["feedback:"+scope.connection+":"+scope.workspace+":"+scope.session] = true
		}
		v.feedbackStatusID = selected.id
	}
	active := selected.id != "" && selected.id != "recovery"
	retained := n.Local(frame, "status", func() conversationStatus { return selected })
	if active {
		*retained = selected
	}
	gap := frame.AnimateTo("gap", map[bool]float32{true: 8, false: 0}[active], motionAnimation(c, 240*time.Millisecond))
	dockGap := float32(0)
	if scope.session != "" {
		dockGap = 8
	}
	frame.Margin(0, 0, gap-dockGap, 0)
	animatedContentHeight(c, frame, active, 240*time.Millisecond, func() float32 {
		v.renderConversationStatus(c, t, *retained, len(statuses), v.feedbackSelection)
		return 0
	})
	d := v.Model.Details[v.Model.SessionID]
	if selected.id != "recovery" {
		d.Session.Status = "completed"
		d.Session.Recovery = nil
	}
	if scope.session != "" {
		v.recovery(c, t, d)
	}
}

func (v *View) feedbackScopeLabel(f app.Feedback) string {
	m := v.persistenceModel()
	parts := []string{}
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == f.WorkspaceID {
			parts = append(parts, w.Name)
			break
		}
	}
	for _, s := range m.Snapshot.Sessions {
		if s.ID == f.SessionID {
			parts = append(parts, s.Title)
			break
		}
	}
	name := m.LocalName()
	for _, d := range m.Connections {
		if d.ID == f.ConnectionID {
			name = d.Name
			break
		}
	}
	return strings.Join(append(parts, name), " · ")
}
func (v *View) feedbackButton(c *n.Context, t *n.Theme) {
	root := v.splitRoot()
	state := root.feedbackState()
	button := iconButton(c, feedbackBell, "最近消息").Tooltip("最近消息")
	if button.Clicked() {
		state.open = !state.open
	}
	popover(c.Key("recent-feedback"), button, &state.open, func(panel n.Element) {
		panel.Width(360).MaxWidth(360).Padding(12).Radius(10).Border(1, t.Border).Background(raised(t)).Children(func() {
			n.Text(c, "最近消息").FontSize(13).FixedLineHeight(20)
			n.Scroll(c).FillWidth().MaxHeight(400).Children(func() {
				n.Column(c).FillWidth().Gap(12).Children(func() {
					if len(state.messages) == 0 {
						n.Text(c, "暂无消息").FontSize(12).TextColor(t.TextMuted)
					}
					for i := len(state.messages) - 1; i >= 0; i-- {
						message := state.messages[i]
						if message.waiting {
							continue
						}
						n.Column(c.Key(feedbackKey(message.Feedback))).FillWidth().Gap(4).Children(func() {
							processingPanelDivider(c, t)
							n.Text(c, root.feedbackScopeLabel(message.Feedback)).FontSize(11).TextColor(t.TextMuted).FillWidth()
							n.Text(c, message.Title).FontSize(13).FixedLineHeight(20)
							if message.Body != "" {
								n.Text(c, message.Body).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
							}
							if message.SessionID != "" {
								if ghost(c, "查看会话").FontSize(12).Height(28).Clicked() {
									root.openFeedbackSource(message.Feedback)
									state.open = false
								}
							}
							if message.Details != "" {
								if ghost(c, "查看记录详情").FontSize(12).Height(28).Clicked() {
									state.messages[i].details = !state.messages[i].details
								}
								if state.messages[i].details {
									n.Text(c, message.Details).FillWidth().FontSize(12).FixedLineHeight(20).Selectable()
								}
							}
						})
					}
				})
			})
		})
	})
}
func (v *View) openFeedbackSource(f app.Feedback) {
	m := v.Model
	for _, s := range m.Snapshot.Sessions {
		if s.ID == f.SessionID && s.WorkspaceID == f.WorkspaceID && m.WorkspaceConnection(s.WorkspaceID) == f.ConnectionID {
			v.navigateSession(s.ID, s.WorkspaceID)
			return
		}
	}
}
func (v *View) conversationMessageOverlay(c *n.Context, t *n.Theme) {
	if v.parent != nil {
		return
	}
	state := v.feedbackState()
	now := c.Now()
	n.Overlay(c.Key("conversation-feedback"), func() {
		active := -1
		for i := len(state.messages) - 1; i >= 0; i-- {
			if !state.messages[i].dismissed {
				active = i
				break
			}
		}
		if active < 0 {
			return
		}
		message := &state.messages[active]
		width, _ := c.Size()
		toast := n.Column(c.Key("toast:"+feedbackKey(message.Feedback))).Absolute().Right(16).Top(56).Width(min(336, width-32)).Padding(8, 12).Radius(10).Background(raised(t)).Border(1, t.Border).Shadow(0, 6, 18, 0, n.RGBA(0, 0, 0, .08)).Gap(8).Label("弹出消息").Transition(fadeMotion(c, 180*time.Millisecond))
		paused := state.open || toast.Hovered() || toast.FocusWithin()
		for i := range state.messages {
			entry := &state.messages[i]
			if !entry.waiting && (entry.Kind == app.FeedbackSuccess || entry.Kind == app.FeedbackInformation) && !entry.dismissed {
				if i == active && !paused && !entry.tick.IsZero() {
					entry.remaining -= now.Sub(entry.tick)
				}
				if entry.remaining <= 0 {
					entry.dismissed = true
				}
				entry.tick = now
				if i == active && !paused {
					c.After(max(time.Millisecond, entry.remaining))
				}
			}
		}
		if message.dismissed || state.open {
			toast.Hidden(true)
			return
		}
		toast.Children(func() {
			n.Row(c).FillWidth().Gap(8).AlignItems(n.Start).Children(func() {
				icon := feedbackInfo
				if message.Kind == app.FeedbackSuccess {
					icon = check
				}
				n.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					sourceLabel := v.feedbackScopeLabel(message.Feedback)
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						font := n.Font{Family: t.Font, Size: 11, Weight: 400}
						inlineSymbol(c, icon, sourceLabel, font, 14, t.TextMuted).Label("消息来源图标")
						n.Text(c, sourceLabel).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).FillWidth()
					})
					n.Text(c, message.Title).PaddingStart(22).FontSize(13).FixedLineHeight(20).FontWeight(500).FillWidth()
				})
				if iconButton(c, closeIcon, "关闭消息").Size(24, 24).Clicked() {
					message.dismissed = true
				}
			})
			n.Column(c).FillWidth().PaddingStart(22).Gap(8).Children(func() {
				if message.Body != "" {
					n.Text(c, message.Body).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
				}
				if message.SessionID != "" && message.SessionID != v.Model.SessionID {
					if ghost(c, "查看会话").FontSize(12).Height(24).Clicked() {
						v.openFeedbackSource(message.Feedback)
					}
				}
				if message.Details != "" {
					if ghost(c, "查看消息详情").FontSize(12).Height(24).Clicked() {
						message.details = !message.details
					}
					if message.details {
						n.Scroll(c).FillWidth().MaxHeight(120).Children(func() { n.Text(c, message.Details).FillWidth().FontSize(12).FixedLineHeight(20).Selectable() })
					}
				}
			})
		})
	})
}

func (v *View) feedbackStatusPager(c *n.Context, t *n.Theme, count, index int) {
	if count < 2 {
		return
	}
	n.Text(c, fmt.Sprintf("%d / %d", index+1, count)).FontSize(11).TextColor(t.TextMuted)
	for _, step := range []int{-1, 1} {
		label, text := "上一个状态", "‹"
		if step > 0 {
			label, text = "下一个状态", "›"
		}
		next := index + step
		if ghost(c, text).Size(28, 28).Label(label).Disabled(next < 0 || next >= count).Clicked() {
			v.feedbackSelection = next
			v.feedbackPinnedID = v.feedbackStatuses[next].id
		}
	}
}
func (v *View) requestBeingEdited() bool {
	for _, r := range v.Model.Details[v.Model.SessionID].Requests {
		if !r.State.AwaitingResolution() {
			continue
		}
		if v.composerHadFocus {
			return true
		}
		prefix := r.SessionID + "/" + r.ID + "/"
		for key, answer := range v.Model.Answers {
			if strings.HasPrefix(key, prefix) && answer != "" {
				return true
			}
		}
		for key, answer := range v.secretAnswers {
			if strings.HasPrefix(key, prefix) && answer != "" {
				return true
			}
		}
	}
	return false
}
