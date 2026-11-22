package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

const mobileCompactionMinor = 30

type mobileCompactionPhase uint8

const (
	mobileCompactionReady mobileCompactionPhase = iota
	mobileCompactionSending
	mobileCompactionAccepted
	mobileCompactionUnknown
	mobileCompactionChecking
)

type mobileCompaction struct {
	phase                             mobileCompactionPhase
	input                             domain.ContextActionRequest
	thread, epoch, message, settledID string
	started                           time.Time
	afterOrder                        int64
	commandSeen                       bool
	draftKey, draft                   string
	draftDoc                          *richtext.Document
	draftVersion                      uint64
	rowKeys                           map[string]string
}

func (a *mobileApp) compaction(s domain.Session) *mobileCompaction {
	if a.compactions == nil {
		a.compactions = make(map[app.WorkKey]*mobileCompaction)
	}
	key := a.model.WorkKey(s.ID, "")
	state := a.compactions[key]
	if state == nil || state.thread != s.ThreadID {
		state = &mobileCompaction{thread: s.ThreadID}
		a.compactions[key] = state
	}
	m, action := a.model, s.ContextAction
	if (state.phase == mobileCompactionSending || state.phase == mobileCompactionChecking) && (state.epoch != m.ConnectionEpoch || !m.Connected) {
		state.phase, state.message = mobileCompactionUnknown, "连接已变化，压缩结果待核对"
	}
	if action == nil || action.SourceRef != s.ThreadID || action.OperationID == "" {
		return state
	}
	if action.State == domain.ExecutionUnknown && action.OperationID != state.settledID && state.phase == mobileCompactionReady {
		state.input = domain.ContextActionRequest{SessionID: s.ID, OperationID: action.OperationID, Revision: action.RequestRevision}
		state.phase = mobileCompactionUnknown
	}
	if state.input.OperationID != action.OperationID {
		return state
	}
	if action.TurnID != "" {
		if state.consume(m) {
			a.save()
		}
	}
	switch {
	case action.State == domain.ExecutionUnknown:
		if state.phase != mobileCompactionChecking {
			state.phase = mobileCompactionUnknown
		}
	case action.State.Active():
		if action.TurnID != "" {
			state.phase = mobileCompactionAccepted
		}
	default:
		state.phase, state.settledID = mobileCompactionReady, action.OperationID
		state.message = ""
	}
	return state
}

func (s *mobileCompaction) consume(m *app.Model) bool {
	if s.draftDoc == nil {
		return false
	}
	cleared := false
	if s.draftDoc.Version() == s.draftVersion && m.Drafts[s.draftKey] == s.draft &&
		len(m.Attachments[s.draftKey]) == 0 && len(m.DraftSkills[s.draftKey]) == 0 {
		m.Drafts[s.draftKey] = ""
		cleared = true
	}
	s.draftDoc = nil
	return cleared
}

func (s *mobileCompaction) action(session domain.Session) *domain.ContextAction {
	if existing := session.ContextAction; existing != nil && existing.SourceRef == session.ThreadID &&
		(s.input.OperationID == "" || existing.OperationID == s.input.OperationID || s.phase == mobileCompactionReady) {
		return existing
	}
	if s.phase == mobileCompactionSending || s.phase == mobileCompactionAccepted {
		return &domain.ContextAction{OperationID: s.input.OperationID, SourceRef: s.thread, State: domain.ExecutionStarting}
	}
	return nil
}

func (a *mobileApp) compactionReason(session domain.Session, s *mobileCompaction) string {
	m := a.model
	switch {
	case session.ID == "":
		return "开始会话后可用"
	case !m.Connected:
		return "设备已断开"
	case m.WorkerMinor < mobileCompactionMinor:
		return "更新执行服务后可用"
	case s.phase != mobileCompactionReady:
		return "正在处理压缩操作"
	case !m.SessionReadiness(session).Capabilities.Compact:
		return "此 Agent 暂不支持手动压缩"
	case session.Archived || session.ReadOnlyHistory() || a.workspace(session.WorkspaceID).Archived:
		return "请先恢复会话或工作区"
	case session.ContextChanged:
		return "请先确认执行分支"
	case session.ExecutionGuard != "" || !session.Status.CanSubmit() || session.ContextAction != nil && session.ContextAction.State.Active():
		return "当前回复结束并确认执行状态后可用"
	case m.HasPendingInputForSession(session.ID):
		return "上一条输入尚未确认"
	case m.ActiveQueueEdit(session.ID).ID != "" || m.HistoryEdits[session.ID].ItemID != "":
		return "请先结束消息编辑"
	}
	detail := m.Details[session.ID]
	for _, requests := range [][]domain.PendingRequest{detail.Requests, m.Snapshot.PendingRequests} {
		if slices.ContainsFunc(requests, func(r domain.PendingRequest) bool {
			return r.SessionID == session.ID && r.State.AwaitingResolution()
		}) {
			return "请先处理问题和授权"
		}
	}
	if slices.ContainsFunc(detail.Works, func(work domain.WorkActivity) bool { return work.Active() }) {
		return "请等待子 Agent 和后台任务结束"
	}
	if session.ThreadID == "" {
		return "会话尚未接纳"
	}
	if a.controller == nil && !a.preview {
		return "执行服务暂不可用"
	}
	return ""
}

func mobileCompactCommand(text string) bool {
	fields := strings.Fields(text)
	return len(fields) != 0 && fields[0] == "/compact"
}

func (a *mobileApp) tryCompactionCommand(editor *mobileDraftEditor) bool {
	m := a.model
	k := m.DraftKey()
	session := a.session()
	if editor != nil {
		k, session = editor.target.key, a.sessionByID(editor.target.session)
	}
	if !mobileCompactCommand(m.Drafts[k]) {
		return false
	}
	if editor != nil && (editor.pending() || editor.target.history || editor.target.queueEdit != "") {
		return true
	}
	if len(strings.Fields(m.Drafts[k])) != 1 {
		m.Error = "/compact 需要单独输入，不接受额外正文"
		return true
	}
	if len(m.Attachments[k]) != 0 || len(m.DraftSkills[k]) != 0 {
		m.Error = "/compact 不能携带附件或引用"
		return true
	}
	a.beginCompaction(session, editor)
	return true
}

func (a *mobileApp) beginCompaction(session domain.Session, editor *mobileDraftEditor) {
	if session.ID != a.model.SessionID && (editor == nil || editor.target != a.parallelDraftTarget()) {
		return
	}
	state := a.compaction(session)
	if reason := a.compactionReason(session, state); reason != "" {
		a.model.Error = reason
		a.refresh()
		return
	}
	m := a.model
	m.Error = ""
	state.input = domain.ContextActionRequest{OperationID: domain.NewID(), SessionID: session.ID, Revision: session.Revision}
	state.phase, state.started, state.commandSeen = mobileCompactionSending, time.Now(), false
	state.afterOrder = 0
	if items := m.Details[session.ID].Items; len(items) != 0 {
		state.afterOrder = items[len(items)-1].Order
	}
	state.message = ""
	state.draftDoc = nil
	if editor != nil {
		state.draftKey, state.draft = editor.target.key, m.Drafts[editor.target.key]
		state.draftDoc, state.draftVersion = editor.Doc, editor.Doc.Version()
	}
	a.sendCompaction(session, state, false)
}

func (a *mobileApp) queryCompaction(session domain.Session, state *mobileCompaction) {
	if state.phase == mobileCompactionUnknown && state.input.OperationID != "" && a.model.Connected {
		state.phase = mobileCompactionChecking
		a.sendCompaction(session, state, true)
	}
}

func (a *mobileApp) sendCompaction(session domain.Session, state *mobileCompaction, query bool) {
	m, controller := a.model, a.controller
	in := state.input
	epoch := m.ConnectionEpoch
	key := m.WorkKey(session.ID, "")
	state.epoch = epoch
	method := "session.context.compact"
	if query {
		method = "session.context.operation"
	}
	done := func(raw json.RawMessage, err error) {
		current := a.sessionByID(session.ID)
		if m != a.model || controller != a.controller || epoch != m.ConnectionEpoch || state != a.compactions[key] ||
			state.input != in || current.ThreadID != state.thread || current.WorkspaceID != session.WorkspaceID ||
			m.Snapshot.DeviceID != key.DeviceID {
			return
		}
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && (!query && remote.Code == "notSent" || query && remote.Code == "notFound") {
				state.phase, state.settledID = mobileCompactionReady, in.OperationID
				state.input, state.draftDoc = domain.ContextActionRequest{}, nil
			} else {
				state.phase = mobileCompactionUnknown
			}
			state.message = err.Error()
			a.refresh()
			return
		}
		var receipt domain.ContextActionResult
		if json.Unmarshal(raw, &receipt) != nil || receipt.Operation.ID != in.OperationID ||
			receipt.Operation.SessionID != in.SessionID || receipt.Operation.Kind != "compact" ||
			receipt.Session.ID != in.SessionID || receipt.Session.ThreadID != current.ThreadID ||
			receipt.Session.WorkspaceID != current.WorkspaceID || receipt.Session.Context.DeviceID != key.DeviceID {
			state.phase, state.message = mobileCompactionUnknown, "压缩回执与当前操作不一致"
			a.refresh()
			return
		}
		if receipt.Session.Revision >= current.Revision {
			for i := range m.Snapshot.Sessions {
				if m.Snapshot.Sessions[i].ID == in.SessionID {
					m.Snapshot.Sessions[i] = receipt.Session
					break
				}
			}
			if detail, ok := m.Details[in.SessionID]; ok && detail.Session.Revision <= receipt.Session.Revision {
				detail.Session = receipt.Session
				m.Details[in.SessionID] = detail
			}
		}
		switch receipt.Operation.State {
		case domain.OperationConfirmed:
			state.message, state.settledID = "", in.OperationID
			state.consume(m)
			state.phase = mobileCompactionReady
			if action := a.sessionByID(session.ID).ContextAction; action != nil && action.OperationID == in.OperationID {
				if action.State.Active() {
					state.phase = mobileCompactionAccepted
				} else if action.State == domain.ExecutionUnknown {
					state.phase = mobileCompactionUnknown
				}
			}
		case domain.OperationRejected, domain.OperationAbandoned:
			state.phase, state.settledID, state.message = mobileCompactionReady, in.OperationID, receipt.Operation.Error
			state.input, state.draftDoc = domain.ContextActionRequest{}, nil
		default:
			state.phase, state.message = mobileCompactionUnknown, "压缩结果尚未确认"
		}
		a.save()
		a.refresh()
	}
	if a.preview {
		a.previewContextOperation(method, in, done)
	} else if controller == nil || !controller.Do(app.Job{Method: method, Input: in, ConnectionEpoch: epoch,
		Apply: func(_ *app.Model, raw json.RawMessage, err error) { done(raw, err) },
	}) {
		state.message = "设备繁忙，请重试"
		if query {
			state.phase = mobileCompactionUnknown
		} else {
			state.phase, state.input, state.draftDoc = mobileCompactionReady, domain.ContextActionRequest{}, nil
		}
	}
	a.refresh()
}

func (a *mobileApp) compactionControl(c *ui.Context, session domain.Session, s *mobileCompaction) {
	p := palette(c)
	info := session.ContextUsage
	display := "用量未知"
	var fraction float32
	if info != nil && info.UsedTokens != nil {
		display = fmt.Sprintf("%d 令牌", *info.UsedTokens)
		if info.LimitTokens != nil && *info.LimitTokens > 0 {
			display = fmt.Sprintf("%d / %d 令牌", *info.UsedTokens, *info.LimitTokens)
			fraction = float32(min(1, max(0, float64(*info.UsedTokens)/float64(*info.LimitTokens))))
		}
	}
	busy := a.model.Connected && (s.phase == mobileCompactionSending || s.phase == mobileCompactionAccepted ||
		a.currentLiveCompaction(session, a.model.Details[session.ID], mobileclient.ConversationItems(a.model.Details[session.ID])) != "")
	button := mobileButton(c, "上下文用量", nil).Size(mobileTapSize(c), mobileTapSize(c)).AlignItems(ui.Center).Justify(ui.Center).Value(display).KeepFocus()
	value := button.AnimateTo("usage", fraction, ui.Animation{Duration: 200 * time.Millisecond})
	reduce := c.Preferences().ReduceMotion
	button.Children(func() {
		ui.Box(c).Size(18, 18).PassThrough().Draw(func(painter *ui.Painter, r ui.Rect) {
			var ring ui.Path
			ring.Circle(r.X+r.W/2, r.Y+r.H/2, 6.5)
			painter.StrokePath(&ring, 2, p.selected)
			if busy {
				start := float32(0)
				if !reduce {
					start = float32(painter.Now().UnixMilli()%900) / 900
					painter.After(time.Second / 60)
				}
				painter.StrokePath(ring.Trim(start, start+.28), 2, p.secondary)
				if start+.28 > 1 {
					painter.StrokePath(ring.Trim(0, start+.28-1), 2, p.secondary)
				}
			} else if value > 0 {
				painter.StrokePath(ring.Trim(0, value), 2, p.secondary)
			}
		})
	}).PopoverMenuButton(func(menu *ui.Menu) {
		menu.Item(display).Disabled(true)
		menu.Separator()
		if s.phase == mobileCompactionUnknown {
			if menu.Item("核对压缩结果").Disabled(!a.model.Connected).Chosen() {
				a.queryCompaction(session, s)
			}
			return
		}
		reason := a.compactionReason(session, s)
		if menu.Item("压缩上下文").Disabled(reason != "").Chosen() {
			a.beginCompaction(session, nil)
		}
		if reason != "" {
			menu.Item(reason).Disabled(true)
		}
	}, ui.PopoverMenuButtonOptions{At: ui.AnchorTopEnd, Self: ui.AnchorBottomEnd})
}

func (a *mobileApp) currentLiveCompaction(session domain.Session, detail domain.SessionDetail, items []domain.Item) string {
	if !a.model.Connected || session.Archived {
		return ""
	}
	for _, requests := range [][]domain.PendingRequest{detail.Requests, a.model.Snapshot.PendingRequests} {
		if slices.ContainsFunc(requests, func(r domain.PendingRequest) bool {
			return r.SessionID == session.ID && r.WorkID == "" && r.State.AwaitingResolution()
		}) {
			return ""
		}
	}
	return mobileclient.LiveCompaction(items, detail.Turns)
}

func (s *mobileCompaction) project(items []domain.Item, session domain.Session, latest bool) []domain.Item {
	action := s.action(session)
	if action == nil {
		return items
	}
	for _, item := range items {
		if item.ID == "context-command:"+action.OperationID && action.OperationID == s.input.OperationID {
			s.commandSeen = true
		}
		if action.TurnID != "" && item.TurnID == action.TurnID && item.Presentation == "compaction" {
			if s.rowKeys == nil {
				s.rowKeys = make(map[string]string)
			}
			s.rowKeys[item.ID] = "context-compaction:" + action.OperationID
		}
	}
	var localAfter *int64
	if !s.started.IsZero() && s.input.OperationID == action.OperationID && !s.commandSeen &&
		action.State != domain.ExecutionUnknown {
		localAfter = &s.afterOrder
	}
	return mobileclient.CompactionItems(items, action, localAfter, latest)
}

func (s *mobileCompaction) rowKey(id string) string {
	if key := s.rowKeys[id]; key != "" {
		return key
	}
	return id
}

func (a *mobileApp) compactionRow(c *ui.Context, session domain.Session, item domain.Item,
	live bool, turn domain.Turn, interactive bool, s *mobileCompaction) {
	p := palette(c)
	title := item.Name
	if !item.Final && !live {
		title = "压缩状态待确认"
	} else if title == "" {
		title = mobileclient.ContextCompactionLabel(domain.ExecutionState(item.Status))
		if title == "" {
			title = "上下文压缩"
		}
	}
	if live {
		mobileReplyProgress(c, title, turn.StartedAt, nil, true)
		return
	}
	glyph := compactSymbol
	if item.Status == string(domain.ExecutionCompleted) {
		glyph = checkSymbol
	}
	font := mobileStatusFont(c, captionTextSize)
	ui.Row(c).MinHeight(transcriptMetadataHeight).Gap(6).AlignItems(ui.Baseline).Children(func() {
		transcriptSymbol(c, glyph, title, font, footerIconSize, p.secondary)
		ui.Text(c, title).Font(font.Family).FontSize(font.Size).FixedLineHeight(mobileStatusLineHeight(font)).TextColor(p.secondary).MaxLines(2).MinWidth(0).Shrink(1).
			ContentTransition(ui.ContentTransition{Animation: &ui.Animation{Duration: 180 * time.Millisecond}})
		if interactive && (item.Status == string(domain.ExecutionFailed) || item.Status == string(domain.ExecutionInterrupted)) {
			action(c.Key("retry"), "重试", func() { a.beginCompaction(session, nil) }).Label("重试压缩").
				KeepFocus().PaddingX(4).Disabled(a.compactionReason(session, s) != "")
		}
	})
}

func compactCommandChip(c *ui.Context, text string) {
	p := palette(c)
	ui.Row(c).AlignSelf(ui.End).AlignItems(ui.Center).Gap(6).Padding(4, 8).Radius(8).
		Border(1, p.line).Background(p.inset).Children(func() {
		alignedSymbol(c, compactSymbol, text, ui.Font{Size: captionTextSize * c.Preferences().TextScale}, 14, p.secondary)
		label(c, text, controlTextSize, p.ink).Selectable()
	})
}
