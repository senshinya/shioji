package ui

import (
	"context"
	"encoding/json"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

type workHistoryUI struct {
	visible     bool
	before      int64
	direction   float32
	directionAt time.Time
	cancel      context.CancelFunc
}

func (u *workDetailUI) cancelHistoryRead() {
	if u.history != nil && u.history.cancel != nil {
		u.history.cancel()
		u.history.cancel = nil
		u.generation++
		u.phase = workUnread
	}
}

func (v *View) beginWorkHistoryFrame() {
	for _, pane := range []*View{v, v.secondary} {
		if pane == nil {
			continue
		}
		for _, panel := range pane.workPanels {
			for _, u := range panel.details {
				if u.history != nil {
					u.history.visible = false
				}
			}
		}
	}
}

func (v *View) cancelHiddenWorkHistory() {
	for _, pane := range []*View{v, v.secondary} {
		if pane == nil {
			continue
		}
		for key, panel := range pane.workPanels {
			for id, u := range panel.details {
				if u.history == nil || u.history.visible {
					continue
				}
				u.cancelHistoryRead()
				v.Model.ReleaseWorkHistory(app.WorkKey{DeviceID: key.DeviceID, SessionID: key.SessionID, ID: id})
				u.history, u.streams = nil, nil
				u.phase, u.hydrated, u.list = workUnread, false, n.ListState{}
			}
		}
	}
}

func (v *View) readWorkHistory(w domain.WorkActivity, u *workDetailUI, force bool, before int64) {
	m := v.Model
	if w.Revision == 0 && !force || !m.Connected || v.Controller == nil {
		return
	}
	sid, epoch := m.SessionID, m.ConnectionEpoch
	source := m.Details[sid].Session.ThreadID
	if u.history == nil || u.epoch != epoch || u.source != source {
		u.cancelHistoryRead()
		u.history = &workHistoryUI{}
		u.phase, u.hydrated, u.streams = workUnread, false, nil
		u.epoch, u.source = epoch, source
	}
	_, cached := m.WorkDetails[m.WorkKey(sid, w.ID)]
	if u.phase == workReading || !force && (u.phase == workReadFailed || u.phase == workRead && cached) {
		return
	}
	cache := v.splitRoot().Model
	key := cache.BeginWorkReadKey(m.WorkKey(sid, w.ID))
	// The initial snapshot also needs an untrimmed live merge target. It can
	// span several chunks while more than 64 new observations arrive.
	detail := cache.WorkDetails[key]
	if detail.History == nil || detail.ThreadID != source {
		detail.History, detail.ThreadID = &domain.HistoryWindow{}, source
		cache.WorkDetails[key] = detail
	}
	hydrate, sequence := !u.hydrated, m.Sequence
	if hydrate {
		u.readSequence = sequence
	}
	u.phase, u.error = workReading, ""
	u.generation++
	generation, p := u.generation, u.history
	p.before, p.direction = before, 0
	q := domain.WorkHistoryQuery{ID: w.ID, HistoryQuery: domain.HistoryQuery{SessionID: sid, ThreadID: source, Generation: m.Snapshot.Generation, Rounds: m.Preferences.Normalized().HistoryPageRounds, Before: before}}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	if !v.do(app.Job{Context: ctx, Method: "session.work.history", Input: q, Apply: func(m *app.Model, body json.RawMessage, err error) {
		defer cancel()
		if ctx.Err() != nil || u.generation != generation || m.ConnectionEpoch != epoch || m.Details[sid].Session.ThreadID != source {
			return
		}
		p.cancel = nil
		var d domain.WorkDetail
		if err == nil {
			if json.Unmarshal(body, &d) != nil || d.SessionID != sid || d.Work.ID != w.ID || d.ThreadID != source || d.History == nil {
				u.phase, u.error = workReadFailed, "子 Agent 历史与会话不匹配"
				return
			}
		} else {
			u.phase, u.error = workReadFailed, err.Error()
			return
		}
		observed := map[string]bool{}
		if hydrate {
			for _, entry := range m.WorkDetails[m.WorkKey(sid, w.ID)].Items {
				if entry.Revision > sequence {
					observed[entry.Item.ID] = true
				}
			}
		}
		if before > 0 {
			m.MergeWorkHistoryPage(d)
		} else {
			m.MergeWorkDetail(d)
		}
		if hydrate {
			u.seedHistory(m.WorkDetails[m.WorkKey(sid, w.ID)], observed, epoch)
		}
		u.hydrated, u.phase = true, workRead
	}}) {
		cancel()
		p.cancel = nil
		u.phase, u.error = workReadFailed, "设备忙，请重试加载历史"
	}
}

func (v *View) workHistoryScroll(c *n.Context, port n.Element, w domain.WorkActivity, u *workDetailUI, d domain.WorkDetail) {
	p := u.history
	if p == nil || d.History == nil {
		return
	}
	port.HandleInput(func(ev n.InputEvent) bool {
		if ev.Kind == n.InputScroll && ev.DY != 0 {
			p.direction, p.directionAt = ev.DY, ev.Time
			c.Services().Invalidate()
		}
		return false
	})
	port.DrawOver(func(_ *n.Painter, _ n.Rect) {
		if !d.History.Older || u.phase == workReading || u.error != "" || p.direction >= 0 || c.Now().Sub(p.directionAt) > 350*time.Millisecond {
			return
		}
		before, _ := u.list.ScrollEdges()
		if before <= max(float32(240), u.list.Bounds(c).H*.75) {
			v.readWorkHistory(w, u, true, d.History.First)
		}
	})
}
