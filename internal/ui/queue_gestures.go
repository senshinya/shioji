package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type queuedRowDrag struct {
	session, id, epoch string
	revision           int64
	grabY              float32
	to                 int
	moving, saving     bool
}

func (v *View) editQueued(id string) {
	if err := v.Model.EditQueued(id); err != nil {
		v.Model.Error = err.Error()
	} else {
		v.focusComposer = true
		delete(v.queueArrivals, id)
	}
}

// Preview order stays local until the worker acknowledges the drop. The rows'
// existing position transitions make room while native Drag follows the pointer.
func (v *View) previewQueueRows(c *n.Context, list n.Element, q domain.QueueState) []domain.QueueItem {
	dropped, hasDrop := n.Drop[queuedRowDrag](list)
	_, y, over := list.PointerPosition()
	d := v.queueDrag
	if d == nil || d.session != q.SessionID || d.epoch != v.Model.ConnectionEpoch || d.revision != q.Revision || (!d.moving && !d.saving) {
		return q.Items
	}
	if d.moving {
		if over {
			d.to = max(0, min(len(q.Items)-1, int((y-d.grabY+16)/32)))
		}
	}
	from := -1
	for i, item := range q.Items {
		if item.ID == d.id {
			from = i
			break
		}
	}
	if hasDrop && dropped.id == d.id && dropped.session == q.SessionID && dropped.epoch == v.Model.ConnectionEpoch && dropped.revision == q.Revision && from >= 0 && from != d.to {
		before := ""
		index := 0
		for _, item := range q.Items {
			if item.ID == d.id {
				continue
			}
			if index == d.to {
				before = item.ID
			}
			index++
		}
		d.saving = true
		d.moving = false
		v.queueCommand("queue.reorder", protocol.QueueInput{SessionID: q.SessionID, ID: d.id, BeforeID: before, Revision: d.revision}, nil)
	}
	if from < 0 || from == d.to {
		return q.Items
	}
	items := append([]domain.QueueItem(nil), q.Items...)
	item := items[from]
	if from < d.to {
		copy(items[from:d.to], items[from+1:d.to+1])
	} else {
		copy(items[d.to+1:from+1], items[d.to:from])
	}
	items[d.to] = item
	return items
}

// Native Drag owns pointer capture, the following copy, cancellation and edge
// scrolling. Keep the press-time revision even while dispatch temporarily locks.
func (v *View) queueRowGestures(c *n.Context, row n.Element, q domain.QueueState, item domain.QueueItem) bool {
	m := v.Model
	pressed, dragging := row.Pressed(), row.Dragging()
	d := v.queueDrag
	if d != nil && d.session == q.SessionID && d.id == item.ID && !d.saving && d.moving != dragging {
		d.moving = dragging
		c.AnimationFrame()
	}
	unlocked := m.Connected && !v.queueBusy[q.SessionID] && m.QueueEdits[q.SessionID].ID == ""
	if row.DoubleClicked() && unlocked && !m.HasPendingInput() && item.State != "dispatching" && item.State != "unknown" {
		v.editQueued(item.ID)
	}
	reorder := unlocked && len(q.Items) > 1
	for _, queued := range q.Items {
		if queued.State == "dispatching" || queued.State == "unknown" {
			reorder = false
		}
	}
	if !reorder {
		if d != nil && d.session == q.SessionID && d.id == item.ID && (pressed || dragging) {
			row.Drag(*d)
		}
		if dragging {
			row.Opacity(0).Cursor(n.CursorGrabbing)
		}
		return pressed || dragging
	}
	payload := queuedRowDrag{session: q.SessionID, id: item.ID, epoch: m.ConnectionEpoch, revision: q.Revision}
	if pressed && v.queueDrag == nil {
		_, payload.grabY, _ = row.PointerPosition()
		for i, queued := range q.Items {
			if queued.ID == item.ID {
				payload.to = i
				break
			}
		}
		v.queueDrag = &payload
	}
	d = v.queueDrag
	if d != nil && d.session == q.SessionID && d.id == item.ID {
		payload = *d
	}
	row.Drag(payload).Cursor(n.CursorGrab)
	if dragging {
		row.Opacity(0).Cursor(n.CursorGrabbing)
	}
	return pressed || dragging
}
