package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"strings"
	"time"
)

type contextInputPhase uint8

const (
	contextReady contextInputPhase = iota
	contextSending
	contextAccepted
	contextUnknown
	contextChecking
)

type contextInputUI struct {
	phase                                            contextInputPhase
	in                                               domain.ContextActionRequest
	source, epoch, draftKey, draft, error, settledID string
	command                                          bool
	commandSeen                                      bool
	afterOrder                                       int64
	draftDocument                                    *composerDocument
	draftVersion                                     uint64
	started                                          time.Time
}

// Acceptance can consume only the captured command, once. Identical text
// typed later is a new draft even if it happens to have the same characters.
func (s *contextInputUI) consumeCommand(m *app.Model) {
	if !s.command {
		return
	}
	unchanged := s.draftDocument == nil || s.draftDocument.doc.Version() == s.draftVersion
	if unchanged && m.Drafts[s.draftKey] == s.draft && len(m.Attachments[s.draftKey]) == 0 && len(m.DraftSkills[s.draftKey]) == 0 {
		m.Drafts[s.draftKey] = ""
	}
	s.command = false
}

// Lucide Minimize2, the same inward diagonal arrows used by T3 Code.
// License: third_party/lucide/LICENSE.
const compactContextIconPath = "m14 10 7-7M20 10h-6V4m-11 17 7-7M4 14h6v6"

var compactContextIcon = n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="` + compactContextIconPath + `" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`))

func (v *View) contextSession() domain.Session {
	for _, s := range v.Model.Snapshot.Sessions {
		if s.ID == v.Model.SessionID {
			return s
		}
	}
	return domain.Session{}
}
func (v *View) contextInput() *contextInputUI {
	if v.contextInputs == nil {
		v.contextInputs = map[app.WorkKey]*contextInputUI{}
	}
	key := v.Model.WorkKey(v.Model.SessionID, "")
	state := v.contextInputs[key]
	if state == nil {
		state = &contextInputUI{}
		v.contextInputs[key] = state
	}
	return state
}
func (v *View) syncContextInput() {
	s := v.contextInput()
	session := v.contextSession()
	a := session.ContextAction
	if s.source != "" && session.ThreadID != s.source {
		s.phase = contextReady
		s.error = ""
		s.in = domain.ContextActionRequest{}
		s.source = ""
	}
	if s.in.OperationID != "" && (s.epoch != v.Model.ConnectionEpoch || !v.Model.Connected) && (s.phase == contextSending || s.phase == contextChecking) {
		s.phase = contextUnknown
		s.error = "连接已变化，压缩结果待核对"
	}
	if a == nil || a.SourceRef != session.ThreadID {
		return
	}
	if a.State == "unknown" && (s.in.OperationID == "" || s.phase == contextReady && s.in.OperationID != a.OperationID) && a.OperationID != s.settledID {
		s.in = domain.ContextActionRequest{OperationID: a.OperationID, SessionID: session.ID, Revision: a.RequestRevision}
		s.source = session.ThreadID
		s.phase = contextUnknown
	}
	if s.in.OperationID == a.OperationID {
		if a.TurnID != "" {
			s.consumeCommand(v.Model)
		}
		if a.State == "unknown" {
			if s.phase != contextChecking {
				s.phase = contextUnknown
			}
		} else if a.State.Active() {
			s.phase = contextAccepted
		} else {
			s.phase = contextReady
			s.settledID = a.OperationID
		}
	}
}
func (v *View) contextAction() *domain.ContextAction {
	session := v.contextSession()
	if a := session.ContextAction; a != nil && a.SourceRef == session.ThreadID {
		s := v.contextInput()
		if s.in.OperationID == "" || a.OperationID == s.in.OperationID || s.phase == contextReady {
			return a
		}
	}
	s := v.contextInput()
	if s.phase == contextSending || s.phase == contextAccepted {
		return &domain.ContextAction{OperationID: s.in.OperationID, SourceRef: s.source, State: "starting"}
	}
	return nil
}
func (v *View) compactReason() string {
	m := v.Model
	session := v.contextSession()
	s := v.contextInput()
	switch {
	case m.SessionID == "":
		return "开始会话后可用"
	case !m.Connected:
		return "设备已断开"
	case m.WorkerMinor < 30:
		return "更新执行服务后可用"
	case s.phase != contextReady:
		return "正在处理压缩操作"
	case !m.RunReadiness().Capabilities.CanRequestCompaction():
		return "此 Agent 暂不支持手动压缩"
	case session.Archived || m.ArchivedContext() != "":
		return "请先恢复会话或工作区"
	case session.ContextChanged:
		return "请先确认执行分支"
	case session.ExecutionGuard != "" || !session.Status.CanSubmit():
		return "当前回复结束并确认执行状态后可用"
	case m.HasPendingInput():
		return "上一条输入尚未确认"
	case m.ActiveQueueEdit(m.SessionID).ID != "":
		return "请先结束队列编辑"
	}
	d := m.Details[m.SessionID]
	for _, r := range d.Requests {
		if r.State.AwaitingResolution() {
			return "请先处理问题和授权"
		}
	}
	for _, w := range d.Works {
		if w.Active() {
			return "请等待子 Agent 和后台任务结束"
		}
	}
	if session.ThreadID == "" {
		return "会话尚未接纳"
	}
	if v.Controller == nil {
		return "执行服务暂不可用"
	}
	return ""
}
func (v *View) tryContextCommand() bool {
	m := v.Model
	if m.ActiveQueueEdit(m.SessionID).ID != "" {
		return false
	}
	fields := strings.Fields(m.Drafts[m.DraftKey()])
	if len(fields) == 0 || fields[0] != "/compact" {
		return false
	}
	if doc := v.composerDocuments[m.DraftKey()]; doc != nil && doc.doc.Composing() {
		return true
	}
	if len(fields) != 1 {
		s := v.contextInput()
		s.command = true
		s.draftKey, s.draft = m.DraftKey(), m.Drafts[m.DraftKey()]
		s.error = "/compact 需要单独输入，不接受额外正文"
		return true
	}
	v.beginContextCompaction(true)
	return true
}
func (v *View) beginContextCompaction(command bool) {
	v.syncContextInput()
	m := v.Model
	s := v.contextInput()
	if s.phase != contextReady {
		return
	}
	s.command = command
	s.draftKey, s.draft = m.DraftKey(), m.Drafts[m.DraftKey()]
	if command && (len(m.Attachments[m.DraftKey()]) > 0 || len(m.DraftSkills[m.DraftKey()]) > 0) {
		s.error = "/compact 不能携带附件或引用"
		return
	}
	if reason := v.compactReason(); reason != "" {
		s.error = reason
		return
	}
	session := v.contextSession()
	s.in = domain.ContextActionRequest{OperationID: domain.NewID(), SessionID: session.ID, Revision: session.Revision}
	s.source, s.epoch, s.draftKey, s.draft = session.ThreadID, m.ConnectionEpoch, m.DraftKey(), m.Drafts[m.DraftKey()]
	s.draftDocument = v.composerDocuments[s.draftKey]
	if s.draftDocument != nil {
		s.draftVersion = s.draftDocument.doc.Version()
	}
	s.phase, s.error, s.started = contextSending, "", v.frameTime
	s.commandSeen = false
	s.afterOrder = 0
	if items := m.Details[session.ID].Items; len(items) > 0 {
		s.afterOrder = items[len(items)-1].Order
	}
	v.sendContextOperation(s, false)
}
func (v *View) sendContextOperation(s *contextInputUI, query bool) {
	if query {
		if s.phase != contextUnknown || !v.Model.Connected {
			return
		}
		s.phase = contextChecking
		s.epoch = v.Model.ConnectionEpoch
	}
	in := s.in
	method := "session.context.compact"
	if query {
		method = "session.context.operation"
	}
	if !v.do(app.Job{Method: method, Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if s.in != in {
			return
		}
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && remote.Code == "notSent" {
				s.phase = contextReady
				s.settledID = in.OperationID
				s.in = domain.ContextActionRequest{}
			} else {
				s.phase = contextUnknown
			}
			s.error = err.Error()
			return
		}
		var result domain.ContextActionResult
		if json.Unmarshal(b, &result) != nil || result.Operation.ID != in.OperationID || result.Operation.SessionID != in.SessionID || result.Operation.Kind != "compact" || result.Session.ID != in.SessionID {
			s.phase = contextUnknown
			s.error = "压缩回执与请求不一致"
			return
		}
		op := result.Operation
		owned := false
		for i, old := range m.Snapshot.Sessions {
			if old.ID != in.SessionID {
				continue
			}
			if old.WorkspaceID != result.Session.WorkspaceID || old.ThreadID != s.source || result.Session.ThreadID != s.source {
				s.phase = contextReady
				return
			}
			owned = true
			if old.Revision <= result.Session.Revision {
				m.Snapshot.Sessions[i] = result.Session
			}
		}
		if !owned {
			s.phase = contextReady
			return
		}
		if d, ok := m.Details[in.SessionID]; ok && d.Session.Revision <= result.Session.Revision {
			d.Session = result.Session
			m.Details[in.SessionID] = d
		}
		switch op.State {
		case "confirmed":
			s.phase = contextAccepted
			if a := result.Session.ContextAction; a == nil || a.OperationID != in.OperationID || !a.State.Active() {
				s.phase = contextReady
				if a != nil && a.OperationID == in.OperationID && a.State == "unknown" {
					s.phase = contextUnknown
				}
			}
			s.error = ""
			s.settledID = op.ID
			s.consumeCommand(m)
		case "rejected", "abandoned":
			s.phase = contextReady
			s.error = op.Error
			s.settledID = op.ID
			s.in = domain.ContextActionRequest{}
		default:
			s.phase = contextUnknown
			s.error = "压缩结果尚未确认"
		}
	}}) {
		if query {
			s.phase = contextUnknown
		} else {
			s.phase = contextReady
			s.in = domain.ContextActionRequest{}
		}
		s.error = "设备繁忙，请重试"
	}
}
func (v *View) contextCommandNotice(c *n.Context, t *n.Theme) {
	s := v.contextInput()
	if s.command && s.phase == contextReady && s.draftKey == v.Model.DraftKey() && s.draft != v.Model.Drafts[s.draftKey] {
		s.error = ""
	}
	frame := n.Column(c.Key("context-command-notice")).FillWidth().Clip().Margin(0, 0, -4, 0)
	animatedContentHeight(c, frame, s.command && s.error != "", 180*time.Millisecond, func() float32 {
		n.Text(c, s.error).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Label("压缩命令提示").Transition(fadeMotion(c, 180*time.Millisecond))
		return 0
	})
}
func (v *View) contextRows(rows []transcriptRow) []transcriptRow {
	if v.contextRowKeys == nil {
		v.contextRowKeys = map[app.WorkKey]string{}
	}
	for i, row := range rows {
		if key := v.contextRowKeys[v.Model.WorkKey(v.Model.SessionID, row.item.ID)]; key != "" {
			rows[i].key = key
		}
	}
	a := v.contextAction()
	if a == nil {
		return rows
	}
	s := v.contextInput()
	history := v.Model.Details[v.Model.SessionID].History
	atLatest := history == nil || !history.Newer
	local := !s.started.IsZero() && (s.in.OperationID == a.OperationID || s.settledID == a.OperationID)
	key := "context-compaction:" + a.OperationID
	progress := float32(1)
	if local && !v.reduceMotion {
		progress = min(float32(1), max(float32(0), float32(v.frameTime.Sub(s.started))/float32(200*time.Millisecond)))
		if progress < 1 {
			v.toolsAnimating = true
		}
	}
	commandID := "context-command:" + a.OperationID
	commandAt := -1
	for i, row := range rows {
		if row.item.ID == commandID {
			commandAt = i
			s.commandSeen = true
			if local {
				rows[i].opacity = progress
			}
			break
		}
	}
	// Keep the same command row while its durable journal event catches up.
	if commandAt < 0 && atLatest && local && !s.commandSeen {
		at := len(rows)
		for i, row := range rows {
			if (a.TurnID != "" && row.item.TurnID == a.TurnID) || (s.afterOrder > 0 && row.item.Order > s.afterOrder) {
				at = i
				break
			}
		}
		command := transcriptRow{key: commandID, item: domain.Item{ID: commandID, TurnID: a.TurnID, Kind: "command", Presentation: "compact", Name: "压缩上下文", Status: "accepted", Final: true}, end: -1, opacity: progress}
		rows = slices.Insert(rows, at, command)
		commandAt = at
	}
	for i, row := range rows {
		if row.item.Presentation == "compaction" && row.item.TurnID == a.TurnID && a.TurnID != "" {
			rows[i].key = key
			if !a.State.Active() {
				rows[i].item.Status = string(a.State)
				rows[i].item.Final = true
				if !row.item.Final || row.item.Status != string(a.State) || row.item.Name == "" {
					rows[i].item.Name = contextActionLabel(a.State)
				}
			}
			v.contextRowKeys[v.Model.WorkKey(v.Model.SessionID, row.item.ID)] = key
			if local {
				rows[i].opacity = progress
			}
			return rows
		}
	}
	// A terminal action can outlive its history in the current page. Only
	// show its fallback beside its own command while the source item catches up.
	if !atLatest || a.State == "unknown" || a.State == "abandoned" || (!a.State.Active() && commandAt < 0) {
		return rows
	}
	item := domain.Item{ID: key, TurnID: a.TurnID, Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文", Status: "running"}
	if !a.State.Active() {
		item.Status = string(a.State)
		item.Final = true
		item.Name = contextActionLabel(a.State)
		if item.Name == "" {
			return rows
		}
	}
	at := len(rows)
	if commandAt >= 0 {
		at = commandAt + 1
	}
	return slices.Insert(rows, at, transcriptRow{key: key, item: item, end: -1, opacity: progress})
}
func contextActionLabel(state domain.ExecutionState) string {
	switch state {
	case "completed":
		return "压缩命令已结束"
	case "failed":
		return "上下文压缩失败"
	case "interrupted":
		return "上下文压缩已停止"
	case "unknown":
		return "压缩状态待核对"
	}
	return ""
}

// All compaction surfaces use the same current activity as tool summaries,
// the reply footer and the offscreen latest indicator.
func (v *View) currentCompaction() string {
	if !v.Model.Connected || v.sessionStopInFlight(v.Model.SessionID) {
		return ""
	}
	action := v.contextAction()
	if turn, ok := v.activeConversationTurn(); ok {
		activity := v.runningActivity(turn)
		if activity.waiting {
			return ""
		}
		if activity.compaction != "" {
			return activity.compaction
		}
		if activity.item != "" || turn.Presentation != "compaction" || action == nil || action.TurnID != turn.ID {
			return ""
		}
	}
	if action != nil && action.State.Active() {
		return "context-compaction:" + action.OperationID
	}
	return ""
}

func (v *View) contextCompactionItem(c *n.Context, t *n.Theme, item domain.Item) {
	live := !item.Final && item.WorkID == "" && v.currentCompaction() == item.ID
	label := item.Name
	if label == "" {
		label = "正在压缩上下文"
	}
	if !item.Final && !live {
		label = "压缩状态待确认"
		if v.stoppingTurn(item.TurnID) {
			label = contextActionLabel(domain.ExecutionInterrupted)
		}
	}
	row := n.Row(c.Key("compaction-status")).FillWidth().Gap(6).AlignItems(n.Baseline)
	row.Children(func() {
		font := n.Font{Family: t.Font, Size: transcriptCaptionSize, Weight: 400}
		if live {
			transcriptStatusDot(c, label, font, t.Success, true)
		} else if item.Status == "completed" {
			transcriptSymbol(c, transcriptCheckIcon, label, font, t.TextMuted)
		} else {
			transcriptSymbol(c, transcriptCompactIcon, label, font, t.TextMuted)
		}
		metadata := n.Row(c.Key("metadata")).Gap(6).AlignItems(n.Baseline)
		if live {
			shimmer(c, metadata, t.TextMuted, t.Text)
		}
		metadata.Children(func() {
			n.Text(c.Key("label"), label).FontSize(font.Size).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)})
			if live && item.TurnID != "" {
				for _, turn := range v.Model.Details[v.Model.SessionID].Turns {
					if turn.ID == item.TurnID && turn.Status.Active() {
						runningElapsed(c.Key("elapsed"), v.runningElapsedStart(turn), t.TextMuted, false)
						break
					}
				}
			}
		})
		if item.Status == "failed" || item.Status == "interrupted" {
			if control(c.Key("retry-compact")).Height(26).PaddingX(4).Disabled(v.compactReason() != "").Label("重试压缩").Children(func() {
				n.Text(c, "重试").FontSize(transcriptCaptionSize).TextColor(t.TextMuted)
			}).Clicked() {
				v.beginContextCompaction(false)
			}
		}
	})
}
