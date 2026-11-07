package ui

import (
	"slices"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

const historyShatterDuration = 520 * time.Millisecond
const historyCollapseDuration = 200 * time.Millisecond
const historyForkFadeDuration = 220 * time.Millisecond

type historyRestartMotion struct {
	in            domain.HistoryChange
	started       time.Time
	epoch         string
	turns         map[string]bool
	phase         historyExitPhase
	targetSession string
	targetThread  string
	anchorKey     string
	anchorY       *float32
	anchor        historyAnchorPhase
	item          domain.Item
	source        domain.SessionDetail
}

type historyExitPhase uint8

const (
	historyExiting historyExitPhase = iota
	historyDispatched
)

// The target may hydrate before the source row has been pinned. Retain both
// orderings instead of assuming that navigation always supplies source first.
type historyAnchorPhase uint8

const (
	historyAnchorUnbound historyAnchorPhase = iota
	historyAnchorSource
	historyAnchorTarget
	historyAnchorBoth
)

func (m *historyRestartMotion) sourcePinned() bool {
	return m.anchor == historyAnchorSource || m.anchor == historyAnchorBoth
}
func (m *historyRestartMotion) targetAnchored() bool {
	return m.anchor == historyAnchorTarget || m.anchor == historyAnchorBoth
}
func (m *historyRestartMotion) pinSource() {
	if m.targetAnchored() {
		m.anchor = historyAnchorBoth
	} else {
		m.anchor = historyAnchorSource
	}
}
func (m *historyRestartMotion) handOff() {
	if m.sourcePinned() {
		m.anchor = historyAnchorBoth
	} else {
		m.anchor = historyAnchorTarget
	}
}

func newHistoryRestartMotion(in domain.HistoryChange, d domain.SessionDetail, now time.Time, epoch string) *historyRestartMotion {
	m := &historyRestartMotion{in: in, started: now, epoch: epoch, turns: map[string]bool{}}
	if !in.Restart {
		m.source = d
		m.source.Items = slices.Clone(d.Items)
		m.source.Turns = slices.Clone(d.Turns)
		m.source.Requests = slices.Clone(d.Requests)
	}
	target := in.TurnID
	for _, item := range d.Items {
		if item.ID == in.ItemID {
			target = item.TurnID
			m.item = item
			break
		}
	}
	if target == "" && !in.Restart {
		var latest int64 = -1
		for _, turn := range d.Turns {
			if turn.Status == "completed" && turn.Order > latest {
				target, latest = turn.ID, turn.Order
			}
		}
	}
	var order int64
	for _, turn := range d.Turns {
		if turn.ID == target {
			order = turn.Order
			break
		}
	}
	for _, turn := range d.Turns {
		if in.Restart && turn.Order >= order || !in.Restart && turn.Order > order {
			m.turns[turn.ID] = true
		}
	}
	if in.Restart {
		m.targetSession = in.SessionID
		m.anchorKey = in.ItemID
	} else {
		m.anchorKey = "turn-footer:" + target
	}
	return m
}

func (v *View) beginHistoryMotion(in domain.HistoryChange) {
	motion := newHistoryRestartMotion(in, v.Model.Details[in.SessionID], v.frameTime, v.Model.ConnectionEpoch)
	if r, ok := v.historyRowBounds[motion.anchorKey]; ok && r.H > 0 {
		y := r.Y
		motion.anchorY = &y
	}
	v.historyExit = motion
	if v.reduceMotion {
		v.dispatchHistoryRestart()
	} else {
		v.services.Invalidate()
	}
}

// The reset and its replacement submission are separate ordered events. Keep
// only the local revised bubble between them, never resurrect old replies.
func (v *View) historyBridgeRows(rows []transcriptRow) []transcriptRow {
	m := v.historyExit
	if m == nil || !m.in.Restart || m.phase != historyDispatched || v.Model.SessionID != m.in.SessionID {
		return rows
	}
	for _, row := range rows {
		if row.key == m.in.ItemID || row.key == "input:"+m.in.OperationID {
			return rows
		}
	}
	return append(rows, transcriptRow{key: m.in.ItemID, item: m.item, end: -1})
}

func (v *View) anchorHistoryRows(state *n.ListState, rows []transcriptRow) {
	m := v.historyExit
	if m == nil {
		return
	}
	sid := v.Model.SessionID
	target := m.anchorKey
	if m.in.Restart {
		target = "input:" + m.in.OperationID
	}
	for i, row := range rows {
		if sid == m.targetSession && row.key == target {
			if !m.targetAnchored() && m.anchorY != nil {
				state.KeepRow(i+1, *m.anchorY)
			}
			m.handOff()
			return
		}
		if sid == m.in.SessionID && row.key == m.anchorKey && !m.sourcePinned() {
			if m.anchorY != nil {
				state.KeepRow(i+1, *m.anchorY)
			}
			m.pinSource()
		}
	}
}

func (v *View) seedHistoryFork(op domain.HistoryOperation) {
	m := v.persistenceModel()
	if known := m.Details[op.Session.ID]; len(known.Items) > 0 || len(known.Requests) > 0 || known.Plan != nil {
		return
	}
	source := m.Details[op.Source.ID]
	if motion := v.historyExit; motion != nil && motion.in.OperationID == op.ID {
		source = motion.source
	}
	if source.Session.ID != op.Source.ID || source.Session.ThreadID != op.Source.ThreadID || source.Session.Revision != op.Source.Revision {
		return
	}
	detail := domain.SessionDetail{Session: op.Session, Queue: domain.QueueState{SessionID: op.Session.ID}, HasMore: source.HasMore}
	for _, current := range m.Snapshot.Sessions {
		if current.ID == op.Session.ID && current.Revision > op.Session.Revision {
			if current.ThreadID != op.Session.ThreadID {
				return
			}
			detail.Session = current
		}
	}
	kept := map[string]bool{}
	for _, turn := range source.Turns {
		if turn.Order <= op.KeepOrder {
			kept[turn.ID] = true
			detail.Turns = append(detail.Turns, turn)
		}
	}
	for _, item := range source.Items {
		if kept[item.TurnID] {
			detail.Items = append(detail.Items, item)
		}
	}
	for _, request := range source.Requests {
		if kept[request.TurnID] && !request.State.AwaitingResolution() {
			request.SessionID = op.Session.ID
			detail.Requests = append(detail.Requests, request)
		}
	}
	m.Details[op.Session.ID] = detail
}

func (v *View) syncHistoryExit(c *n.Context) {
	motion := v.historyExit
	if motion == nil {
		return
	}
	if motion.phase == historyDispatched {
		if !motion.in.Restart && motion.targetThread != "" {
			current := v.persistenceModel().Details[motion.targetSession].Session.ThreadID
			if current != "" && current != motion.targetThread {
				v.historyExit = nil
				return
			}
		}
		finished := !motion.in.Restart || v.persistenceModel().HistoryEdits[motion.in.SessionID].OperationID == ""
		if finished && (motion.targetAnchored() || v.Model.SessionID != motion.in.SessionID && v.Model.SessionID != motion.targetSession) {
			v.historyExit = nil
		}
		return
	}
	duration := historyShatterDuration + historyCollapseDuration
	if !motion.in.Restart {
		duration = historyForkFadeDuration
	}
	if v.reduceMotion || c.Now().Sub(motion.started) >= duration {
		v.dispatchHistoryRestart()
	} else {
		c.AnimationFrame()
	}
}

func (v *View) dispatchHistoryRestart() {
	motion := v.historyExit
	if motion == nil || motion.phase == historyDispatched {
		return
	}
	in, m := motion.in, v.persistenceModel()
	source := m.Details[in.SessionID].Session
	edit := m.HistoryEdits[in.SessionID]
	epoch := m.ConnectionEpoch
	if state := m.DeviceStates[m.WorkspaceConnection(source.WorkspaceID)]; state != nil {
		epoch = state.ConnectionEpoch
	}
	// No intent has been sent or persisted during the local exit. An external
	// change or lost connection restores the untouched history and edit draft.
	if epoch != motion.epoch || !v.historyReady(source) || source.Revision != in.Revision || in.Restart && edit.ItemID != in.ItemID {
		m.Error = "会话状态已变化，请确认后重新提交修改"
		v.historyExit = nil
		return
	}
	motion.phase = historyDispatched
	if in.Restart {
		edit.OperationID, edit.SubmittedText = in.OperationID, in.Text
		m.HistoryEdits[in.SessionID] = edit
	}
	v.historyCommand(in, false)
}

func (v *View) historySuffixRow(row transcriptRow) bool {
	m := v.historyExit
	if m == nil || m.in.SessionID != v.Model.SessionID || m.in.Restart && row.item.ID == m.in.ItemID {
		return false
	}
	if row.footer != "" {
		return m.turns[row.footer]
	}
	return m.turns[row.item.TurnID]
}

func (v *View) historySuffixPose(c *n.Context, content n.Element, row transcriptRow) {
	if !v.historySuffixRow(row) {
		return
	}
	content.IgnoreInput(true)
	elapsed := c.Now().Sub(v.historyExit.started)
	if !v.historyExit.in.Restart {
		p := min(float32(elapsed)/float32(historyForkFadeDuration), 1)
		content.Opacity(1 - n.EaseOut(max(p, 0)))
		if p < 1 {
			c.AnimationFrame()
		}
		return
	}
	if v.reduceMotion || elapsed >= historyShatterDuration {
		content.Opacity(0)
		return
	}
	seed := uint32(2166136261)
	for _, b := range row.key {
		seed = (seed ^ uint32(b)) * 16777619
	}
	content.Effect(historyShatter{progress: float32(elapsed) / float32(historyShatterDuration), seed: seed})
	c.AnimationFrame()
}

func (v *View) historySuffixReveals(c *n.Context, state *n.ListState, rows []transcriptRow) {
	if v.historyExit == nil || !v.historyExit.in.Restart || v.historyExit.in.SessionID != v.Model.SessionID {
		return
	}
	p := float32(c.Now().Sub(v.historyExit.started)-historyShatterDuration) / float32(historyCollapseDuration)
	if v.reduceMotion {
		p = 1
	}
	if p <= 0 {
		return
	}
	for i, row := range rows {
		if !v.historySuffixRow(row) {
			continue
		}
		if len(state.Reveals) > 0 && state.Reveals[len(state.Reveals)-1].End == i+1 {
			state.Reveals[len(state.Reveals)-1].End = i + 2
		} else {
			state.Reveals = append(state.Reveals, n.ListReveal{First: i + 1, End: i + 2, Progress: 1 - composerDescentEase(min(p, 1))})
		}
	}
}

// Break the actual native rendered content into small pieces on the GPU.
// There is no screenshot readback and no per-frame random noise: every piece
// follows a stable path. The ordinary transcript remains virtualized.
type historyShatter struct {
	progress float32
	seed     uint32
}

func (historyShatter) EffectMargin() n.Point { return n.Point{X: 96, Y: 96} }

func (fx historyShatter) PaintEffect(p *n.Painter, box n.Rect, content *n.Bitmap, inset n.Point) {
	w, h := content.Size()
	sx, sy := float32(w)/(box.W+192), float32(h)/(box.H+192)
	for y, iy := float32(0), uint32(0); y < box.H; y, iy = y+8, iy+1 {
		for x, ix := float32(0), uint32(0); x < box.W; x, ix = x+8, ix+1 {
			hash := (ix*374761393 + iy*668265263) ^ fx.seed
			hash = (hash ^ hash>>13) * 1274126177
			u, v := float32(hash&255)/255, float32((hash>>8)&255)/255
			delay := .08 * u
			k := max(0, min(1, (fx.progress-delay)/(1-delay)))
			alpha := min(float32(1), (1-k)/.22)
			if alpha <= 0 {
				continue
			}
			dst := n.Rect{X: box.X + x, Y: box.Y + y, W: min(8, box.W-x), H: min(8, box.H-y)}
			src := n.Rect{X: inset.X + x*sx, Y: inset.Y + y*sy, W: dst.W * sx, H: dst.H * sy}
			direction := float32(1)
			if u < .5 {
				direction = -1
			}
			pose := n.Rotation((u - .5) * 130 * k).Then(n.Translation(direction*(18+48*v)*k, -24*k+(40+42*u)*k*k))
			p.Transform(pose, dst.X+dst.W/2, dst.Y+dst.H/2, func() {
				p.ImageRegion(content, src, dst, n.ImageOptions{Tint: n.RGBA(1, 1, 1, alpha)})
			})
		}
	}
}
