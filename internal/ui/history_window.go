package ui

import (
	"context"
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"sort"
	"time"
)

type conversationHistory struct {
	direction                          float32
	directionAt                        time.Time
	session, epoch, source, connection string
	read                               *app.HistoryRead
	cancel                             context.CancelFunc
	error                              string
	indexBusy, indexDone               bool
	indexBefore                        int64
	indexCancel                        context.CancelFunc
	indexError                         string
	summaryCancel                      context.CancelFunc
	summaryIDs                         []string
	summaryBusy                        bool
	summaryAt                          time.Time
}

func (v *View) cancelPagedHistory() {
	if p := v.historyPage; p != nil {
		if s := v.finds[p.session]; s != nil && s.remote != nil {
			if s.remote.cancel != nil {
				s.remote.cancel()
			}
			s.remote = nil
		}
		if p.cancel != nil {
			p.cancel()
		}
		if p.indexCancel != nil {
			p.indexCancel()
		}
		if p.summaryCancel != nil {
			p.summaryCancel()
		}
		if v.Model.HistoryReads[p.session] == p.read {
			delete(v.Model.HistoryReads, p.session)
		}
	}
	v.historyPage = nil
}

func (v *View) syncPagedHistory(c *n.Context) {
	m := v.Model
	d := m.Details[m.SessionID]
	if p := v.historyPage; p != nil && (p.session != m.SessionID || p.epoch != m.ConnectionEpoch || p.source != d.Session.ThreadID || v.devices) {
		v.cancelPagedHistory()
	}
	if d.History == nil || !m.Connected || m.WorkerMinor < 37 || v.Controller == nil {
		return
	}
	if v.historyPage == nil {
		v.historyPage = &conversationHistory{session: m.SessionID, epoch: m.ConnectionEpoch, source: d.Session.ThreadID, connection: m.ConnectionID()}
	}
	p := v.historyPage
	if m.HistoryOpenings[m.SessionID] {
		if state := v.lists[m.SessionID]; state != nil {
			state.ScrollToEnd()
		}
		delete(m.HistoryOpenings, m.SessionID)
	}
	if r := m.HistoryReads[m.SessionID]; r != nil && r != p.read {
		if r.Pending {
			return
		}
		if r.Error != "" {
			p.error = r.Error
			delete(m.HistoryReads, m.SessionID)
		}
	}
	if d.History.Invalidated && p.read == nil && p.error == "" && v.historyExit == nil {
		if p.indexCancel != nil {
			p.indexCancel()
		}
		p.indexBusy, p.indexDone, p.indexBefore, p.indexError = false, false, 0, ""
		v.requestHistory(domain.HistoryQuery{}, true)
		return
	}
	if !p.summaryBusy && p.read == nil && p.error == "" && !d.History.Invalidated && (len(p.summaryIDs) > 0 || d.History.SummaryDirty > 0) {
		remaining := 500*time.Millisecond - v.frameTime.Sub(p.summaryAt)
		if remaining > 0 {
			c.After(remaining)
		} else {
			v.readHistorySummary()
		}
	}
	if !p.indexBusy && !p.indexDone && p.indexError == "" {
		v.readHistoryOutline()
	}
	if f := v.findState(); f.placement == findAwaitingHistory && f.result.ItemID != "" && p.read == nil && p.error == "" {
		found := false
		for _, item := range d.Items {
			found = found || item.ID == f.result.ItemID
		}
		if !found {
			v.requestHistory(domain.HistoryQuery{AroundID: f.result.ItemID}, true)
		}
	}
}

func (v *View) requestHistory(q domain.HistoryQuery, replace bool) {
	p := v.historyPage
	if p == nil || p.read != nil || !v.Model.Connected {
		return
	}
	if p.summaryCancel != nil {
		p.summaryCancel()
		p.summaryCancel = nil
	}
	p.summaryBusy = false
	p.summaryIDs = nil
	q.SessionID, q.ThreadID, q.Rounds = p.session, p.source, v.Model.Preferences.Normalized().HistoryPageRounds
	q.Limit = 0
	q.Generation = v.Model.Snapshot.Generation
	findQuery, fromFind := v.findState().query, q.AroundID != "" && v.findState().placement == findAwaitingHistory
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.error = ""
	p.direction = 0
	r := v.Model.BeginHistoryRead(p.session)
	p.read = r
	finish := func() {
		cancel()
		p.read = nil
		p.cancel = nil
		p.direction = 0
		if v.Model.HistoryReads[p.session] == r {
			delete(v.Model.HistoryReads, p.session)
		}
	}
	if !v.do(app.Job{Context: ctx, Method: "session.history", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if v.historyPage != p || historyConnectionEpoch(m, p.connection) != p.epoch || m.Details[p.session].Session.ThreadID != p.source || ctx.Err() != nil {
			finish()
			return
		}
		if fromFind && (v.findState().query != findQuery || v.findState().mode == findClosed) {
			finish()
			return
		}
		defer finish()
		if err != nil {
			p.error = err.Error()
			return
		}
		var d domain.SessionDetail
		if json.Unmarshal(b, &d) != nil || d.Session.ID != p.session || d.History == nil {
			p.error = "历史内容与会话不匹配"
			return
		}
		if err = m.MergeHistoryPage(d, r, replace, q); err != nil {
			p.error = err.Error()
			return
		}
		v.historyBaseline = true
		v.toolArrivals = nil
		v.replyStreams = nil
		if replace {
			if q.AroundID != "" {
				v.historyTarget = q.AroundID
			} else if state := v.lists[p.session]; state != nil {
				state.ScrollToEnd()
			}
		}
	}}) {
		finish()
		p.error = "设备忙，请重试加载历史"
	}
}

func (v *View) readHistoryOutline() {
	p := v.historyPage
	if p == nil {
		return
	}
	if _, ok := v.Model.HistoryOutlines[p.session]; !ok {
		v.Model.HistoryOutlines[p.session] = domain.HistoryOutline{SessionID: p.session, ThreadID: p.source}
	}
	q := domain.HistoryQuery{SessionID: p.session, ThreadID: p.source, Before: p.indexBefore, Limit: 1000, Generation: v.Model.Snapshot.Generation}
	ctx, cancel := context.WithCancel(context.Background())
	p.indexCancel = cancel
	p.indexBusy = true
	if !v.do(app.Job{Context: ctx, Method: "session.outline", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		defer cancel()
		if v.historyPage != p || historyConnectionEpoch(m, p.connection) != p.epoch || ctx.Err() != nil || m.Details[p.session].Session.ThreadID != p.source {
			return
		}
		p.indexBusy = false
		p.indexCancel = nil
		var page domain.HistoryOutline
		if err == nil && (json.Unmarshal(b, &page) != nil || page.SessionID != p.session || page.ThreadID != p.source) {
			err = errors.New("提问目录与会话不匹配")
		}
		if err != nil {
			p.indexError = err.Error()
			return
		}
		index := m.HistoryOutlines[p.session]
		if index.ThreadID != p.source {
			index = domain.HistoryOutline{SessionID: p.session, ThreadID: p.source}
		}
		entries := map[string]domain.HistoryPrompt{}
		for _, list := range [][]domain.HistoryPrompt{page.Prompts, index.Prompts} {
			for _, entry := range list {
				entries[entry.ID] = entry
			}
		}
		index.Prompts = nil
		for _, entry := range entries {
			index.Prompts = append(index.Prompts, entry)
		}
		sort.Slice(index.Prompts, func(i, j int) bool { return index.Prompts[i].Order < index.Prompts[j].Order })
		index.Older, index.Sequence = page.Older, page.Sequence
		m.HistoryOutlines[p.session] = index
		if page.Older {
			if len(page.Prompts) == 0 {
				p.indexError = "提问目录没有推进"
				return
			}
			next := page.Prompts[len(page.Prompts)-1].Order
			if next <= 0 || p.indexBefore > 0 && next >= p.indexBefore {
				p.indexError = "提问目录没有推进"
				return
			}
			p.indexBefore = next
		} else {
			p.indexDone = true
		}
	}}) {
		cancel()
		p.indexBusy = false
		p.indexError = "设备忙，请重试加载提问目录"
	}
}

func (v *View) historyPageControl(c *n.Context, t *n.Theme, newer bool) {
	d := v.Model.Details[v.Model.SessionID]
	p := v.historyPage
	if d.History == nil || p == nil {
		return
	}
	initial := v.Model.HistoryReads[v.Model.SessionID]
	if p.read != nil || initial != nil && initial.Pending {
		n.Text(c, "正在加载历史…").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
		return
	}
	label := "加载更早消息"
	q := domain.HistoryQuery{Before: d.History.First}
	if newer {
		label = "加载后续消息"
		q = domain.HistoryQuery{After: d.History.Last}
	}
	if p.error != "" {
		label = "重试加载历史"
	}
	if !newer && p.indexError != "" {
		if control(c).KeepFocus().Height(20).Label("重试加载提问目录").Tooltip(p.indexError).Children(func() { n.Text(c, "重试加载提问目录").FontSize(11).TextColor(t.TextMuted) }).Clicked() {
			p.indexError = ""
			v.readHistoryOutline()
		}
		return
	}
	if !newer && !d.History.Older && p.error == "" {
		return
	}
	if control(c).KeepFocus().Height(20).Label(label).Tooltip(p.error).Children(func() { n.Text(c, label).FontSize(11).TextColor(t.TextMuted) }).Clicked() {
		if d.History.Invalidated {
			v.requestHistory(domain.HistoryQuery{}, true)
		} else {
			v.requestHistory(q, false)
		}
	}
}

func (v *View) positionHistoryTarget(state *n.ListState, rows []transcriptRow) {
	if v.historyTarget == "" {
		return
	}
	for i, row := range rows {
		if row.key == v.historyTarget {
			state.KeepRowWithinContent(i+1, 80)
			v.historyTarget = ""
			return
		}
	}
}

func (v *View) prefetchHistory(state *n.ListState, rows []transcriptRow) {
	d := v.Model.Details[v.Model.SessionID]
	p := v.historyPage
	if d.History != nil {
		if state.FollowingEnd() && !d.History.Newer {
			delete(v.Model.HistoryAnchors, v.Model.SessionID)
		} else {
			first, _ := state.Visible()
			at := min(max(0, first-1), len(rows)-1)
			for ; at >= 0; at-- {
				if rows[at].item.ID != "" {
					v.Model.HistoryAnchors[v.Model.SessionID] = rows[at].item.ID
					break
				}
			}
		}
	}
	if d.History == nil || p == nil || p.read != nil || p.error != "" || v.historyTarget != "" || v.findState().mode != findClosed || p.direction == 0 || v.frameTime.Sub(p.directionAt) > 350*time.Millisecond {
		return
	}
	first, last := state.Visible()
	if p.direction < 0 && d.History.Older && first <= 2 {
		v.requestHistory(domain.HistoryQuery{Before: d.History.First}, false)
	} else if p.direction > 0 && d.History.Newer && last >= len(rows) {
		v.requestHistory(domain.HistoryQuery{After: d.History.Last}, false)
	}
}

func (v *View) historyInput(ev n.InputEvent) bool {
	p := v.historyPage
	if p == nil {
		return false
	}
	if ev.Kind == n.InputScroll && ev.DY != 0 {
		p.direction = ev.DY
		p.directionAt = ev.Time
		v.services.Invalidate()
	}
	return false
}

func historyConnectionEpoch(m *app.Model, connection string) string {
	if state := m.DeviceStates[connection]; state != nil {
		return state.ConnectionEpoch
	}
	return m.ConnectionEpoch
}

func (v *View) readHistorySummary() {
	p := v.historyPage
	d := v.Model.Details[p.session]
	if len(p.summaryIDs) == 0 {
		for _, g := range d.History.Groups {
			p.summaryIDs = append(p.summaryIDs, g.ID)
		}
		h := *d.History
		h.SummaryDirty = 0
		d.History = &h
		v.Model.Details[p.session] = d
	}
	if len(p.summaryIDs) == 0 {
		return
	}
	id := p.summaryIDs[0]
	p.summaryIDs = p.summaryIDs[1:]
	ctx, cancel := context.WithCancel(context.Background())
	p.summaryCancel = cancel
	p.summaryBusy = true
	p.summaryAt = v.frameTime
	q := domain.HistoryQuery{Generation: v.Model.Snapshot.Generation, SessionID: p.session, ThreadID: p.source, AroundID: id, SummaryOnly: true}
	if !v.do(app.Job{Context: ctx, Method: "session.history", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		defer cancel()
		if v.historyPage != p || ctx.Err() != nil || historyConnectionEpoch(m, p.connection) != p.epoch {
			return
		}
		p.summaryBusy = false
		p.summaryCancel = nil
		current := m.Details[p.session]
		if current.History == nil || current.History.Invalidated || current.Session.ThreadID != p.source {
			return
		}
		var page domain.SessionDetail
		if err != nil || json.Unmarshal(b, &page) != nil || page.Session.ID != p.session || page.Session.ThreadID != p.source || page.History == nil {
			p.error = "工具状态更新失败，请重试加载历史"
			return
		}
		if current.History.LiveSequence > page.Sequence {
			h := *current.History
			h.SummaryDirty = current.History.LiveSequence
			current.History = &h
			m.Details[p.session] = current
			return
		}
		h := *current.History
		h.Groups = append([]domain.HistoryToolGroup(nil), h.Groups...)
		for i, g := range h.Groups {
			for _, next := range page.History.Groups {
				if g.ID == next.ID {
					h.Groups[i] = next
				}
			}
		}
		current.History = &h
		m.Details[p.session] = current
	}}) {
		cancel()
		p.summaryBusy = false
		p.error = "设备忙，请重试加载历史"
	}
}

func (v *View) hasHistoryPageControl() bool {
	p := v.historyPage
	d := v.Model.Details[v.Model.SessionID]
	if p == nil || d.History == nil {
		return false
	}
	initial := v.Model.HistoryReads[v.Model.SessionID]
	return d.History.Older || p.error != "" || p.indexError != "" || p.read != nil || initial != nil && initial.Pending
}
