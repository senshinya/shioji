package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

var queueEditIcon = svg("M13 3l4 4M4 13l-1 5 5-1L18 6l-4-4L4 13z")
var queueRemoveIcon = svg("M5 5l10 10M15 5L5 15")

// Window-space text positions survive worker confirmation and queue removal.
// They belong only to the session currently on screen, never saved history.
type queuedMessageSource struct {
	item      domain.QueueItem
	bounds    n.Rect
	delivered bool
}

const queueArrivalDuration = 360 * time.Millisecond

type queuedArrivalMotion struct {
	session, epoch string
	started        time.Time
	phase          queueArrivalPhase
}

type queueArrivalPhase uint8

const (
	queueAwaitingBoth queueArrivalPhase = iota
	queueAwaitingReceipt
	queueAwaitingRow
	queueArriving
)

func newQueuedArrival(accepted bool) queuedArrivalMotion {
	a := queuedArrivalMotion{}
	if accepted {
		a.phase = queueAwaitingRow
	}
	return a
}
func (a *queuedArrivalMotion) accepted() bool {
	return a.phase == queueAwaitingRow || a.phase == queueArriving
}
func (a *queuedArrivalMotion) observe(receipt, row bool, now time.Time) {
	switch a.phase {
	case queueAwaitingBoth:
		if receipt && row {
			a.phase = queueArriving
		} else if receipt {
			a.phase = queueAwaitingRow
		} else if row {
			a.phase = queueAwaitingReceipt
		}
	case queueAwaitingReceipt:
		if receipt {
			a.phase = queueArriving
		}
	case queueAwaitingRow:
		if row {
			a.phase = queueArriving
		}
	}
	if a.phase == queueArriving && a.started.IsZero() {
		a.started = now
	}
}

func (v *View) beginQueueArrival(id string, ready bool) *queuedArrivalMotion {
	if v.reduceMotion {
		return nil
	}
	if v.queueArrivals == nil {
		v.queueArrivals = map[string]*queuedArrivalMotion{}
	}
	arrival := newQueuedArrival(ready)
	a := &arrival
	a.session, a.epoch = v.Model.SessionID, v.Model.ConnectionEpoch
	v.queueArrivals[id] = a
	return a
}

// The arriving row's allocated height grows with the queue. The same
// progress pushes its existing rows up and reveals the new row at the bottom.
func (v *View) queueArrivalProgress(c *n.Context, id string) float32 {
	a := v.queueArrivals[id]
	if a == nil {
		return 1
	}
	if a.phase != queueArriving {
		return 0
	}
	p := min(max(float32(c.Now().Sub(a.started))/float32(queueArrivalDuration), 0), 1)
	if p < 1 {
		c.AnimationFrame()
	}
	return composerDescentEase(p)
}
func (v *View) cancelQueuedEdit() {
	m := v.Model
	sid := m.SessionID
	id := m.QueueEdits[sid].ID
	if id == "" {
		return
	}
	m.CancelQueueEdit(sid)
	v.focusComposer = true
}

func (v *View) rememberQueuedSource(item domain.QueueItem, bounds n.Rect) {
	source := v.queueSources[item.ID]
	source.item, source.bounds = item, bounds
	v.queueSources[item.ID] = source
}
func (v *View) syncQueuedMotions() {
	m := v.Model
	if v.queueSourceSession != m.SessionID || v.queueSourceEpoch != m.ConnectionEpoch || v.devices {
		v.queueSources = map[string]queuedMessageSource{}
		v.queueSourceSession, v.queueSourceEpoch = m.SessionID, m.ConnectionEpoch
		clear(v.queueArrivals)
		v.queueDisplay = domain.QueueState{}
	}
	if v.queueSources == nil {
		v.queueSources = map[string]queuedMessageSource{}
	}

	for id, a := range v.queueArrivals {
		if v.reduceMotion || a.epoch != m.ConnectionEpoch || a.session != m.SessionID || !a.started.IsZero() && v.frameTime.Sub(a.started) >= queueArrivalDuration {
			delete(v.queueArrivals, id)
			continue
		}
		seen := false
		for _, item := range m.Queue().Items {
			if item.ID == id {
				seen = true
				break
			}
		}
		previous := a.phase
		a.observe(m.Resolved[id], seen, v.frameTime)
		if previous != queueArriving && a.phase == queueArriving {
			v.composerHeightUntil = v.frameTime.Add(queueArrivalDuration)
		}
	}
	newest := -1
	for id, source := range v.queueSources {
		if !source.delivered && source.item.Input.OperationID != "" {
			for index, item := range m.Details[m.SessionID].Items {
				if item.Kind == "user" && item.ID == "input:"+source.item.Input.OperationID {
					if index > newest {
						v.sent = &sentMessageMotion{input: item.ID, origin: m.SessionID, session: m.SessionID, epoch: m.ConnectionEpoch, workspace: m.WorkspaceID, source: n.Rect{X: source.bounds.X - 16, Y: source.bounds.Y - 12, W: source.bounds.W + 32, H: source.bounds.H + 24}, queued: true, focused: true}
						newest = index
					}
					source.delivered = true
					v.queueSources[id] = source
					break
				}
			}
		}
		present := false
		for _, item := range m.Queue().Items {
			if item.ID == id {
				present = true
				break
			}
		}
		if !present {
			delete(v.queueSources, id)
		}
	}
}

func (v *View) queueDisclosure(c *n.Context, t *n.Theme) {
	m := v.Model
	sid := m.SessionID
	q := m.Queue()
	edit := m.QueueEdits[sid]
	if v.queueDrag != nil && (v.queueDrag.session != sid || v.queueDrag.epoch != m.ConnectionEpoch) {
		v.queueDrag = nil
	}

	held := false
	present := len(q.Items) > 0
	if sid == "" {
		v.queueDisplay = domain.QueueState{}
		v.queueDrag = nil
		return
	}
	if present {
		v.queueDisplay = q
	} else if v.queueDisplay.SessionID == sid && len(v.queueDisplay.Items) > 0 {
		q = v.queueDisplay
		v.queueDrag = nil
	}

	bg := raised(t)
	if t.Dark {
		bg = t.Surface
	}
	root := n.Column(c.Key("queued-turns:"+sid)).Label("待发送队列").FillWidth().Gap(0).Background(bg).Border(1, t.Border).Radius(16, 16, 0, 0).Margin(0, 0, -17, 0).Clip()
	opacity := float32(1)
	if edit.ID != "" {
		opacity = .45
	}
	root.Opacity(root.AnimateTo("queue-edit-opacity", opacity, motionAnimation(c, 180*time.Millisecond)))
	rowHeight := float32(0)
	arriving, accepted := false, false
	for _, item := range q.Items {
		rowHeight += 32 * v.queueArrivalProgress(c, item.ID)
		a := v.queueArrivals[item.ID]
		arriving = arriving || a != nil
		accepted = accepted || a == nil || a.accepted()
	}
	targetPresence := float32(0)
	if present && accepted {
		targetPresence = 1
	}
	presenceMotion := motionAnimation(c, 160*time.Millisecond)
	if arriving {
		presenceMotion = n.Animation{Duration: queueArrivalDuration, Ease: composerDescentEase}
	}
	presence := root.AnimateTo("queue-presence", targetPresence, presenceMotion)
	if !present && presence <= .001 {
		v.queueDisplay = domain.QueueState{}
		root.Height(0).Hidden(true).Margin(0).Border(0, n.Transparent)
		return
	}
	root.IgnoreInput(!present).Children(func() {
		n.Row(c).FillWidth().Height(32).Shrink(0).PaddingX(14).AlignItems(n.Center).Gap(8).Children(func() {
			label := fmt.Sprintf("待发送 %d", len(q.Items))
			trigger := n.ButtonBase(c.Key("queue-disclosure")).Height(32).Grow(1).MinWidth(0).Justify(n.Start).Label(label)
			color := textHoverColor(c, trigger, t.Text.Alpha(.86))
			trigger.DrawOver(func(_ *n.Painter, r n.Rect) {
				for _, item := range q.Items {
					if present && !m.QueueExpanded[sid] {
						v.rememberQueuedSource(item, n.Rect{X: r.X + 20, Y: r.Y + 6, W: r.W - 20, H: 20})
					}
				}
			})
			trigger.Children(func() {
				font := n.Font{Family: c.Theme().Font, Size: 12, Weight: 500}
				n.Row(c).Gap(3).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset("待发送", font))).Children(func() {
					angle := float32(-90)
					if m.QueueExpanded[sid] {
						angle = 0
					}
					inlineSymbol(c.Key("queue-chevron"), chevron, "待发送", font, 14, subtle(t), angle).Width(16)
					n.Text(c, "待发送").FontSize(12).FontWeight(500).FixedLineHeight(18).TextColor(color)
					n.Text(c, fmt.Sprint(len(q.Items))).FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
				})
			})
			if trigger.Clicked() && present {
				m.QueueExpanded[sid] = !m.QueueExpanded[sid]
			}
			if q.Paused {
				font := n.Font{Family: c.Theme().Font, Size: 11, Weight: 400}
				n.Row(c).Height(26).Gap(8).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset("已暂停", font))).Children(func() {
					n.Text(c, "已暂停").FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Tooltip(q.Reason)
					if ghost(c, "继续队列").Font(c.Theme().Font).FontSize(11).FixedLineHeight(18).Height(26).Disabled(!m.Connected || v.queueBusy[sid]).Clicked() && present {
						v.queueCommand("queue.continue", protocol.QueueInput{SessionID: sid, Revision: q.Revision}, nil)
					}
				})
			}
		})
		_, windowHeight := c.Size()
		full := min(rowHeight, min(float32(96), windowHeight*.22)) + 4
		body := n.Column(c.Key("queue-body")).FillWidth().Gap(0).Shrink(0).Clip()
		extent := full
		if arriving {
			body.Jump("queue-content-height", full)
		} else {
			extent = body.AnimateTo("queue-content-height", full, motionAnimation(c, 160*time.Millisecond))
		}
		expansion := float32(0)
		if m.QueueExpanded[sid] {
			expansion = 1
		}
		height := extent * body.AnimateTo("queue-expanded", expansion, motionAnimation(c, 160*time.Millisecond))
		// Continue the backplate behind the composer's 16 px top corners.
		surfaceHeight := (32 + height) * presence
		if arriving {
			surfaceHeight = 32*presence + height
		}
		root.Height(surfaceHeight + 16).Transform(n.Translation(0, 16*(1-presence)))
		body.Height(height).Children(func() {
			if height <= .1 {
				return
			}
			n.Column(c).Absolute().Left(0).Right(0).Top(0).Height(extent).Gap(0).Children(func() {
				scroll := n.Scroll(c.Key("queue-scroll")).FillWidth().Height(extent).Padding(0, 4).Label("待发送消息列表")
				scroll.Children(func() {
					list := n.Column(c.Key("queue-rows")).FillWidth().Gap(0)
					items := v.previewQueueRows(c, list, q)
					list.Children(func() {
						for i, item := range items {
							i, item := i, item
							progress := v.queueArrivalProgress(c, item.ID)
							motion := n.ElementTransition{Position: !c.Preferences().ReduceMotion && !arriving, Colors: true, Duration: motionAnimation(c, 160*time.Millisecond).Duration, Ease: n.EaseOut}
							slot := n.Column(c.Key("queue-slot:" + item.ID)).FillWidth().Height(32 * progress).Shrink(0).Transition(motion)
							if progress < 1 {
								slot.Clip()
							}
							slot.Children(func() {
								row := n.Row(c.Key(item.ID)).Absolute().Left(0).Right(0).Top(0).FillWidth().Height(32).PaddingX(10).Gap(3).Radius(5).AlignItems(n.Center)

								if present {
									held = v.queueRowGestures(c, row, q, item) || held
								}
								hover := n.Transparent
								if row.Hovered() || row.FocusWithin() {
									hover = t.Text.Alpha(.04)
								}
								row.Background(row.AnimateColor("queue-row-hover", hover, motionAnimation(c, 120*time.Millisecond)))
								if row.Dragging() {
									row.Background(t.SurfaceHover).Shadow(0, 2, 6, 0, n.RGBA(0, 0, 0, .08))
								}

								row.Children(func() {
									n.Text(c, fmt.Sprint(i+1)).Width(16).TextAlign(n.Center).Font("Menlo").FontSize(11).FixedLineHeight(18).TextColor(subtle(t))
									preview := item.Input.Text
									if edit.ID == item.ID {
										preview = m.Drafts["session:"+sid]
									}
									text := strings.ReplaceAll(strings.TrimSpace(preview), "\n", " ")
									if len(item.Input.Attachments) > 0 {
										if text != "" {
											text += " · "
										}
										text += fmt.Sprintf("%d 个附件", len(item.Input.Attachments))
									}
									queuedText := n.Text(c, text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(14).FixedLineHeight(20).TextColor(t.Text.Alpha(.86)).Tooltip(preview)
									queuedText.DrawOver(func(_ *n.Painter, r n.Rect) {
										if present {
											v.rememberQueuedSource(item, r)
										}
									})
									if item.State != "" {
										n.Text(c, map[string]string{"dispatching": "发送中", "failed": "未发送", "unknown": "待核实"}[string(item.State)]).FontSize(11).TextColor(subtle(t))
									}
									visible := float32(0)
									width, _ := c.Size()
									if row.Hovered() || row.FocusWithin() || width < 720 {
										visible = 1
									}
									n.Row(c).Gap(1).Opacity(row.AnimateTo("queue-actions", visible, motionAnimation(c, 120*time.Millisecond))).Children(func() {
										locked := !m.Connected || v.queueBusy[sid] || item.State.Reserved()
										for _, action := range []struct {
											label    string
											icon     *n.SVG
											disabled bool
											run      func()
										}{
											{fmt.Sprintf("立即发送待发消息 %d", i+1), arrow, locked || (m.Active() && m.ActiveTurn() == "") || item.State != "", func() {
												v.queueCommand("queue.send", protocol.QueueInput{SessionID: sid, ID: item.ID, Revision: q.Revision, TurnID: m.ActiveTurn()}, nil)
											}},
											{fmt.Sprintf("编辑待发消息 %d", i+1), queueEditIcon, locked || edit.ID != "" || m.HasPendingInput(), func() {
												v.editQueued(item.ID)
											}},
											{fmt.Sprintf("移除待发消息 %d", i+1), queueRemoveIcon, !m.Connected || v.queueBusy[sid] || item.State == "dispatching" || edit.ID == item.ID, func() {
												v.queueCommand("queue.remove", protocol.QueueInput{SessionID: sid, ID: item.ID, Revision: q.Revision}, nil)
											}},
										} {
											if control(c.Key(action.label), 5).Size(25, 26).Label(action.label).Disabled(action.disabled).Children(func() { n.Icon(c, action.icon).Size(12, 12) }).Clicked() && present {
												action.run()
											}
										}
									})
								})
							})
						}
						end := n.Column(c.Key("queue-end")).Height(4).Shrink(0)
						if m.QueueExpanded[sid] {
							for _, item := range q.Items {
								if a := v.queueArrivals[item.ID]; a != nil && a.accepted() {
									end.ScrollIntoView()
									break
								}
							}
						}
					})
				})
			})
		})
	})
	if !held && v.queueDrag != nil && v.queueDrag.phase != queueDragSaving {
		v.queueDrag = nil
		c.AnimationFrame()
	}
}

func (v *View) queueEditBar(c *n.Context, t *n.Theme) {
	m := v.Model
	sid := m.SessionID
	if m.QueueEdits[sid].ID == "" {
		return
	}
	n.Row(c.Key("queue-edit-bar")).Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 180*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}}).Absolute().Left(6).Right(14).Top(10).Height(28).Padding(0, 0, 6, 10).Gap(5).AlignItems(n.Center).Children(func() {
		n.Icon(c, queueEditIcon).Size(12, 12).TextColor(t.TextMuted)
		n.Text(c, "编辑待发消息").FontSize(11).FixedLineHeight(22).TextColor(t.TextMuted)
		n.Spacer(c).Grow(1)
		if ghost(c, "取消编辑").PaddingX(0).Height(22).Disabled(v.queueBusy[sid]).Clicked() {
			v.cancelQueuedEdit()
		}
	})
}
func (v *View) queueCommand(method string, in protocol.QueueInput, done func(domain.QueueState)) {
	if v.Controller == nil {
		if method == "queue.reorder" {
			v.queueDrag = nil
		}
		return
	}
	if v.queueBusy == nil {
		v.queueBusy = map[string]bool{}
	}
	v.queueBusy[in.SessionID] = true
	if !v.do(app.Job{Method: method, Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		delete(v.queueBusy, in.SessionID)
		if method == "queue.reorder" && v.queueDrag != nil && v.queueDrag.session == in.SessionID {
			v.queueDrag = nil
		}
		if err != nil {
			m.Error = err.Error()
			return
		}
		var q domain.QueueState
		if err = json.Unmarshal(b, &q); err != nil {
			m.Error = err.Error()
			return
		}
		m.SetQueue(q)
		if done != nil {
			done(q)
		}
	}}) {
		delete(v.queueBusy, in.SessionID)
		if method == "queue.reorder" && v.queueDrag != nil && v.queueDrag.session == in.SessionID {
			v.queueDrag = nil
		}
	}
}
func (v *View) saveQueued() {
	m := v.Model
	sid := m.SessionID
	edit := m.QueueEdits[sid]
	text := m.Drafts[m.DraftKey()]
	if edit.ID == "" || v.queueBusy[sid] || !m.Connected || (strings.TrimSpace(text) == "" && len(m.Attachments[m.DraftKey()]) == 0) {
		return
	}
	refs, err := m.SubmissionAttachments(m.WorkspaceID)
	if err != nil {
		m.Error = err.Error()
		return
	}
	v.queueCommand("queue.edit", protocol.QueueInput{Attachments: refs, SessionID: sid, ID: edit.ID, Revision: edit.Revision, Text: text}, func(q domain.QueueState) {
		m.FinishQueueEdit(sid, edit.ID, text, q, refs)
		if m.SessionID == sid {
			v.focusComposer = true
		}
	})
}
func (v *View) submitActive(in domain.Submission, method, turn string) {
	m := v.Model
	var sent *sentMessageMotion
	var payload any = in
	var arrival *queuedArrivalMotion
	if method == "turn.enqueue" {
		arrival = v.beginQueueArrival(in.OperationID, false)
	}
	if method == "turn.steer" {
		sent = v.beginSendMotion(in)
		payload = protocol.SteerInput{Input: in, TurnID: turn}
	}
	var prepare func() error
	if v.Saver != nil {
		state, _ := v.persistenceModel().EncodeState()
		v.Saver.Queue(state)
		v.lastQueued = state
		prepare = v.Saver.FlushLatest
	}
	if !v.do(app.Job{Method: method, Input: payload, Prepare: prepare, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if err != nil {
			if v.queueArrivals[in.OperationID] == arrival {
				delete(v.queueArrivals, in.OperationID)
			}
			if sent != nil && v.sent == sent {
				v.sent = nil
			}
			m.SubmissionError(in, err)
			return
		}
		var op domain.Operation
		if err = json.Unmarshal(b, &op); err != nil {
			if v.queueArrivals[in.OperationID] == arrival {
				delete(v.queueArrivals, in.OperationID)
			}
			if sent != nil && v.sent == sent {
				v.sent = nil
			}
			m.Error = err.Error()
			return
		}
		m.Accepted(in, op)
		if arrival != nil && op.State != "confirmed" && v.queueArrivals[in.OperationID] == arrival {
			delete(v.queueArrivals, in.OperationID)
		}
		if sent != nil && v.sent == sent {
			if op.State == "rejected" || op.State == "indeterminate" {
				v.sent = nil
			} else {
				sent.session = op.SessionID
			}
		}
	}}) {
		if v.queueArrivals[in.OperationID] == arrival {
			delete(v.queueArrivals, in.OperationID)
		}
		if sent != nil && v.sent == sent {
			v.sent = nil
		}
		m.Reject(in, "操作繁忙，请重试")
	}
}
