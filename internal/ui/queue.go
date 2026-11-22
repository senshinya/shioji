package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
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
	preview   *homeSendPresentation
	motion    *sentMessageMotion
}

type queueCommandState struct {
	method, epoch string
	input         protocol.QueueInput
	editToken     string
}

func (v *View) pendingQueueCommand(sid string) *queueCommandState {
	pending := v.splitRoot().queueCommands[v.Model.WorkKey(sid, "queue")]
	if pending != nil && pending.epoch == v.Model.ConnectionEpoch {
		return pending
	}
	return nil
}

func queueItem(q domain.QueueState, id string) (domain.QueueItem, bool) {
	for _, item := range q.Items {
		if item.ID == id {
			return item, true
		}
	}
	return domain.QueueItem{}, false
}

func (p *queueCommandState) observed(q domain.QueueState) bool {
	if p == nil || q.Revision <= p.input.Revision {
		return false
	}
	item, exists := queueItem(q, p.input.ID)
	switch p.method {
	case "queue.remove", "queue.send":
		return !exists
	case "queue.edit":
		in := p.input
		return exists && item.Input.Text == in.Text && slices.Equal(item.Input.Attachments, in.Attachments) &&
			slices.Equal(item.Input.Skills, in.Skills) && slices.Equal(item.Input.ReferencePositions, in.ReferencePositions)
	case "queue.continue":
		return !q.Paused
	}
	return false
}

const queueArrivalDuration = 360 * time.Millisecond

type queuedArrivalMotion struct {
	input                  domain.Submission
	started                time.Time
	provisional, confirmed bool
}

func (v *View) beginQueueArrival(in domain.Submission) *queuedArrivalMotion {
	a := &queuedArrivalMotion{input: in, provisional: true}
	v.queueArrivals = append(v.queueArrivals, a)
	v.services.Invalidate()
	return a
}

func (v *View) queueArrival(id string) *queuedArrivalMotion {
	for _, a := range v.queueArrivals {
		if a.input.OperationID == id {
			return a
		}
	}
	return nil
}

func (a *queuedArrivalMotion) animating(now time.Time) bool {
	return a != nil && !a.started.IsZero() && now.Sub(a.started) < queueArrivalDuration
}

// The arriving row's allocated height grows with the queue. The same
// progress pushes its existing rows up and reveals the new row at the bottom.
func (a *queuedArrivalMotion) progress(c *n.Context) float32 {
	if !a.animating(c.Now()) {
		return 1
	}
	p := max(float32(c.Now().Sub(a.started))/float32(queueArrivalDuration), 0)
	c.AnimationFrame()
	return composerDescentEase(p)
}
func (v *View) cancelQueuedEdit() {
	m := v.Model
	sid := m.SessionID
	id := m.ActiveQueueEdit(sid).ID
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
	if v.queueSourceSession != m.SessionID || v.queueSourceEpoch != m.ConversationGeneration() || v.devices {
		v.queueSources = map[string]queuedMessageSource{}
		v.queueSourceSession, v.queueSourceEpoch = m.SessionID, m.ConversationGeneration()
		v.queueArrivals = nil
		v.queueDisplay = domain.QueueState{}
	}
	if v.queueSources == nil {
		v.queueSources = map[string]queuedMessageSource{}
	}

	q := m.Queue()
	v.queueArrivals = slices.DeleteFunc(v.queueArrivals, func(a *queuedArrivalMotion) bool {
		id := a.input.OperationID
		_, pending := m.Pending[id]
		hasRow := slices.ContainsFunc(q.Items, func(item domain.QueueItem) bool { return item.ID == id })
		if hasRow {
			a.provisional = false
		} else {
			// The event stream delivers the queue write before its receipt. If
			// both arrive with a removal, never bring the departed row back.
			streamReceipt := m.Resolved[id] && slices.ContainsFunc(m.Snapshot.Operations, func(op domain.Operation) bool { return op.ID == id && op.State == domain.OperationConfirmed })
			if !a.provisional || streamReceipt || !a.confirmed && (!pending || m.UncertainSubmissions[id]) {
				return true
			}
		}
		if v.reduceMotion {
			a.started = v.frameTime.Add(-queueArrivalDuration)
		} else if a.started.IsZero() {
			// Input callbacks can run after a long idle. Start on the first drawn frame.
			a.started = v.frameTime
			v.composerHeightUntil = v.frameTime.Add(queueArrivalDuration)
		}
		return hasRow && !pending && !a.animating(v.frameTime)
	})
	newest := -1
	for id, source := range v.queueSources {
		queued, present := queueItem(q, id)
		if h := source.preview; h != nil {
			switch {
			case queueInputObserved(m.Details[m.SessionID], source):
				if h.turn == "" {
					// An idle send owns the pending footer until the confirmed
					// turn can take over its row in the transcript.
					h.state = domain.OperationConfirmed
				} else {
					source.preview = nil
				}
			case present && queued.State == domain.QueueItemFailed:
				v.rejectQueuedSend(&source)
			case present && queued.State == domain.QueueItemUnknown:
				h.state = domain.OperationIndeterminate
			}
		}
		if !source.delivered && source.item.Input.OperationID != "" {
			for index, item := range m.Details[m.SessionID].Items {
				if item.Kind == "user" && item.ID == "input:"+source.item.Input.OperationID {
					if index > newest {
						v.sent = v.queuedSendMotion(source)
						newest = index
					}
					source.delivered = true
					break
				}
			}
		}
		if source.motion != nil && source.motion.phase == sentAnimating && v.frameTime.Sub(source.motion.started) >= sentScrollDuration+60*time.Millisecond {
			source.motion = nil
		}
		if !present && source.preview == nil && source.motion == nil {
			delete(v.queueSources, id)
		} else {
			v.queueSources[id] = source
		}
	}
}

func queueInputObserved(detail domain.SessionDetail, source queuedMessageSource) bool {
	return slices.ContainsFunc(detail.Items, func(item domain.Item) bool {
		return item.Kind == "user" && item.ID == "input:"+source.item.Input.OperationID
	})
}

func (v *View) queuedSendMotion(source queuedMessageSource) *sentMessageMotion {
	m := v.Model
	motion := &sentMessageMotion{input: "input:" + source.item.Input.OperationID, origin: m.SessionID, session: m.SessionID, epoch: m.ConversationGeneration(), workspace: m.WorkspaceID, queued: true, focused: true}
	motion.steer = source.preview != nil && source.preview.turn != ""
	if r := source.bounds; r.W > 0 {
		motion.source = n.Rect{X: r.X - 16, Y: r.Y - 12, W: r.W + 32, H: r.H + 24}
	}
	return motion
}

func (v *View) rejectQueuedSend(source *queuedMessageSource) {
	if v.sent == source.motion {
		v.sent = nil
	}
	source.preview, source.motion, source.delivered = nil, nil, false
}

func (v *View) projectQueuedSends(items []domain.Item) []domain.Item {
	var previews []*homeSendPresentation
	for _, source := range v.queueSources {
		if source.preview != nil {
			previews = append(previews, source.preview)
		}
	}
	slices.SortFunc(previews, func(a, b *homeSendPresentation) int { return a.sentAt.Compare(b.sentAt) })
	for _, preview := range previews {
		id := "input:" + preview.in.OperationID
		if slices.ContainsFunc(items, func(item domain.Item) bool { return item.ID == id && item.Kind == "queued" }) {
			// An owning-device snapshot may include the private queue record
			// before the actual transcript item is journaled.
			items = slices.DeleteFunc(slices.Clone(items), func(item domain.Item) bool { return item.ID == id && item.Kind == "queued" })
		}
		items = preview.projectInput(items)
	}
	return items
}

func (v *View) latestQueuedSendPreview(turn string) (string, *homeSendPresentation) {
	var id string
	var latest *homeSendPresentation
	for key, source := range v.queueSources {
		if h := source.preview; h != nil && h.turn == turn && (latest == nil || h.sentAt.After(latest.sentAt)) {
			id, latest = key, h
		}
	}
	return id, latest
}

func (v *View) queuedSendStatusRows(rows []transcriptRow) []transcriptRow {
	id, h := v.latestQueuedSendPreview("")
	if h == nil {
		return rows
	}
	key, input := "queue-send-status:"+h.in.OperationID, "input:"+h.in.OperationID
	for _, row := range rows {
		if row.key == input && v.adoptSendFooter(rows, row.item.TurnID, key) {
			source := v.queueSources[id]
			source.preview = nil
			v.queueSources[id] = source
			return rows
		}
	}
	if len(rows) > 0 && rows[len(rows)-1].key == input {
		label := h.pendingLabel()
		if h.state == domain.OperationConfirmed {
			label = "进行中"
		}
		rows = append(rows, transcriptRow{key: key, preparing: label, end: -1, opacity: 1})
	}
	return rows
}

func (v *View) queueDisclosure(c *n.Context, t *n.Theme) {
	m := v.Model
	sid := m.SessionID
	q := m.Queue()
	edit := m.ActiveQueueEdit(sid)
	pending := v.pendingQueueCommand(sid)
	workerQueue := q
	recoverable := m.QueueEdits[sid].Submitted != nil && (pending == nil || pending.epoch != m.ConnectionEpoch)
	if v.queueDrag != nil && (v.queueDrag.session != sid || v.queueDrag.epoch != m.ConnectionEpoch) {
		v.queueDrag = nil
	}

	if sid == "" {
		v.queueDisplay = domain.QueueState{}
		v.queueDrag = nil
		return
	}
	provisional := false
	if pending != nil && pending.method == "queue.remove" {
		q.Items = slices.DeleteFunc(slices.Clone(q.Items), func(item domain.QueueItem) bool { return item.ID == pending.input.ID })
	}
	if len(v.queueSources) > 0 && slices.ContainsFunc(q.Items, func(item domain.QueueItem) bool { return v.queueSources[item.ID].delivered }) {
		q.Items = slices.DeleteFunc(slices.Clone(q.Items), func(item domain.QueueItem) bool { return v.queueSources[item.ID].delivered })
	}
	for _, a := range v.queueArrivals {
		if a.provisional {
			if !provisional {
				q.Items = slices.Clone(q.Items)
				q.SessionID = sid
				v.queueDrag = nil
				provisional = true
			}
			q.Items = append(q.Items, domain.QueueItem{ID: a.input.OperationID, Input: a.input})
		}
	}
	held := false
	present := len(q.Items) > 0
	if present {
		if _, saved := m.QueueExpanded[sid]; !saved {
			m.QueueExpanded[sid] = true
		}
		v.queueDisplay = q
	} else if v.queueDisplay.SessionID == sid && len(v.queueDisplay.Items) > 0 {
		q = v.queueDisplay
		v.queueDrag = nil
	}
	if recoverable && !present {
		if ghost(c.Key("resume-queue-edit"), "恢复待发编辑").Disabled(m.HasPendingInput() || m.HistoryEdits[sid].ItemID != "").Clicked() {
			v.editQueued(m.QueueEdits[sid].ID)
		}
	}

	bg := raised(t)
	if t.Dark {
		bg = t.Surface
	}
	const backplateOverlap, bottomMargin = 16, -17
	root := n.Column(c.Key("queued-turns:"+sid)).Label("待发送队列").FillWidth().Gap(0).Background(bg).Border(1, t.Border).Radius(16, 16, 0, 0).Margin(0, 0, bottomMargin, 0).Clip()
	opacity := float32(1)
	if edit.ID != "" {
		opacity = .45
	}
	// Dim the contents while keeping the backplate opaque over the transcript.
	contentOpacity := root.AnimateTo("queue-edit-opacity", opacity, motionAnimation(c, 180*time.Millisecond))
	rowHeight := float32(0)
	arriving := false
	for _, item := range q.Items {
		a := v.queueArrival(item.ID)
		rowHeight += 32 * a.progress(c)
		arriving = arriving || a.animating(c.Now())
	}
	targetPresence := float32(0)
	if present {
		targetPresence = 1
	}
	presenceMotion := motionAnimation(c, 160*time.Millisecond)
	if arriving {
		presenceMotion = n.Animation{Duration: queueArrivalDuration, Ease: composerDescentEase}
	}
	var departure *sentMessageMotion
	if !present && len(q.Items) == 1 && !v.reduceMotion && m.Connected {
		source := v.queueSources[q.Items[0].ID]
		if motion := source.motion; motion != nil && motion == v.sent && motion.steer &&
			(motion.phase == sentAnimating || m.Active()) &&
			(source.preview == nil || source.preview.state != domain.OperationIndeterminate) {
			departure = motion
		}
	}
	var presence float32
	if departure != nil {
		// Keep the source dock in place through a reply-footer handoff,
		// then let the lifted message and panel use the same clock.
		presence = root.Jump("queue-presence", 1-departure.progress(c))
	} else {
		presence = root.AnimateTo("queue-presence", targetPresence, presenceMotion)
	}
	if !present && presence <= .001 {
		v.queueDisplay = domain.QueueState{}
		root.Height(0).Hidden(true).Margin(0).Border(0, n.Transparent)
		return
	}
	root.IgnoreInput(!present).Children(func() {
		n.Row(c).FillWidth().Height(32).Shrink(0).PaddingX(14).AlignItems(n.Center).Gap(8).Opacity(contentOpacity).Children(func() {
			// Retain the queue-to-message animation's source width while only
			// the intrinsic title button handles disclosure clicks.
			heading := n.Row(c.Key("queue-heading")).Height(32).Grow(1).MinWidth(0)
			heading.DrawOver(func(_ *n.Painter, r n.Rect) {
				for _, item := range q.Items {
					if a := v.queueArrival(item.ID); present && !m.QueueExpanded[sid] && (a == nil || !a.provisional) {
						v.rememberQueuedSource(item, n.Rect{X: r.X + 20, Y: r.Y + 6, W: r.W - 20, H: 20})
					}
				}
			})
			heading.Children(func() {
				label := fmt.Sprintf("待发送 %d", len(q.Items))
				trigger := n.ButtonBase(c.Key("queue-disclosure")).Height(32).MinWidth(0).Justify(n.Start).Label(label)
				color := textHoverColor(c, trigger, t.Text.Alpha(.86))
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
			})
			if recoverable {
				if ghost(c.Key("resume-queue-edit"), "恢复待发编辑").FontSize(11).Disabled(m.HasPendingInput() || m.HistoryEdits[sid].ItemID != "").Clicked() {
					v.editQueued(m.QueueEdits[sid].ID)
				}
			}
			if q.Paused {
				font := n.Font{Family: c.Theme().Font, Size: 11, Weight: 400}
				n.Row(c).Height(26).Gap(8).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset("已暂停", font))).Children(func() {
					n.Text(c, "已暂停").FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Tooltip(q.Reason)
					label := "继续队列"
					if pending != nil && pending.method == "queue.continue" {
						label = "正在恢复队列"
					}
					if ghost(c.Key("continue-queue"), label).Font(c.Theme().Font).FontSize(11).FixedLineHeight(18).Height(26).Disabled(!m.Connected || pending != nil).Clicked() && present {
						v.queueCommand("queue.continue", protocol.QueueInput{SessionID: sid, Revision: q.Revision})
					}
				})
			}
		})
		_, windowHeight := c.Size()
		full := min(rowHeight, min(float32(96), windowHeight*.22)) + 4
		body := n.Column(c.Key("queue-body")).FillWidth().Gap(0).Shrink(0).Clip().Opacity(contentOpacity)
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
		root.Height(surfaceHeight + backplateOverlap).Transform(n.Translation(0, backplateOverlap*(1-presence)))
		if departure != nil && departure.phase == sentAnimating {
			if state := v.lists[sid]; state != nil {
				// Include the height and negative bottom margin that overlap
				// the editor, so the scroll targets the dock without the queue.
				state.EndOverlayRelease = max(0, surfaceHeight+backplateOverlap+bottomMargin)
			}
		}
		body.Height(height).Children(func() {
			if height <= .1 {
				return
			}
			n.Column(c).Absolute().Left(0).Right(0).Top(0).Height(extent).Gap(0).Children(func() {
				scroll := n.Scroll(c.Key("queue-scroll")).FillWidth().Height(extent).Padding(0, 4).Label("待发送消息列表")
				scroll.Children(func() {
					list := n.Column(c.Key("queue-rows")).FillWidth().Gap(0)
					items := q.Items
					if !provisional {
						items = v.previewQueueRows(c, list, q)
					}
					list.Children(func() {
						for i, item := range items {
							i, item := i, item
							a := v.queueArrival(item.ID)
							local := a != nil && a.provisional
							progress := a.progress(c)
							motion := n.ElementTransition{Position: !c.Preferences().ReduceMotion && !arriving, Colors: true, Duration: motionAnimation(c, 160*time.Millisecond).Duration, Ease: n.EaseOut}
							slot := n.Column(c.Key("queue-slot:" + item.ID)).FillWidth().Height(32 * progress).Shrink(0).Transition(motion)
							if departure != nil && departure.phase == sentAnimating {
								// The transcript bubble now paints the departing text.
								slot.Invisible()
							}
							if progress < 1 {
								slot.Clip()
							}
							slot.Children(func() {
								row := n.Row(c.Key(item.ID)).Absolute().Left(0).Right(0).Top(0).FillWidth().Height(32).PaddingX(10).Gap(3).Radius(5).AlignItems(n.Center)

								if present && !provisional {
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
									status := map[string]string{"dispatching": "正在发送", "failed": "未发送", "unknown": "待核实"}[string(item.State)]
									if edit.ID == item.ID {
										preview = m.Drafts["session:"+sid]
									}
									if pending != nil && pending.input.ID == item.ID && !pending.observed(workerQueue) {
										switch pending.method {
										case "queue.edit":
											preview, status = pending.input.Text, "正在保存"
										case "queue.send":
											status = "正在发送"
										}
									}
									text := strings.ReplaceAll(strings.TrimSpace(preview), "\n", " ")
									if len(item.Input.Attachments) > 0 {
										if text != "" {
											text += " · "
										}
										text += fmt.Sprintf("%d 个附件", len(item.Input.Attachments))
									}
									if len(item.Input.Skills) > 0 {
										if text != "" {
											text += " · "
										}
										for i, skill := range item.Input.Skills {
											if i > 0 {
												text += ", "
											}
											text += "/" + skill.Name
										}
									}
									queuedText := n.Text(c, text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(14).FixedLineHeight(20).TextColor(t.Text.Alpha(.86)).Tooltip(preview)
									queuedText.DrawOver(func(_ *n.Painter, r n.Rect) {
										if present && !local {
											v.rememberQueuedSource(item, r)
										}
									})
									if status != "" {
										n.Text(c.Key("queue-status"), status).FontSize(11).TextColor(subtle(t))
									}
									visible := float32(0)
									width, _ := c.Size()
									if row.Hovered() || row.FocusWithin() || width < 720 {
										visible = 1
									}
									shown := row.AnimateTo("queue-actions", visible, motionAnimation(c, 120*time.Millisecond))
									const actionsWidth = 3*25 + 2
									// Share one clock for the fade and reserved text width. The
									// controls keep their original size and right-edge position.
									n.Column(c.Key("queue-action-space")).Width((actionsWidth+3)*shown).Shrink(0).Margin(0, 0, 0, -3)
									n.Row(c.Key("queue-actions-group")).Absolute().Right(10).Top(3).Width(actionsWidth).Height(26).Gap(1).Opacity(shown).Children(func() {
										locked := local || !m.Connected || pending != nil || item.State.Reserved()
										for _, action := range []struct {
											label    string
											icon     *n.SVG
											disabled bool
											run      func()
										}{
											{fmt.Sprintf("立即发送待发消息 %d", i+1), arrow, locked || (m.Active() && (m.ActiveTurn() == "" || !m.RunReadiness().Capabilities.Steer)) || item.State != "", func() {
												v.queueCommand("queue.send", protocol.QueueInput{SessionID: sid, ID: item.ID, Revision: q.Revision, TurnID: m.ActiveTurn()})
											}},
											{fmt.Sprintf("编辑待发消息 %d", i+1), queueEditIcon, locked || edit.ID != "" || m.HasPendingInput(), func() {
												v.editQueued(item.ID)
											}},
											{fmt.Sprintf("移除待发消息 %d", i+1), queueRemoveIcon, local || !m.Connected || pending != nil || item.State == "dispatching" || edit.ID == item.ID, func() {
												v.queueCommand("queue.remove", protocol.QueueInput{SessionID: sid, ID: item.ID, Revision: q.Revision})
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
								if v.queueArrival(item.ID).animating(c.Now()) {
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
	if m.ActiveQueueEdit(sid).ID == "" {
		return
	}
	n.Row(c.Key("queue-edit-bar")).Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 180*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}}).Absolute().Left(6).Right(14).Top(10).Height(28).Padding(0, 0, 6, 10).Gap(5).AlignItems(n.Center).Children(func() {
		n.Icon(c, queueEditIcon).Size(12, 12).TextColor(t.TextMuted)
		n.Text(c, "编辑待发消息").FontSize(11).FixedLineHeight(22).TextColor(t.TextMuted)
		n.Spacer(c).Grow(1)
		if ghost(c, "取消编辑").PaddingX(0).Height(22).Disabled(v.pendingQueueCommand(sid) != nil).Clicked() {
			v.cancelQueuedEdit()
		}
	})
}
func (v *View) queueCommand(method string, in protocol.QueueInput) {
	m, root := v.Model, v.splitRoot()
	sid := in.SessionID
	if v.Controller == nil || v.pendingQueueCommand(sid) != nil {
		if method == "queue.reorder" {
			v.queueDrag = nil
		}
		return
	}
	key := m.WorkKey(sid, "queue")
	pending := &queueCommandState{method: method, epoch: m.ConnectionEpoch, input: in}
	if root.queueCommands == nil {
		root.queueCommands = map[app.WorkKey]*queueCommandState{}
	}
	root.queueCommands[key] = pending
	var prepare func() error
	switch method {
	case "queue.edit":
		pending.editToken = m.ActiveQueueEdit(sid).Token
		m.SuspendQueueEdit(sid)
		v.focusComposer = true
		if root.Saver != nil {
			root.queueState()
			prepare = root.Saver.FlushLatest
		}
	case "queue.send":
		if item, ok := queueItem(m.Queue(), in.ID); ok && item.Input.OperationID != "" {
			source := v.queueSources[in.ID]
			source.item = item
			source.preview = &homeSendPresentation{in: item.Input, sentAt: time.Now(), session: sid, turn: in.TurnID}
			source.motion, source.delivered = v.queuedSendMotion(source), true
			if v.queueSources == nil {
				v.queueSources = make(map[string]queuedMessageSource)
			}
			v.queueSources[in.ID], v.sent = source, source.motion
			v.services.Invalidate()
		}
	}
	finish := func(m *app.Model, b json.RawMessage, err error) {
		if root.queueCommands[key] != pending {
			return
		}
		delete(root.queueCommands, key)
		if method == "queue.reorder" && v.queueDrag != nil && v.queueDrag.session == sid {
			v.queueDrag = nil
		}
		var q domain.QueueState
		if err == nil {
			err = json.Unmarshal(b, &q)
			if err == nil && (q.SessionID != sid || q.Revision < in.Revision) {
				err = fmt.Errorf("设备返回的队列状态与操作不一致")
			}
		}
		if err == nil {
			m.SetQueue(q)
		}
		current := m.Details[sid].Queue
		observed := pending.observed(current)
		if method == "queue.send" && v.Model.SessionID == sid && v.Model.ConnectionEpoch == pending.epoch {
			if source, ok := v.queueSources[in.ID]; ok && err != nil {
				observed = observed || queueInputObserved(m.Details[sid], source)
				if !observed && source.preview != nil {
					var remote *protocol.RemoteError
					if errors.As(err, &remote) {
						v.rejectQueuedSend(&source)
					} else {
						source.preview.state = domain.OperationIndeterminate
						observed = true
					}
					v.queueSources[in.ID] = source
				}
			}
		}
		if method == "queue.edit" {
			save := m.QueueEdits[sid]
			if save.Submitted != nil && save.Token == pending.editToken {
				if err == nil || observed {
					delete(m.QueueEdits, sid)
				} else if v.Model.SessionID == sid && root.focusedView() == v {
					draftKey := "session:" + sid
					// Only reopen automatically if the user has left the ordinary
					// draft alone. Otherwise its editor and selection stay live.
					if m.Drafts[draftKey] == save.Displaced && slices.Equal(m.Attachments[draftKey], save.DisplacedAttachments) &&
						slices.Equal(m.DraftSkills[draftKey], save.DisplacedSkills) && !v.Model.HasPendingInput() && m.HistoryEdits[sid].ItemID == "" {
						m.ResumeQueueEdit(sid, save.Revision)
						v.focusComposer = v.composerHadFocus
					}
				}
			}
		}
		if err != nil && !observed {
			m.Error = err.Error()
		}
	}
	if !v.do(app.Job{Method: method, Input: in, Prepare: prepare, Apply: finish}) {
		finish(m, nil, &protocol.RemoteError{Code: "notSent", Message: "设备操作繁忙，请重试"})
	}
}

func (v *View) saveQueued() {
	m := v.Model
	sid := m.SessionID
	edit := m.ActiveQueueEdit(sid)
	if _, exists := queueItem(m.Queue(), edit.ID); !exists || v.pendingQueueCommand(sid) != nil || !m.Connected || !m.HasDraftContent(m.DraftKey()) {
		return
	}
	refs, err := m.SubmissionAttachments(m.WorkspaceID)
	if err != nil {
		m.Error = err.Error()
		return
	}
	skills, err := m.SubmissionSkills()
	if err != nil {
		m.Error = err.Error()
		return
	}
	if skills == nil {
		skills = []domain.SkillReference{}
	}
	v.queueCommand("queue.edit", protocol.QueueInput{ReferencePositions: m.DraftReferencePositions(m.DraftKey()), Skills: skills, Attachments: refs,
		SessionID: sid, ID: edit.ID, Revision: edit.Revision, Text: m.Drafts[m.DraftKey()]})
}

func (v *View) submitActive(in domain.Submission, method, turn string) {
	m := v.Model
	var sent *sentMessageMotion
	var payload any = in
	var arrival *queuedArrivalMotion
	if method == "turn.enqueue" {
		arrival = v.beginQueueArrival(in)
	}
	if method == "turn.steer" {
		sent = v.beginPresentedSend(in, turn)
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
			if arrival != nil {
				arrival.provisional = false
			}
			if sent != nil {
				v.presentedSendError(m, in, err)
			} else {
				m.SubmissionError(in, err)
			}
			return
		}
		var op domain.Operation
		if err = json.Unmarshal(b, &op); err != nil || op.ID != in.OperationID {
			err = errors.New("设备未确认本次发送")
			if arrival != nil {
				arrival.provisional = false
			}
			if sent != nil {
				v.presentedSendError(m, in, err)
			} else {
				m.SubmissionError(in, err)
			}
			return
		}
		m.Accepted(in, op)
		if sent != nil {
			v.acceptHomeSend(m, op)
		}
		if arrival != nil {
			arrival.confirmed = op.State == domain.OperationConfirmed
			if op.State.ReceiptObserved() && !arrival.confirmed {
				arrival.provisional = false
			}
		}
		if sent != nil && v.sent == sent {
			if op.State == "rejected" || op.State == "abandoned" {
				v.sent = nil
			} else {
				sent.session = op.SessionID
			}
		}
	}}) {
		if arrival != nil {
			arrival.provisional = false
		}
		if sent != nil && v.sent == sent {
			v.sent = nil
		}
		m.Reject(in, "操作繁忙，请重试")
	}
}
