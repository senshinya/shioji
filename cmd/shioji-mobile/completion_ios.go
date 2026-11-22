package main

import (
	"errors"

	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

func (a *mobileApp) observeReadCompletion(c *ui.Context, footer ui.Element, list *ui.ListState, session domain.Session, turn domain.Turn, visit *[3]string) {
	model := a.model
	key := [3]string{session.ID, session.ThreadID, turn.ID}
	if model.WorkerMinor >= 39 {
		reply := session.Completion
		if reply == nil || reply.Pending || !reply.Unread || reply.HistoryRef != session.ThreadID || reply.TurnID != turn.ID || reply.ID == "" {
			return
		}
		key[2] = reply.ID
	} else if reply := model.CompletedReplies[session.ID]; !reply.Unread || reply.TurnID != turn.ID || reply.HistoryRef != session.ThreadID {
		return
	}
	if *visit == key {
		return
	}
	footer.DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
		viewport := list.Bounds(c)
		if viewport.H == 0 || bounds.H == 0 || bounds.Y >= viewport.Y+viewport.H || bounds.Y+bounds.H <= viewport.Y || *visit == key {
			return
		}
		*visit = key
		if model.WorkerMinor < 39 {
			model.ReadCompletedReply(session.ID)
			a.refresh()
			return
		}
		job, ok := model.CompletionReadJob(session.ID)
		if !ok {
			return
		}
		job.ConnectionEpoch = model.ConnectionEpoch
		if a.controller == nil || !a.controller.Do(job) {
			job.Apply(model, nil, errors.New("操作队列已满，请重试"))
			a.refresh()
		}
	})
}
