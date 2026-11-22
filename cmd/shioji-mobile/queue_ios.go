package main

import (
	"encoding/json"
	"fmt"
	"slices"

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

func (a *mobileApp) queue(c *ui.Context, parent *mygo.Window, session domain.Session, editor *mobileDraftEditor, req *mobileRequest) {
	p := palette(c)
	m, sid := a.model, session.ID
	q := m.Details[sid].Queue
	a.queueEditRecovery(c, parent, editor, req, q)
	if q.SessionID == "" {
		return
	}
	busy := !m.Connected || req.busy || a.queueSavePending(sid) || m.ActiveQueueEdit(sid).ID != ""
	stopping := a.sessionStop(session).pending()
	reserved := slices.ContainsFunc(q.Items, func(item domain.QueueItem) bool { return item.State.Reserved() })
	turn := m.ActiveTurnForSession(sid)
	canSend := !busy && !stopping && !m.HasPendingInputForSession(sid) && !session.ContextChanged && session.ExecutionGuard == ""
	if session.Status.Active() {
		canSend = canSend && turn != "" && m.SessionReadiness(session).Capabilities.Steer
	} else {
		turn = ""
		canSend = canSend && session.Status.CanSubmit()
	}
	for _, request := range m.Snapshot.PendingRequests {
		if request.SessionID == sid && pendingRequest(request) {
			canSend = false
			break
		}
	}
	change := func(method string, in protocol.QueueInput) {
		a.mutateQueue(parent, session, req, method, in)
	}
	target := mobileclient.QueueViewport(len(q.Items))
	panel := ui.Column(c.Key("queue:"+sid)).Background(p.panel).Radius(16).Border(1, p.line).Clip()
	height := panel.AnimateTo("queue-viewport:"+sid, target, mobileLayoutMotion)
	panel.Hidden(target == 0 && height < 1)
	resume := func() { change("queue.continue", protocol.QueueInput{SessionID: sid, Revision: q.Revision}) }
	pausedSingle := q.Paused && len(q.Items) == 1
	pausedMultiple := q.Paused && len(q.Items) > 1
	minimum := min(height, mobileclient.QueueRowHeight)
	if pausedMultiple {
		minimum += mobileTapSize(c)
	}
	panel.MinHeight(minimum + 2)
	panel.Children(func() {
		if pausedMultiple {
			ui.Row(c).Shrink(0).AlignItems(ui.Center).PaddingX(12).Children(func() {
				oneLine(c, "队列已暂停", 12, p.secondary).Grow(1)
				action(c, "恢复", resume).KeepFocus().Disabled(busy || stopping || reserved)
			})
		}
		rows := ui.Scroll(c).WidthPercent(100).Clip().Label("待发消息列表")
		rows.Height(height).MinHeight(min(height, mobileclient.QueueRowHeight)).Disabled(target == 0)
		if height > 0 {
			rows.Children(func() {
				for index, entry := range q.Items {
					row := c.Key(entry.ID)
					ui.Row(row).Height(mobileclient.QueueRowHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 2, 0, 12).
						Children(func() {
							if index > 0 {
								ui.Box(row).Absolute().Top(0).Left(12).Right(12).Height(1).Background(p.line).PassThrough()
							}
							in := protocol.QueueInput{SessionID: sid, Revision: q.Revision, ID: entry.ID, TurnID: turn}
							ui.Column(row).Grow(1).MinWidth(0).Gap(2).Children(func() {
								queueMessagePreview(row, entry.Input)
								detail := ""
								if pausedSingle {
									detail = "队列已暂停"
								}
								for _, attachment := range entry.Input.Attachments {
									if !attachment.InlineReference() {
										if detail != "" {
											detail += " · "
										}
										detail += attachment.Name
										break
									}
								}
								if detail != "" {
									oneLine(row, detail, captionTextSize, p.secondary).Ellipsis("…")
								}
							})
							if pausedSingle {
								action(row, "恢复", resume).PaddingX(3).KeepFocus().Disabled(busy || stopping || reserved)
							}
							title := fmt.Sprintf("发送第 %d 条待发消息", index+1)
							if session.Status.Active() {
								title = fmt.Sprintf("介入当前轮，第 %d 条待发消息", index+1)
							}
							ui.ButtonBase(row.Key("send")).Size(mobileTapSize(c), mobileTapSize(c)).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).
								Label(title).Role(ui.RoleButton).KeepFocus().Disabled(!canSend || entry.State.Reserved()).
								OnClick(func() { change("queue.send", in) }).Children(func() {
								ui.Row(row).Size(25, 25).Radius(13).Border(1, p.line).AlignItems(ui.Center).
									Justify(ui.Center).PassThrough().Children(func() {
									ui.Icon(row, sendSymbol).Size(14, 14).TextColor(p.ink).PassThrough()
								})
							})
							iconButton(row, "待发消息操作", moreSymbol, inlineIconSize, nil).KeepFocus().
								Disabled(busy || entry.State.Reserved()).PopoverMenuButton(func(menu *ui.Menu) {
								if menu.Item("编辑").Chosen() {
									a.beginQueueEdit(parent, session, editor, req, entry.ID)
								}
								if index > 0 && menu.Item("上移").Disabled(reserved).Chosen() {
									move := in
									move.BeforeID = q.Items[index-1].ID
									change("queue.reorder", move)
								}
								if index+1 < len(q.Items) && menu.Item("下移").Disabled(reserved).Chosen() {
									move := in
									if index+2 < len(q.Items) {
										move.BeforeID = q.Items[index+2].ID
									}
									change("queue.reorder", move)
								}
								if menu.Item("移除").Danger().Chosen() {
									change("queue.remove", in)
								}
							}, ui.PopoverMenuButtonOptions{At: ui.AnchorTopEnd, Self: ui.AnchorBottomEnd})
						})
				}
			})
		}
	})
	if req.err != "" {
		label(c, req.err, captionTextSize, p.warning).Role(ui.RoleStatus)
	}
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
}

func (a *mobileApp) mutateQueue(parent *mygo.Window, session domain.Session, req *mobileRequest, method string, in protocol.QueueInput) {
	if !a.visibleLinkSource(parent, session) {
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
