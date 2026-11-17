package ui

import (
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

type composerSendMatch struct{ input string }

// T3's composer uses this curve explicitly. Its desktop message positioning
// delegates to browser smooth scrolling, whose Chromium curve is separate.
var composerDescentEase = n.CubicBezier(.4, 0, .2, 1)
var sentScrollEase = n.CubicBezier(.4, 0, 0, 1)

const sentScrollDuration = 480 * time.Millisecond
const sentFadeDuration = 300 * time.Millisecond

// A send owns one ephemeral transition. Its input identity comes from the
// accepted operation, so history, remote-client sends and navigation cannot
// accidentally replay the entrance. Nothing here changes runtime acceptance.
type sentMessageMotion struct {
	input, origin, session, epoch, workspace string
	started                                  time.Time
	source, composerSource                   n.Rect
	phase                                    sentMotionPhase
	focused, departed, queued                bool
}

type sentMotionPhase uint8

const (
	sentAwaitingRow sentMotionPhase = iota
	sentAnimating
)

func (v *View) beginSendMotion(in domain.Submission) *sentMessageMotion {
	s := &sentMessageMotion{input: "input:" + in.OperationID, origin: in.SessionID, epoch: v.Model.ConnectionEpoch, workspace: in.WorkspaceID, composerSource: v.composerBounds}
	if v.inputBounds.W > 0 {
		// Align the bubble text with the draft text, accounting for their
		// respective 16/12 px bubble and 9/0 px input insets.
		s.source = v.inputBounds
		s.source.X -= 7
		s.source.Y -= 12
		s.source.W += 14
		s.source.H += 24
	}
	v.sent = s
	return s
}

func (v *View) syncSendMotion() {
	s := v.sent
	if s == nil {
		return
	}
	m := v.Model
	if s.origin == "" && m.SessionID != "" && m.SessionID == s.session {
		s.departed = true
	}
	if (s.departed && m.SessionID == "") || (m.ConnectionEpoch != s.epoch && v.homeSend == nil) || v.devices || (s.origin == "" && m.WorkspaceID != s.workspace) || (m.SessionID != s.origin && m.SessionID != s.session) {
		v.sent = nil
		return
	}
	if s.phase == sentAnimating && v.frameTime.Sub(s.started) >= sentScrollDuration+60*time.Millisecond {
		v.sent = nil
	}
}

func (v *View) anchorSentMessage(c *n.Context, state *n.ListState, rows []transcriptRow) {
	s := v.sent
	if s == nil || s.session != v.Model.SessionID || s.phase == sentAnimating {
		return
	}
	for i, row := range rows {
		if row.key != s.input {
			continue
		}
		s.started, s.phase = c.Now(), sentAnimating
		// Automatic queue dispatch must not pull a reader away from history.
		if !s.queued || state.AtEnd() {
			animation := n.Animation{Duration: sentScrollDuration, Ease: sentScrollEase}
			if s.origin != "" {
				// Existing conversations settle at their measured end. Only a
				// newly created session reserves top reading space on entry.
				state.ScrollToEnd(animation)
			} else {
				state.AnchorRow(i+1, 100, animation)
			}
		}
		c.AnimationFrame()
		break
	}
}

func (v *View) sentMessagePose(c *n.Context, content n.Element, item domain.Item) {
	s := v.sent
	if s == nil || item.ID != s.input || s.phase != sentAnimating || c.Preferences().ReduceMotion || s.source.W > 0 {
		return
	}
	p := min(float32(c.Now().Sub(s.started))/float32(sentFadeDuration), 1)
	k := n.EaseOut(max(0, p))
	content.Opacity(k).Transform(n.Translation(0, 12*(1-k)))
	if p < 1 {
		c.AnimationFrame()
	}
}

// Keep the sent words visible as they leave the editor. Only their
// bubble surface emerges; the message itself does not fade in at the top.
func (v *View) sentMessageBubble(c *n.Context, t *n.Theme, bubble n.Element, item domain.Item) {
	s := v.sent
	if s == nil || item.ID != s.input || s.source.W <= 0 || c.Preferences().ReduceMotion {
		return
	}
	bubble.Background(userBubble(t).Alpha(v.sendProgress(c)))
	bubble.Transition(n.ElementTransition{From: &s.source, Duration: sentScrollDuration, Ease: sentScrollEase, Position: true, Lift: true})
}

func (v *View) sendProgress(c *n.Context) float32 {
	s := v.sent
	if s == nil || s.phase != sentAnimating {
		return 0
	}
	p := min(max(float32(c.Now().Sub(s.started))/float32(sentScrollDuration), 0), 1)
	if p < 1 {
		c.AnimationFrame()
	}
	return sentScrollEase(p)
}

func (v *View) composerSendTransition(c *n.Context, composer n.Element, home bool) {
	composer.DrawOver(func(_ *n.Painter, r n.Rect) { v.composerBounds = r })
	s := v.sent
	if s == nil || s.queued {
		return
	}
	t := n.ElementTransition{Duration: 360 * time.Millisecond, Ease: n.EaseOut, Size: true}
	if s.origin == "" {
		t.Duration, t.Ease = 400*time.Millisecond, composerDescentEase
		if v.homeSend != nil && !home && s.composerSource.W > 0 {
			t.From = &s.composerSource
		}
	}
	if s.origin == "" && (home || s.session == v.Model.SessionID) {
		t.Match = composerSendMatch{s.input}
	}
	composer.Transition(t)
}

// The new session's navigation context may arrive after its first message.
// Keep this short entrance independent of the message's hydration timing.
type sessionEntryMotion struct {
	session, operation, workspace, epoch string
	rowAt, headingAt                     time.Time
	entered                              bool
}

const sessionEntryDuration = 320 * time.Millisecond

func (v *View) syncSessionEntry() {
	s := v.sessionEntry
	if s == nil {
		return
	}
	m := v.Model
	home := m.SessionID == "" && m.WorkspaceID == s.workspace && !s.entered
	if (!home && m.SessionID != s.session) || m.ConnectionEpoch != s.epoch || v.devices || v.reduceMotion {
		v.sessionEntry = nil
		return
	}
	if home && m.Resolved[s.operation] {
		// A confirmation on this home selects the new session. A resolved
		// operation that left it here was rejected; do not replay it later.
		v.sessionEntry = nil
		return
	}
	if m.SessionID == s.session {
		s.entered = true
	}
	for _, session := range m.Snapshot.Sessions {
		if session.ID != s.session {
			continue
		}
		if s.rowAt.IsZero() {
			s.rowAt = v.frameTime
		}
		if s.entered && s.headingAt.IsZero() {
			s.headingAt = v.frameTime
		}
		break
	}
	if !s.rowAt.IsZero() && !s.headingAt.IsZero() && v.frameTime.Sub(s.rowAt) >= sessionEntryDuration && v.frameTime.Sub(s.headingAt) >= sessionEntryDuration {
		v.sessionEntry = nil
	}
}

func (v *View) sessionEntryProgress(c *n.Context, session string, heading bool) float32 {
	s := v.sessionEntry
	if s == nil || s.session != session {
		return 1
	}
	at := s.rowAt
	if heading {
		at = s.headingAt
	}
	if at.IsZero() {
		return 1
	}
	p := min(max(float32(c.Now().Sub(at))/float32(sessionEntryDuration), 0), 1)
	if p < 1 {
		c.AnimationFrame()
	}
	return composerDescentEase(p)
}

// This is only a view of a client-owned pending operation. No provisional session
// is inserted into the catalog, selected for RPCs or sent to the worker.
type homeSendPresentation struct {
	in       domain.Submission
	sentAt   time.Time
	session  string
	state    domain.OperationState
	hydrated bool
	list     *n.ListState
}

func (v *View) homeSendPending() bool {
	h := v.homeSend
	if h == nil {
		return false
	}
	_, ok := v.Model.Pending[h.in.OperationID]
	return ok
}
func (v *View) homeSendKey(fallback string) string {
	if h := v.homeSend; h != nil && h.in.SessionID == "" {
		return "home-send:" + h.in.OperationID
	}
	return fallback
}
func (v *View) syncHomeSend() {
	h := v.homeSend
	if h == nil {
		return
	}
	m := v.Model
	if m.SessionID == "" && h.session != "" && v.selected == h.session {
		v.clearHomeSend()
		return
	}
	// Command receipts and the event stream may arrive in either order.
	for _, op := range m.Snapshot.Operations {
		if op.ID == h.in.OperationID {
			v.acceptHomeSend(m, op)
			break
		}
	}

	if h.session != "" {
		v.acceptHomeSend(m, domain.Operation{ID: h.in.OperationID, SessionID: h.session, State: h.state})
	}
	if v.devices || v.workspaceEditor != nil || m.WorkspaceID != h.in.WorkspaceID || (m.SessionID != "" && m.SessionID != h.session) {
		v.clearHomeSend()
		return
	}
	if m.SessionID == "" && m.Resolved[h.in.OperationID] {
		v.clearHomeSend()
	}
	if h.in.SessionID != "" && !v.homeSendPending() && m.Resolved[h.in.OperationID] && h.state != domain.OperationConfirmed {
		v.clearHomeSend()
	}
}
func (v *View) homeSendDetail(detail domain.SessionDetail) domain.SessionDetail {
	h := v.homeSend
	if h == nil {
		return detail
	}
	if v.Model.SessionID == "" && h.session != "" {
		detail = v.Model.Details[h.session]
	}
	id := "input:" + h.in.OperationID
	for _, item := range detail.Items {
		if item.ID == id {
			h.hydrated = true
			return detail
		}
	}
	// The pending journal already holds the full immutable input and references.
	if !h.hydrated {
		item := domain.Item{ID: id, Kind: "user", Text: h.in.Text, Attachments: h.in.Attachments, Skills: h.in.Skills, ReferencePositions: h.in.ReferencePositions, Status: "dispatching", SentAt: &h.sentAt, Final: true}
		detail.Items = append(append([]domain.Item(nil), detail.Items...), item)
	}
	return detail
}
func (v *View) homeSendRows(rows []transcriptRow) []transcriptRow {
	h := v.homeSend
	if h == nil {
		return rows
	}
	key := "home-send-status:" + h.in.OperationID
	turnID := ""
	for _, row := range rows {
		if row.key == "input:"+h.in.OperationID {
			turnID = row.item.TurnID
			break
		}
	}
	for i, row := range rows {
		if turnID != "" && row.footer == turnID {
			if v.toolArrivals.footerKeys == nil {
				v.toolArrivals.footerKeys = map[string]string{}
			}
			v.toolArrivals.footerKeys[row.footer] = key
			// The receipt already occupies this output slot. A newly hydrated
			// turn must not apply a second entrance to the same visible status.
			rows[i].opacity = 1
			delete(v.toolArrivals.motions, row.key)
			return rows
		}
	}
	if !v.homeSendPending() {
		status := v.Model.Details[v.Model.SessionID].Session.Status
		if status == domain.ExecutionFailed || status == domain.ExecutionInterrupted || status == domain.ExecutionUnknown || status == domain.ExecutionAbandoned || h.state == domain.OperationRejected || h.state == domain.OperationIndeterminate {
			return rows
		}
	}
	label := "正在发送"
	if h.session != "" && h.in.SessionID == "" {
		label = "正在准备会话"
	}
	if h.state == domain.OperationConfirmed || v.Model.Details[v.Model.SessionID].Session.Status == domain.ExecutionRunning {
		label = "进行中"
	}
	if h.state == domain.OperationIndeterminate {
		label = "发送结果待核实"
	}
	if !v.Model.Connected {
		label = "等待重新连接"
	}
	if len(rows) > 0 && rows[len(rows)-1].key == "input:"+h.in.OperationID {
		rows = append(rows, transcriptRow{key: key, preparing: label, end: -1, opacity: 1})
	}
	return rows
}
func (v *View) homeSendStatus(c *n.Context, t *n.Theme, label string) {
	v.replyFooter(c, t, domain.Turn{}, label)
}

func (v *View) acceptHomeSend(m *app.Model, op domain.Operation) {
	h := v.homeSend
	if h == nil || h.in.OperationID != op.ID {
		return
	}
	if h.state.ReceiptObserved() && !op.State.ReceiptObserved() {
		return
	}
	h.state = op.State
	if op.SessionID == "" || (op.State != domain.OperationAccepted && op.State != domain.OperationDispatched && op.State != domain.OperationConfirmed) {
		return
	}
	if h.session == "" {
		h.session = op.SessionID
		v.sessionEntry = &sessionEntryMotion{session: op.SessionID, operation: op.ID, workspace: h.in.WorkspaceID, epoch: m.ConnectionEpoch}
	}
	if m.SessionID == "" && m.WorkspaceID == h.in.WorkspaceID {
		// The receipt can beat the event catalog. Keep the local preview until
		// that owning-device session is visible, so selection cannot fetch a
		// detail that the scoped model would correctly reject as unknown.
		for _, session := range m.Snapshot.Sessions {
			if session.ID == op.SessionID && session.WorkspaceID == h.in.WorkspaceID {
				m.SessionID = op.SessionID
				m.Configs["session:"+op.SessionID] = h.in.Config
				break
			}
		}
	}
	if m.SessionID == op.SessionID {
		delete(v.composerDocuments, "home-send-editor:"+op.ID)
	}
	if sent := v.sent; sent != nil && sent.input == "input:"+op.ID {
		sent.session = op.SessionID
	}
}

func (v *View) clearHomeSend() {
	if h := v.homeSend; h != nil {
		delete(v.composerDocuments, "home-send-editor:"+h.in.OperationID)
	}
	v.homeSend = nil
}
