package ui

import (
	"slices"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/conversationui"
	"shioji/internal/domain"
)

const historyShatterDuration = conversationui.HistoryShatterDuration
const historyCollapseDuration = conversationui.HistoryCollapseDuration
const historyForkFadeDuration = conversationui.HistoryForkFadeDuration

type historyRestartMotion struct {
	in              domain.HistoryChange
	started         time.Time
	runningShown    time.Time
	collapseStarted time.Time
	epoch           string
	turns           map[string]bool
	phase           historyExitPhase
	targetSession   string
	targetThread    string
	anchorKey       string
	anchorY         *float32
	anchor          historyAnchorPhase
	item            domain.Item
	source          domain.SessionDetail
}

type historyExitPhase uint8

const (
	historyExiting historyExitPhase = iota
	// Keep the running footer in one laid-out frame before reclaiming space.
	historyShowingRunning
	historyCollapsing
	historyDispatched
	// The RPC succeeded; keep its preview until the watch supplies the input.
	historyConfirmed
)

// The target may hydrate before the source row has been pinned. Retain both
// orderings instead of assuming that navigation always supplies source first.
type historyAnchorPhase uint8

const (
	historyAnchorUnbound historyAnchorPhase = iota
	historyAnchorSource
	historyAnchorSourceBounded
	historyAnchorTarget
	historyAnchorBoth
)

func (m *historyRestartMotion) sourcePinned() bool {
	return m.anchor == historyAnchorSource || m.anchor == historyAnchorSourceBounded || m.anchor == historyAnchorBoth
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
		// Presence records the original turns; the value marks the exiting suffix.
		m.turns[turn.ID] = in.Restart && turn.Order >= order || !in.Restart && turn.Order > order
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
	if m == nil || !m.in.Restart || m.phase < historyDispatched || v.Model.SessionID != m.in.SessionID {
		return rows
	}
	for _, row := range rows {
		if row.key == m.in.ItemID || row.key == "input:"+m.in.OperationID {
			return rows
		}
	}
	return append(rows, transcriptRow{key: m.in.ItemID, item: m.item, end: -1, opacity: 1})
}

// The local exit precedes worker acceptance and turn creation. Show the same
// running footer through that gap without manufacturing an authoritative turn.
func (v *View) historyRunningRows(rows []transcriptRow) []transcriptRow {
	m := v.historyExit
	if m == nil || !m.in.Restart || v.Model.SessionID != m.in.SessionID || m.phase == historyExiting {
		return rows
	}
	key := "history-running:" + m.in.OperationID
	for _, row := range rows {
		if row.footer != "" && row.replyBefore == nil {
			if _, original := m.turns[row.footer]; !original {
				v.retainHistoryRunningFooter(m, row.footer)
				return rows
			}
		}
	}
	for i, row := range rows {
		if row.key == m.in.ItemID || row.key == "input:"+m.in.OperationID {
			pending := transcriptRow{key: key, restarting: true, end: -1}
			return slices.Insert(rows, i+1, pending)
		}
	}
	return rows
}

// syncHistoryExit runs before transcript projection. Transfer identity there
// too, before it retires the local motion and ordinary arrival tracking resumes.
func (v *View) retainHistoryRunningFooter(m *historyRestartMotion, turn string) {
	arrivals := v.toolArrivals
	if arrivals == nil || arrivals.session != m.in.SessionID {
		return
	}
	if arrivals.footerKeys == nil {
		arrivals.footerKeys = map[string]string{}
	}
	arrivals.footerKeys[turn] = "history-running:" + m.in.OperationID
	arrivals.seen["turn-footer:"+turn] = true
	delete(arrivals.motions, "turn-footer:"+turn)
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
				if m.in.Restart {
					// This only replaces the local row's identity. Its position is
					// already settled; replaying a send glide from estimated prefix
					// heights makes it move away and back during confirmation.
					// Keep the sending anchor's eventual end-following behavior.
					state.AnchorRow(i+1, *m.anchorY)
				} else {
					state.KeepRow(i+1, *m.anchorY)
				}
			}
			m.handOff()
			return
		}
		if sid == m.in.SessionID && row.key == m.anchorKey {
			if !m.sourcePinned() {
				if m.anchorY != nil {
					if m.in.Restart && v.reduceMotion {
						state.KeepRowWithinContent(i+1, *m.anchorY)
					} else {
						state.KeepRow(i+1, *m.anchorY)
					}
				}
				m.pinSource()
			} else if m.in.Restart && !m.targetAnchored() && m.anchorY != nil {
				// Hand the replacement the position after end clamping, rather
				// than restoring the blank space removed with the old suffix.
				if bounds, ok := v.historyRowBounds[row.key]; ok {
					*m.anchorY = bounds.Y
				}
			}
			if m.in.Restart && m.phase >= historyCollapsing && m.anchor == historyAnchorSource {
				if m.anchorY != nil {
					state.KeepRowWithinContent(i+1, *m.anchorY)
				}
				m.anchor = historyAnchorSourceBounded
			}
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
	// The receipt proves the source revision, but a paged window can omit
	// items from retained turns on either side of the window.
	if !source.HasLoadedHistory() || source.HasMore || source.History != nil && (source.History.Older || source.History.Newer) {
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
	// A worker-confirmed fork can display its verified prefix immediately.
	detail.HistoryLoaded = len(detail.Items) > 0
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
	if motion.phase >= historyDispatched {
		if !motion.in.Restart && motion.targetThread != "" {
			current := v.persistenceModel().Details[motion.targetSession].Session.ThreadID
			if current != "" && current != motion.targetThread {
				v.historyExit = nil
				return
			}
		}
		detail := v.Model.Details[motion.in.SessionID]
		if motion.in.Restart {
			for _, op := range v.persistenceModel().Snapshot.Operations {
				if op.ID == motion.in.OperationID && (op.State == "indeterminate" || op.State == "rejected" || op.State == "abandoned") {
					v.historyExit = nil
					return
				}
			}
			for _, item := range detail.Items {
				if item.ID == "input:"+motion.in.OperationID && (item.Status == "unknown" || item.Status == "failed" || detail.Session.Status == "unknown" || detail.Session.Status == "failed" || detail.Session.Status == "abandoned") {
					v.historyExit = nil
					return
				}
			}
		}
		finished := !motion.in.Restart || v.persistenceModel().HistoryEdits[motion.in.SessionID].OperationID == ""
		if finished && motion.in.Restart && v.Model.SessionID == motion.in.SessionID {
			// Submission retires the edit before the harness supplies a turn.
			// The first real footer can belong to an assistant item before the
			// accepted user item receives its turn ID from the Submit receipt.
			finished = false
			for _, turn := range detail.Turns {
				if _, original := motion.turns[turn.ID]; original {
					continue
				}
				for _, item := range detail.Items {
					if item.TurnID == turn.ID && !domain.HistorySpacer(item) {
						v.retainHistoryRunningFooter(motion, turn.ID)
						finished = true
						break
					}
				}
				if finished {
					break
				}
			}
		}
		if finished && (motion.targetAnchored() || v.Model.SessionID != motion.in.SessionID && v.Model.SessionID != motion.targetSession) {
			v.historyExit = nil
		}
		return
	}
	if v.reduceMotion {
		v.dispatchHistoryRestart()
		return
	}
	if !motion.in.Restart {
		if c.Now().Sub(motion.started) >= historyForkFadeDuration {
			v.dispatchHistoryRestart()
		} else {
			c.AnimationFrame()
		}
		return
	}
	switch motion.phase {
	case historyExiting:
		if c.Now().Sub(motion.started) >= historyShatterDuration {
			motion.phase, motion.runningShown = historyShowingRunning, c.Now()
		}
	case historyShowingRunning:
		if c.Now().After(motion.runningShown) {
			motion.phase, motion.collapseStarted = historyCollapsing, c.Now()
		}
	case historyCollapsing:
		if c.Now().Sub(motion.collapseStarted) >= historyCollapseDuration {
			v.dispatchHistoryRestart()
		}
	}
	if motion.phase < historyDispatched {
		c.AnimationFrame()
	}
}

func (v *View) dispatchHistoryRestart() {
	motion := v.historyExit
	if motion == nil || motion.phase >= historyDispatched {
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
		edit.OperationID, edit.SubmittedText, edit.SubmittedReferences = in.OperationID, in.Text, in.References
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
	if v.historyExit.phase < historyCollapsing && !v.reduceMotion {
		return
	}
	p := float32(c.Now().Sub(v.historyExit.collapseStarted)) / float32(historyCollapseDuration)
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

// Retain the desktop effect's concrete adapter for its raster regression.
type historyShatter struct {
	progress float32
	seed     uint32
}

func (historyShatter) EffectMargin() n.Point { return (conversationui.Shatter{}).EffectMargin() }
func (fx historyShatter) PaintEffect(p *n.Painter, box n.Rect, content *n.Bitmap, inset n.Point) {
	(conversationui.Shatter{Progress: fx.progress, Seed: fx.seed}).PaintEffect(p, box, content, inset)
}
