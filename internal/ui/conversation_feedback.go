package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

var feedbackBell = designIcon("M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4")
var feedbackInfo = designIcon("M12 8h.01M12 12v5M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0")
var feedbackTrash = designIcon("M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7")

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
	inline                      bool
}
type feedbackWait struct {
	since           time.Time
	source          feedbackSource
	title           string
	view            *View
	attachmentToken string
}
type conversationFeedback struct {
	messages     []conversationMessage
	clearing     []conversationMessage
	clearStarted time.Time
	waits        map[feedbackSource]feedbackWait
	open         bool
	replaying    string
	focusRecent  bool
}

func (v *View) feedbackState() *conversationFeedback {
	root := v.splitRoot()
	if root.feedback == nil {
		root.feedback = &conversationFeedback{waits: map[feedbackSource]feedbackWait{}}
	}
	return root.feedback
}
func sameFeedback(a, b app.Feedback) bool {
	return a.ID == b.ID && a.ConnectionID == b.ConnectionID && a.WorkspaceID == b.WorkspaceID && a.SessionID == b.SessionID
}
func feedbackLifetime(kind app.FeedbackKind) time.Duration {
	switch kind {
	case app.FeedbackSuccess:
		return 4 * time.Second
	case app.FeedbackInformation:
		return 6 * time.Second
	}
	return 0
}
func (s *conversationFeedback) put(f app.Feedback, now time.Time, waiting bool) {
	f.Details = feedbackDetails(f.Details)
	lifetime := time.Duration(0)
	if !waiting {
		lifetime = feedbackLifetime(f.Kind)
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

func (s *conversationFeedback) replay(key string, now time.Time) {
	for i := range s.messages {
		entry := &s.messages[i]
		if entry.waiting || feedbackKey(entry.Feedback) != key {
			continue
		}
		entry.dismissed, entry.details = false, false
		entry.remaining, entry.tick = feedbackLifetime(entry.Kind), now
		s.replaying, s.open, s.focusRecent = key, false, false
		return
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

// Redact diagnostics before they are used in either the visible message or
// the expandable details.
var feedbackURL = regexp.MustCompile(`https?://[^\s<>"']+`)
var feedbackAuthorization = regexp.MustCompile(`(?i)((?:"|')?authorization(?:"|')?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\r\n]+)`)
var feedbackCredential = regexp.MustCompile(`(?i)((?:"|')?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|(?:client[_-]?)?secret)(?:"|')?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;}]+)`)

func feedbackDetails(message string) string {
	message = feedbackURL.ReplaceAllStringFunc(message, func(raw string) string {
		u, e := url.Parse(raw)
		if e != nil {
			return "[链接已隐藏]"
		}
		u.User = nil
		u.Path = ""
		u.RawPath = ""
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	})
	message = feedbackAuthorization.ReplaceAllString(message, "${1}[已隐藏]")
	return feedbackCredential.ReplaceAllString(message, "${1}[已隐藏]")
}

// Keep the original feedback for identity, updates and replay. Both the popup
// and recent list use the same reading order: reason, then useful guidance.
func isFeedbackTimeout(message string) bool {
	message = strings.TrimSpace(message)
	return message == context.DeadlineExceeded.Error() || strings.HasSuffix(message, ": "+context.DeadlineExceeded.Error()) ||
		message == "i/o timeout" || strings.HasSuffix(message, ": i/o timeout")
}

func feedbackCopy(f app.Feedback) (title, body, details string) {
	title, body, details = f.Title, f.Body, strings.TrimSpace(f.Details)
	if isFeedbackTimeout(body) {
		body = "等待设备响应超时。"
	}
	if isFeedbackTimeout(details) {
		if title == "" {
			title = "设备响应超时"
		} else if body == "" {
			body = "等待设备响应超时。"
		}
		return title, body, ""
	}
	if strings.Contains(details, "exec: WaitDelay expired before I/O complete") && strings.Contains(details, "Git:") {
		if title == "" {
			title = "Git 未返回完整结果"
		}
		return
	}
	if title == "" {
		line, _, _ := strings.Cut(details, "\n")
		first := []rune(line)
		title = string(first[:min(160, len(first))])
		if len(first) > 160 {
			title += "…"
		}
		if details == title {
			details = ""
		}
		return
	}
	if details == "" || f.Kind == app.FeedbackSuccess || f.Kind == app.FeedbackInformation {
		return
	}
	generic := title == "操作未完成"
	if generic && body == "操作未能完成，原有内容已保留。" {
		body = ""
	}
	if len([]rune(details)) <= 160 && strings.Count(details, "\n") < 3 {
		if generic {
			title = details
		} else if details != title && details != body {
			if body == "" {
				body = details
			} else {
				body = details + "\n" + body
			}
		}
		details = ""
	} else {
		line, _, _ := strings.Cut(details, "\n")
		preview := []rune(line)
		summary := string(preview[:min(160, len(preview))]) + "…"
		if body == "" {
			body = summary
		} else {
			body = summary + "\n" + body
		}
	}
	return
}

func (v *View) syncConversationFeedback(c *n.Context) {
	m := v.Model
	s := v.feedbackState()
	source := v.feedbackSource()
	now := c.Now()
	for source, wait := range s.waits {
		active := false
		if wait.view != nil {
			active = wait.attachmentToken != "" && wait.view.attachmentPreparing[wait.attachmentToken] > 0
		}
		if !active {
			s.endWait("prepare:"+source.workspace+":"+source.session, source.connection)
			delete(s.waits, source)
		}
	}
	for _, f := range m.Feedbacks {
		s.put(f, now, f.Kind == app.FeedbackSubmissionUncertain)
		sameSource := f.ConnectionID == source.connection && f.WorkspaceID == source.workspace && f.SessionID == source.session
		if sameSource && f.Details != "" && f.Details == m.Error {
			v.feedbackObservedError = m.Error
		}
		if f.Kind == app.FeedbackSubmissionUncertain || sameSource && f.Kind == app.FeedbackSubmissionFailed {
			for i := range s.messages {
				if sameFeedback(s.messages[i].Feedback, f) {
					s.messages[i].inline = true
				}
			}
		}
	}
	m.Feedbacks = nil
	s.messages = slices.DeleteFunc(s.messages, func(message conversationMessage) bool {
		return message.Kind == app.FeedbackSubmissionUncertain && !m.UncertainSubmissions[message.ID]
	})
	for id := range v.submissionCheckError {
		if _, ok := m.Pending[id]; !ok {
			delete(v.submissionCheckError, id)
		}
	}
	// Existing error producers are observed once. Asynchronous jobs additionally
	// capture their actual source at dispatch, rather than the selected pane later.
	if v.feedbackObservedError != m.Error {
		v.feedbackObservedError = m.Error
		if m.Error != "" && !isFeedbackTimeout(m.Error) && m.Error != app.ModelUnavailableMessage && m.Error != v.splitRoot().draftSaveError && !v.conversationErrorHasState() {
			v.reportFeedback(app.Feedback{Details: m.Error})
		}
	}
	waitTitle := ""
	if v.attachmentPreparing[v.attachmentPreparationKey(m.DraftKey())] > 0 {
		waitTitle = "正在准备附件"
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
			wait.attachmentToken = v.attachmentPreparationKey(m.DraftKey())
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
	if hasHistoryAction || !m.Connected || v.currentConnectionPhase() != app.ConnectionIdle || m.RunReadiness().State != "ready" || v.splitRoot().draftSaveError != "" || m.ArchivedContext() != "" {
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
func (v *View) conversationStatuses(c *n.Context) []conversationStatus {
	m := v.Model
	source := v.feedbackSource()
	var out []conversationStatus
	device := statusAction{label: "设备详情", run: func(*n.Context) { v.showFeedbackDevice() }}
	if v.splitRoot().draftSaveError != "" {
		out = append(out, conversationStatus{id: "save", title: "草稿尚未保存", body: "本次修改仍保留在当前窗口，请重试保存，或先复制草稿。", details: v.splitRoot().draftSaveError, icon: fileAttachmentIcon, priority: 0, actions: []statusAction{{label: "重试保存", disabled: v.splitRoot().Saver == nil, run: func(*n.Context) { root := v.splitRoot(); root.lastQueued = nil; root.queueState() }}, {label: "复制草稿", run: func(c *n.Context) {
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
		// Fast receipts can settle during the automatic lookup. Keep the
		// warning out of the way while that first lookup catches up.
		if m.Connected && v.submissionCheckError[id] == "" {
			if remaining := v.submissionWarningDelay(c.Now(), id, source); remaining > 0 {
				c.After(remaining)
				continue
			}
		}
		out = append(out, conversationStatus{id: "submit:" + id, title: "消息是否送达仍待确认", body: submissionCheckMessage(m.Connected, v.submissionCheckError[id]), details: v.submissionCheckError[id], icon: feedbackInfo, priority: 1, actions: []statusAction{{label: "查看发送结果", disabled: !m.Connected || v.Controller == nil, run: func(*n.Context) { v.querySubmission(id, source) }}, {label: "查看原文", run: func(*n.Context) { v.feedbackOriginal = !v.feedbackOriginal }}}})
	}
	if !m.HasPendingInput() {
		if notice := v.submissionFailure(); notice != nil {
			out = append(out, conversationStatus{id: "send-error:" + notice.ID, title: notice.Title, details: notice.Details, icon: feedbackInfo, priority: 1,
				actions: []statusAction{{label: "返回草稿", run: func(*n.Context) {
					v.dismissSubmissionFailure()
					v.focusComposer = true
				}}}})
		}
	}
	if status := v.stopConversationStatus(); status != nil {
		out = append(out, *status)
	}
	if status := v.historyConversationStatus(); status != nil {
		out = append(out, *status)
	}
	if status := v.importedConversationStatus(); status != nil {
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
		if a.Error != "" && a.Attachment.Kind != domain.PathAttachmentResource {
			out = append(out, conversationStatus{id: "attachments", title: "附件暂时无法用于发送", body: "部分附件或引用尚未完成准备，正文和其他附件已保留。", details: a.Error, icon: fileAttachmentIcon, priority: 4, actions: []statusAction{{label: "重试准备", disabled: v.Controller == nil || !m.Connected, run: func(*n.Context) {
				key := m.DraftKey()
				for i, a := range m.Attachments[key] {
					if a.Error != "" && a.Attachment.Kind != domain.PathAttachmentResource {
						a.Error = ""
						m.Attachments[key][i] = a
						v.uploadDraft(key, i)
					}
				}
			}}}})
			break
		}
	}
	if title := v.connectionBlocker(); title != "" && !v.loadingBlocksComposer() {
		action := v.connectionRecoveryAction()
		body := ""
		actions := []statusAction{action}
		if v.currentConnectionPhase() == app.ConnectionSyncFailed {
			body = "正在重新同步。"
		} else {
			actions = append(actions, device)
		}
		out = append(out, conversationStatus{id: "connection", title: title, body: body, icon: recoveryIcon, priority: 5, actions: actions})
	}
	if title, details := v.agentBlocker(); title != "" {
		out = append(out, conversationStatus{id: "runtime", title: title, details: details, icon: feedbackInfo, priority: 5, actions: []statusAction{{label: "打开 Agent 设置", run: func(*n.Context) {
			v.showFeedbackDevice()
			v.deviceTab = "agents"
		}}}})
	}
	if kind := m.ArchivedContext(); kind != "" {
		session := m.SessionID
		if kind == "工作区" {
			session = ""
		}
		pending := v.pendingArchiveChange(m.WorkspaceID, session)
		label := "恢复" + kind
		if pending != nil {
			label = pending.label()
		}
		out = append(out, conversationStatus{id: "archived", title: kind + "已归档", icon: feedbackInfo, compact: true, priority: 6, actions: []statusAction{{label: label, disabled: pending != nil || !v.canManage(source.connection), run: func(*n.Context) { v.restoreArchivedContext(kind) }}}})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].priority < out[j].priority })
	return out
}

func (v *View) submissionFailure() *conversationMessage {
	source := v.feedbackSource()
	entries := v.feedbackState().messages
	for i := len(entries) - 1; i >= 0; i-- {
		f := &entries[i]
		if f.Kind == app.FeedbackSubmissionFailed && !f.dismissed && f.ConnectionID == source.connection && f.WorkspaceID == source.workspace && f.SessionID == source.session {
			return f
		}
	}
	return nil
}

func (v *View) dismissSubmissionFailure() {
	for f := v.submissionFailure(); f != nil; f = v.submissionFailure() {
		f.dismissed = true
	}
}

func submissionCheckMessage(connected bool, issue string) string {
	if !connected {
		return "连接恢复后会查询发送结果。"
	}
	if issue != "" {
		return "暂时无法查询发送结果。"
	}
	return ""
}

func actionFailureTitle(method string) string {
	switch method {
	case "session.submit", "turn.steer":
		return "消息未发送"
	case "queue.add", "turn.enqueue":
		return "无法添加待发消息"
	case "queue.edit":
		return "无法保存待发消息"
	case "queue.remove":
		return "无法移除待发消息"
	case "git.checkout":
		return "无法切换分支"
	case "git.status":
		return "无法读取分支"
	case "session.fork":
		return "无法分叉会话"
	case "session.restart":
		return "无法修改已发送的消息"
	}
	return ""
}

func (v *View) submissionWarningDelay(now time.Time, id string, source feedbackSource) time.Duration {
	for _, entry := range v.feedbackState().messages {
		if entry.Kind == app.FeedbackSubmissionUncertain && entry.ID == id && entry.ConnectionID == source.connection &&
			entry.WorkspaceID == source.workspace && entry.SessionID == source.session {
			return 1200*time.Millisecond - now.Sub(entry.tick)
		}
	}
	return 0
}

func (v *View) querySubmission(id string, source feedbackSource) {
	m := v.Model
	in, ok := m.Pending[id]
	if !ok || source != v.feedbackSource() {
		return
	}
	if v.submissionCheckError == nil {
		v.submissionCheckError = make(map[string]string)
	}
	delete(v.submissionCheckError, id)
	if !v.do(app.Job{Method: "operation", Input: m.SubmissionReceiptQuery(id), Apply: func(m *app.Model, b json.RawMessage, err error) {
		if _, ok := m.Pending[id]; !ok {
			return
		}
		if err != nil {
			v.submissionCheckError[id] = err.Error()
			return
		}
		var op domain.Operation
		if err := json.Unmarshal(b, &op); err != nil || op.ID != in.OperationID {
			v.submissionCheckError[id] = "设备返回了不匹配的发送结果"
			return
		}
		delete(v.submissionCheckError, id)
		m.ApplyOperation(op)
	}}) {
		v.submissionCheckError[id] = "设备操作繁忙"
	}
}
func (v *View) renderConversationStatus(c *n.Context, t *n.Theme, s conversationStatus, count, index int) {
	source := v.feedbackSource()
	foldKey := "feedback:" + source.connection + ":" + source.workspace + ":" + source.session
	folded := v.Model.Collapsed[foldKey] && !s.compact
	top := float32(processingPanelExpandedDisclosureInset - processingPanelDisclosureShift)
	bottom := float32(processingPanelExpandedDisclosureInset + processingPanelDisclosureShift)
	if folded {
		top, bottom = processingPanelFoldedDisclosureInset, processingPanelFoldedDisclosureInset
	}
	panel := processingPanelSurface(n.Column(c.Key("status:"+s.id)).FillWidth().
		Padding(top, processingPanelInset, bottom, processingPanelInset).Label("会话状态面板"), t)
	if s.compact {
		panel.Height(44).Padding(7, processingPanelInset)
	}
	panel.Children(func() {
		n.Row(c).FillWidth().Height(processingPanelHeadingRow).Gap(4).AlignItems(n.Center).Children(func() {
			heading := func(disclosure bool) {
				n.Row(c).Gap(processingPanelIconGap).AlignItems(n.Baseline).Children(func() {
					font := n.Font{Family: t.Font, Size: 13, Weight: 500}
					inlineSymbol(c, s.icon, s.title, font, processingPanelIconSize, t.TextMuted)
					n.Text(c, s.title).FontSize(13).FixedLineHeight(processingPanelHeadingLine).FontWeight(500)
					if disclosure {
						angle := float32(0)
						if folded {
							angle = -90
						}
						inlineSymbol(c, disclosureDown, s.title, font, 11, t.TextMuted, angle)
					}
				})
			}
			if s.compact {
				heading(false)
			} else {
				label := "折叠会话状态"
				if folded {
					label = "展开会话状态"
				}
				if processingPanelDisclosure(c).Label(label).Children(func() { heading(true) }).Clicked() {
					v.Model.Collapsed[foldKey] = !folded
				}
			}
			n.Row(c).Grow(1).MinWidth(0)
			v.feedbackStatusPager(c, t, count, index)
			if s.compact {
				for _, a := range s.actions {
					if ghost(c, a.label).Height(processingPanelActionHeight).FontSize(12).Disabled(a.disabled).Clicked() {
						a.run(c)
					}
				}
			}
		})
		if folded || s.compact {
			return
		}
		n.Column(c).FillWidth().Padding(8, 0, 0, processingPanelTextInset).Gap(12).Children(func() {
			if s.body != "" {
				n.Text(c, s.body).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			}
			n.Row(c).FillWidth().Wrap().Gap(messageFooterGap).Children(func() {
				for i, a := range s.actions {
					action := ghost(c, a.label).Height(processingPanelActionHeight).FontSize(12).Disabled(a.disabled)
					if a.accessibilityLabel != "" {
						action.Label(a.accessibilityLabel)
					}
					if i == 0 {
						action.Background(t.Border).PaddingX(10)
					}
					if action.Clicked() && source == v.feedbackSource() {
						a.run(c)
					}
				}
				if s.details != "" {
					if ghost(c, "查看详情").FontSize(12).Height(processingPanelActionHeight).Clicked() {
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
	statuses := v.conversationStatuses(c)
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
		state.focusRecent = state.open
	}
	const clearFade = 120 * time.Millisecond
	if !state.open || c.Preferences().ReduceMotion || c.Now().Sub(state.clearStarted) >= clearFade {
		state.clearing = nil
	}
	popover(c.Key("recent-feedback"), button, &state.open, func(panel n.Element) {
		panel.Width(360).MaxWidth(360).Padding(4).Radius(10).Border(1, t.Border).Background(raised(t)).Children(func() {
			n.Row(c).FillWidth().Height(28).PaddingStart(8).AlignItems(n.Center).Children(func() {
				n.Text(c, "最近消息").FontSize(13).FixedLineHeight(20).Grow(1)
				empty := !slices.ContainsFunc(state.messages, func(message conversationMessage) bool { return !message.waiting })
				if iconButton(c, feedbackTrash, "清空所有消息").Disabled(empty).Clicked() {
					if !c.Preferences().ReduceMotion {
						state.clearing = slices.DeleteFunc(slices.Clone(state.messages), func(message conversationMessage) bool { return message.waiting })
						state.clearStarted = c.Now()
					}
					state.messages = slices.DeleteFunc(state.messages, func(message conversationMessage) bool { return !message.waiting })
					state.replaying, state.focusRecent = "", false
				}
			})
			// Retain only a noninteractive painting copy during the fade. The
			// authoritative history is already empty, so new results stay independent.
			messages := state.messages
			alpha := float32(1)
			if len(state.clearing) > 0 {
				messages = slices.Concat(state.clearing, state.messages)
				alpha = 1 - n.EaseOut(float32(c.Now().Sub(state.clearStarted))/float32(clearFade))
				c.AnimationFrame()
			}
			count := 0
			for _, message := range messages {
				if !message.waiting {
					count++
				}
			}
			scroll := n.Scroll(c.Key("recent-list")).FillWidth().Label("最近消息列表")
			animation := motionAnimation(c, 180*time.Millisecond)
			if c.Preferences().ReduceMotion {
				animation.Duration = 0
			}
			scroll.Height(scroll.AnimateTo("height", float32(min(400, max(36, count*44))), animation)).Children(func() {
				n.Column(c).FillWidth().Gap(0).FocusGroup(n.Vertical).Children(func() {
					empty := true
					for i := len(messages) - 1; i >= 0; i-- {
						message := messages[i]
						if message.waiting {
							continue
						}
						key := feedbackKey(message.Feedback)
						clearing := i < len(state.clearing)
						if clearing {
							key = "cleared:" + key
						}
						source := root.feedbackScopeLabel(message.Feedback)
						title, body, _ := feedbackCopy(message.Feedback)
						summary := strings.Join(strings.Fields(body), " ")
						if summary == "" {
							summary = source
						}
						row := control(c.Key(key), 6).FillWidth().Height(44).Padding(4, 8).Label(title).Description(source).Tooltip(source)
						n.Inert(row, clearing)
						if clearing {
							row.Opacity(alpha)
						}
						if empty && state.focusRecent && !clearing {
							row.AutoFocus()
							state.focusRecent = false
						}
						empty = false
						row.Children(func() {
							n.Column(c).FillWidth().Children(func() {
								n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
									icon := feedbackInfo
									if message.Kind == app.FeedbackSuccess {
										icon = check
									}
									inlineSymbol(c, icon, title, n.Font{Family: t.Font, Size: 13, Weight: 400}, 14, t.TextMuted)
									n.Text(c, title).FontSize(13).FixedLineHeight(18).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
								})
								n.Text(c, summary).FillWidth().PaddingStart(22).FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).SingleLine().Ellipsis("…")
							})
						})
						if row.Clicked() {
							state.replay(key, c.Now())
							button.Focus()
						}
					}
					if empty {
						n.Text(c.Key("empty"), "暂无消息").Padding(8).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Transition(fadeMotion(c, 120*time.Millisecond))
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
	v.renderMessageOverlay(c, t, v.feedbackState(), "")
}

func (v *View) renderMessageOverlay(c *n.Context, t *n.Theme, state *conversationFeedback, windowLabel string) {
	now := c.Now()
	n.Overlay(c.Key("conversation-feedback"), func() {
		active := -1
		for i := len(state.messages) - 1; i >= 0; i-- {
			if !state.messages[i].dismissed && (!state.messages[i].inline || feedbackKey(state.messages[i].Feedback) == state.replaying) {
				if active < 0 {
					active = i
				}
				if feedbackKey(state.messages[i].Feedback) == state.replaying {
					active = i
					break
				}
			}
		}
		if active < 0 {
			state.replaying = ""
			return
		}
		message := &state.messages[active]
		title, body, details := feedbackCopy(message.Feedback)
		if feedbackKey(message.Feedback) != state.replaying {
			state.replaying = ""
		}
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
					sourceLabel := windowLabel
					if sourceLabel == "" {
						sourceLabel = v.feedbackScopeLabel(message.Feedback)
					}
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						font := n.Font{Family: t.Font, Size: 11, Weight: 400}
						inlineSymbol(c, icon, sourceLabel, font, 14, t.TextMuted).Label("消息来源图标")
						n.Text(c, sourceLabel).FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).FillWidth()
					})
					heading := n.Text(c, title).PaddingStart(22).FontSize(13).FixedLineHeight(20).FontWeight(500).FillWidth()
					if title != message.Title {
						heading.Selectable()
					}
				})
				if iconButton(c, closeIcon, "关闭消息").Size(24, 24).Clicked() {
					message.dismissed = true
				}
			})
			canNavigate := windowLabel == "" && message.SessionID != "" && message.SessionID != v.Model.SessionID
			if body != "" || details != "" || canNavigate {
				n.Column(c).FillWidth().PaddingStart(22).Gap(8).Children(func() {
					if body != "" {
						n.Scroll(c.Key("message-body")).FillWidth().MaxHeight(80).Label("消息说明").Children(func() {
							n.Text(c, body).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
						})
					}
					if message.details && details != "" {
						n.Column(c.Key("message-details")).FillWidth().Transition(disclosureMotion(c, 180*time.Millisecond)).Children(func() {
							n.Scroll(c).FillWidth().MaxHeight(120).Children(func() {
								n.Text(c, details).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
							})
						})
					}
					if canNavigate || details != "" {
						n.Row(c).FillWidth().Wrap().Gap(12).AlignItems(n.Center).Children(func() {
							if canNavigate && ghost(c, "查看会话").Label("查看会话").PaddingX(0).FontSize(12).Height(24).Clicked() {
								v.openFeedbackSource(message.Feedback)
							}
							if details != "" {
								label, accessibility := "详细信息", "查看消息详情"
								angle := float32(0)
								if message.details {
									label, accessibility, angle = "收起详情", "收起消息详情", 180
								}
								disclosure := control(c.Key("message-details-action"), 4).Height(24).Padding(0).Label(accessibility)
								disclosure.Children(func() {
									n.Row(c).Gap(4).AlignItems(n.Baseline).Children(func() {
										n.Text(c, label).FontSize(12).FixedLineHeight(20)
										inlineSymbol(c, disclosureDown, label, n.Font{Family: t.Font, Size: 12, Weight: 400}, 12, t.TextMuted, angle)
									})
								})
								if disclosure.Clicked() {
									message.details = !message.details
								}
							}
						})
					}
				})
			}
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
