package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

func (a *mobileApp) queueRequest(c *ui.Context, sid string) *mobileRequest {
	return ui.Local(c.Root(), a.model.WorkKey(sid, "queue-action"), func() mobileRequest { return mobileRequest{} })
}

func (a *mobileApp) queue(c *ui.Context, parent *mygo.Window, session domain.Session, editor *mobileDraftEditor, req *mobileRequest, compact bool) {
	p := palette(c)
	sid := session.ID
	q := a.model.Details[sid].Queue
	motion := &editor.queueMotion
	a.queueEditRecovery(c, parent, editor, req, q)
	panel := ui.Column(c.Key("queue:"+sid)).Background(p.panel).Radius(16).Border(1, p.line).Clip().Shrink(0)
	present := q.SessionID == sid && len(q.Items) > 0
	if present && len(q.Items) == 1 && motion.consumed != "" && q.Items[0].Input.OperationID == motion.consumed {
		present = false
	}
	if present {
		motion.display = q
	} else if motion.display.SessionID != sid {
		// Keep the layout channel mounted at zero before the first queued input.
		panel.Jump("queue-height", 0)
		panel.Hidden(true)
		return
	}

	rowHeight := max(mobileclient.QueueRowHeight, mobileTapSize(c))
	viewport := func(count int) float32 {
		height := mobileclient.QueueViewport(count) / mobileclient.QueueRowHeight * rowHeight
		if compact {
			return min(height, 1.25*rowHeight)
		}
		return height
	}
	pauseHeight := float32(0)
	if motion.display.Paused {
		pauseHeight = mobileTapSize(c)
	}
	shown := motion.display
	full := viewport(len(shown.Items)) + pauseHeight
	target := float32(0)
	if present {
		target = viewport(len(q.Items)) + pauseHeight
	}
	arriving := motion.arrival != "" && slices.ContainsFunc(q.Items, func(item domain.QueueItem) bool {
		return item.Input.OperationID == motion.arrival
	})
	arrival := float32(1)
	if arriving {
		if motion.animating != motion.arrival {
			motion.animating = motion.arrival
			panel.Jump("queue-arrival", 0)
		}
		arrival = panel.AnimateTo("queue-arrival", 1, mobileSendScrollAnimation)
		if arrival >= 1 {
			motion.arrival, motion.animating = "", ""
		}
	}
	height := float32(0)
	var remaining float32
	var lifting, departing bool
	if !present {
		remaining, lifting, departing = motion.steerDeparture(c, &editor.sendScroll, session.Status.Active())
	}
	if departing {
		height = panel.Jump("queue-height", full*remaining)
	} else if arriving {
		previous := viewport(len(q.Items) - 1)
		if len(q.Items) > 1 {
			previous += pauseHeight
		}
		height = panel.Jump("queue-height", previous+(target-previous)*arrival)
	} else {
		height = panel.AnimateTo("queue-height", target, ui.Animation{Duration: mobileReplyFade, Ease: ui.EaseOut})
	}
	if height <= 0 {
		if !present {
			motion.display = domain.QueueState{}
		}
		panel.Hidden(true)
		return
	}
	exposed := min(1, height/max(1, full))
	if lifting {
		if list := a.history[sid]; list != nil {
			// The panel's negative margin cancels the unoccupied share of its gap.
			list.EndOverlayRelease += height + composerPanelGap*exposed
		}
	}
	panel.Height(height).MinHeight(0).Margin(0, 0, -composerPanelGap*(1-exposed)).
		IgnoreInput(!present).AccessHidden(!present)
	editing := a.model.ActiveQueueEdit(sid).ID != ""
	ink := panel.AnimateTo("queue-edit-content", map[bool]float32{true: .58, false: 1}[editing],
		ui.Animation{Duration: mobileReplyFade, Ease: ui.EaseOut})
	panel.Children(func() {
		ui.Column(c.Key("queue-stack")).Opacity(ink).Absolute().Left(0).Right(0).Top(0).Children(func() {
			if shown.Paused {
				available, reserved, _, _ := a.queueAvailability(session, req, q)
				ui.Row(c.Key("paused")).Height(pauseHeight).Shrink(0).PaddingX(12).AlignItems(ui.Center).Gap(8).Children(func() {
					oneLine(c, "队列已暂停", captionTextSize, p.secondary).Grow(1)
					action(c, "恢复", func() {
						a.mutateQueue(parent, session, req, "queue.continue", protocol.QueueInput{SessionID: sid, Revision: q.Revision})
					}).KeepFocus().Disabled(!available || reserved)
				})
			}
			list := ui.Scroll(c.Key("queue-items")).WidthPercent(100).Height(viewport(len(shown.Items))).Shrink(0).
				Clip().Label("待发消息列表")
			list.Children(func() {
				for index, entry := range shown.Items {
					row := c.Key(entry.ID)
					input := queueItemMessage(entry.Input)
					title := strings.Join(strings.Fields(sentMessageText(mobileclient.SentMessageRuns(input))), " ")
					if title == "" {
						title = "附件"
					}
					slotHeight := rowHeight
					newRow := arriving && entry.Input.OperationID == motion.arrival
					if newRow {
						slotHeight *= arrival
					}
					slot := ui.Column(row).Height(slotHeight).Shrink(0)
					if lifting {
						slot.Invisible()
					}
					if newRow {
						slot.Clip()
					} else if !arriving && !c.Preferences().ReduceMotion {
						slot.Transition(ui.ElementTransition{Position: true, Duration: mobileReplyFade, Ease: ui.EaseOut})
					}
					slot.Children(func() {
						item := ui.ButtonBase(row).FillWidth().Height(rowHeight).Shrink(0).
							PaddingX(12).AlignItems(ui.Center).KeepFocus().Label(title).Role(ui.RoleButton).
							Description("轻点查看操作，长按预览完整消息")
						if present {
							key := fmt.Sprintf("queue:%q:%q:%q:%q:%d", a.model.Snapshot.DeviceID, sid, entry.ID, a.model.ConnectionEpoch, q.Revision)
							menu := a.queueContextTarget(parent, session, editor, req, q, index, input)
							if len(menu.commands) > 0 {
								item.Menu(func(actions *ui.Menu) {
									for _, command := range menu.commands {
										if actions.Item(command.Title).Disabled(command.Disabled).Chosen() {
											command.run()
										}
									}
								})
							}
							queueContextMenuFrame(c).add(item, key, menu, panel, list, slot)
						}
						item.Children(func() {
							if index > 0 {
								ui.Box(row).Absolute().Top(0).Left(12).Right(12).Height(1).Background(p.line).PassThrough()
							}
							preview := ui.Column(row).Grow(1).MinWidth(0).Gap(2).PassThrough()
							if newRow && motion.arrivalSource.W > 0 && !c.Preferences().ReduceMotion {
								preview.Transition(ui.ElementTransition{From: &motion.arrivalSource, Duration: mobileSendScrollAnimation.Duration,
									Ease: mobileSendEase, Position: true, Lift: true})
							}
							preview.DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
								if present {
									motion.remember(entry, bounds)
								}
							}).Children(func() {
								queueMessagePreview(row, entry.Input)
								for _, attachment := range entry.Input.Attachments {
									if !attachment.InlineReference() {
										oneLine(row, attachment.Name, captionTextSize, p.secondary).Ellipsis("…")
										break
									}
								}
							})
						})
					})
				}
			})
		})
	})
	if req.err != "" {
		label(c, req.err, captionTextSize, p.warning).Role(ui.RoleStatus)
	}
}

func queueItemMessage(input domain.Submission) domain.Item {
	return domain.Item{Kind: "user", Text: input.Text, Attachments: input.Attachments, Skills: input.Skills, ReferencePositions: input.ReferencePositions}
}

func (a *mobileApp) queueAvailability(session domain.Session, req *mobileRequest, q domain.QueueState) (available, reserved, canSend bool, turn string) {
	m, sid := a.model, session.ID
	available = m.Connected && !req.busy && !a.queueSavePending(sid) && m.ActiveQueueEdit(sid).ID == "" && !a.sessionStop(session).pending()
	reserved = slices.ContainsFunc(q.Items, func(item domain.QueueItem) bool { return item.State.Reserved() })
	if !available || m.HasPendingInputForSession(sid) || session.ContextChanged || session.ExecutionGuard != "" {
		return
	}
	for _, request := range m.Snapshot.PendingRequests {
		if request.SessionID == sid && pendingRequest(request) {
			return
		}
	}
	if session.Status.Active() {
		turn = m.ActiveTurnForSession(sid)
		canSend = turn != "" && m.SessionReadiness(session).Capabilities.Steer
	} else {
		canSend = session.Status.CanSubmit()
	}
	return
}

func (a *mobileApp) queueContextTarget(parent *mygo.Window, source domain.Session, editor *mobileDraftEditor,
	req *mobileRequest, q domain.QueueState, index int, message domain.Item) contextMenuTarget {
	entry := q.Items[index]
	preview := &contextMenuMessage{Text: sentMessageText(mobileclient.SentMessageRuns(message))}
	for _, attachment := range message.Attachments {
		if !attachment.InlineReference() {
			preview.Attachments = append(preview.Attachments, attachment.Name)
		}
	}
	target := contextMenuTarget{message: preview}
	available, reserved, canSend, turn := a.queueAvailability(source, req, q)
	if !available || entry.State.Reserved() {
		return target
	}

	m, epoch := a.model, a.model.ConnectionEpoch
	add := func(id, title, symbol string, destructive bool, run func()) {
		target.commands = append(target.commands, contextMenuCommand{
			ID: id, Title: title, Symbol: symbol, Destructive: destructive,
			run: func() {
				if a.model != m || m.ConnectionEpoch != epoch || !a.visibleLinkSource(parent, source) {
					return
				}
				latest := m.Details[source.ID].Queue
				if latest.Revision != q.Revision {
					req.err = "待发队列已更新，请重试"
					parent.Invalidate()
					return
				}
				available, reserved, canSend, currentTurn := a.queueAvailability(a.sessionByID(source.ID), req, latest)
				if !available || !editableQueueItem(latest, entry.ID) ||
					id == "send" && (!canSend || currentTurn != turn) ||
					(id == "up" || id == "down") && reserved {
					return
				}
				run()
			},
		})
	}
	in := protocol.QueueInput{SessionID: source.ID, Revision: q.Revision, ID: entry.ID}
	if canSend {
		add("send", "立即发送", "arrow.up.circle", false, func() {
			if position, ok := editor.queueMotion.sources[entry.Input.OperationID]; ok {
				position.turn = turn
				editor.queueMotion.sources[entry.Input.OperationID] = position
			}
			send := in
			send.TurnID = turn
			a.mutateQueue(parent, source, req, "queue.send", send)
		})
	}
	add("edit", "编辑", "pencil", false, func() { a.beginQueueEdit(parent, source, editor, req, entry.ID) })
	if !reserved && index > 0 {
		add("up", "上移", "arrow.up", false, func() {
			move := in
			move.BeforeID = q.Items[index-1].ID
			a.mutateQueue(parent, source, req, "queue.reorder", move)
		})
	}
	if !reserved && index+1 < len(q.Items) {
		add("down", "下移", "arrow.down", false, func() {
			move := in
			if index+2 < len(q.Items) {
				move.BeforeID = q.Items[index+2].ID
			}
			a.mutateQueue(parent, source, req, "queue.reorder", move)
		})
	}
	add("remove", "删除", "trash", true, func() { a.mutateQueue(parent, source, req, "queue.remove", in) })
	return target
}

func editableQueueItem(q domain.QueueState, id string) bool {
	return slices.ContainsFunc(q.Items, func(item domain.QueueItem) bool {
		return item.ID == id && !item.State.Reserved()
	})
}

func (a *mobileApp) queueSavePending(sid string) bool {
	_, saving := a.queueSaves[a.model.WorkKey(sid, "queue-edit")]
	return saving
}

func (a *mobileApp) queueEditRecovery(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, req *mobileRequest, q domain.QueueState) {
	m, sid := a.model, editor.target.session
	edit := m.QueueEdits[sid]
	if edit.Submitted == nil {
		if edit.ID != "" && !editableQueueItem(q, edit.ID) {
			label(c, "待发消息当前不可编辑", captionTextSize, palette(c).warning).Role(ui.RoleStatus)
		}
		return
	}
	if a.queueSavePending(sid) {
		label(c, "正在保存待发消息…", captionTextSize, palette(c).secondary).Role(ui.RoleStatus)
		return
	}
	canResume := func() bool {
		return !req.busy && !a.queueSavePending(sid) && !editor.pending() &&
			!m.HasPendingInputForSession(sid) && m.HistoryEdits[sid].ItemID == ""
	}
	action(c.Key("resume-queue-edit"), "恢复待发编辑", func() {
		if !editor.current(a, parent) || !canResume() || m.QueueEdits[sid].Token != edit.Token {
			return
		}
		editor.Publish(m, editor.target.key)
		req.err = ""
		revision := edit.Revision
		latest := m.Details[sid].Queue
		if m.Connected && latest.SessionID == sid && editableQueueItem(latest, edit.ID) {
			revision = max(revision, latest.Revision)
		}
		m.ResumeQueueEdit(sid, revision)
		a.save()
	}).AlignSelf(ui.Start).PaddingX(2).KeepFocus().Disabled(!canResume())
}

func (a *mobileApp) reconcileQueueEdits() {
	m := a.model
	if !m.Connected || m.ConnectionEpoch == "" {
		return
	}
	for sid, edit := range m.QueueEdits {
		q := m.Details[sid].Queue
		if q.SessionID == sid && mobileclient.QueueEditRecorded(q, edit) {
			delete(m.QueueEdits, sid)
		}
	}
}

func (a *mobileApp) beginQueueEdit(parent *mygo.Window, session domain.Session, editor *mobileDraftEditor, req *mobileRequest, id string) {
	if !editor.current(a, parent) || editor.pending() || req.busy || !a.model.Connected {
		return
	}
	m := a.model
	editor.Publish(m, editor.target.key)
	req.err = ""
	if err := m.EditQueuedInSession(session.ID, id); err != nil {
		req.err = err.Error()
	}
	a.save()
	parent.Invalidate()
}

func (a *mobileApp) mutateQueue(parent *mygo.Window, session domain.Session, req *mobileRequest, method string, in protocol.QueueInput) {
	if !a.visibleLinkSource(parent, session) {
		return
	}
	if a.previewConversationMotion != "" && method == "queue.send" {
		a.previewDispatchMotion(in.SessionID, in.ID, in.TurnID)
		return
	}
	a.pageJob(parent, req, method, in, decodePage(func(q domain.QueueState) error {
		if q.SessionID != in.SessionID || q.Revision < in.Revision {
			return fmt.Errorf("设备返回的队列状态与操作不一致")
		}
		a.model.SetQueue(q)
		return nil
	}))
}

func (a *mobileApp) saveQueueEdit(parent *mygo.Window, session domain.Session, editor *mobileDraftEditor, req *mobileRequest) {
	owner := a.controller
	if owner == nil || !editor.current(a, parent) || editor.pending() || req.busy || !a.model.Connected ||
		!a.visibleLinkSource(parent, session) {
		return
	}
	m, sid := a.model, session.ID
	editor.Publish(m, editor.target.key)
	in, err := m.QueueEditInput(sid)
	if err != nil {
		req.err = err.Error()
		return
	}
	edit := m.ActiveQueueEdit(sid)
	if !editableQueueItem(m.Details[sid].Queue, edit.ID) {
		return
	}
	key := m.WorkKey(sid, "queue-edit")
	if a.queueSavePending(sid) {
		return
	}
	m.SuspendQueueEdit(sid)
	if err := a.save(); err != nil {
		m.ResumeQueueEdit(sid, edit.Revision)
		req.err = err.Error()
		return
	}
	var prepare func() error
	if a.stateWriter != nil {
		prepare = a.stateWriter.FlushLatest
	}
	if a.queueSaves == nil {
		a.queueSaves = make(map[app.WorkKey]string)
	}
	a.queueSaves[key] = edit.Token
	req.err = ""
	// Settle the owning model even if the conversation page was closed.
	finish := func(current *app.Model, body json.RawMessage, err error) {
		token, saving := a.queueSaves[key]
		if current != m || a.model != m || a.controller != owner || !saving || token != edit.Token {
			return
		}
		delete(a.queueSaves, key)
		if err == nil {
			var q domain.QueueState
			err = json.Unmarshal(body, &q)
			if err == nil {
				if q.SessionID != sid || q.Revision <= in.Revision {
					err = fmt.Errorf("设备返回的队列状态与操作不一致")
				} else {
					m.SetQueue(q)
				}
			}
		}
		if saved := m.QueueEdits[sid]; saved.Token == edit.Token && saved.Submitted != nil {
			if err == nil || mobileclient.QueueEditRecorded(m.Details[sid].Queue, saved) {
				delete(m.QueueEdits, sid)
			} else {
				req.err = mobileclient.RedactOperationDiagnostics(err.Error())
			}
		}
	}
	if !owner.Do(app.Job{Method: "queue.edit", Input: in, Prepare: prepare, ConnectionEpoch: m.ConnectionEpoch, Apply: finish}) {
		finish(m, nil, &protocol.RemoteError{Code: "notSent", Message: "请求繁忙，请稍后重试"})
	}
	a.refresh()
}
