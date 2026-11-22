package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"sort"
	"time"
)

// Events observed during a page read bridge its snapshot to the current UI.
// The buffer lives only for the outstanding read, never in persisted state.
type HistoryRead struct {
	Pending  bool
	Error    string
	Events   []domain.Event
	items    map[string]int
	Overflow bool
}

func historyReadMethod(method string) bool {
	return method == "session.history" || method == "session.outline" || method == "session.matches" || method == "session.work.history"
}

func (c *Controller) queueInitialHistory(parent context.Context, id, epoch, generation string, opening bool, resuming ...bool) context.CancelFunc {
	return c.queueInitialHistoryPage(parent, id, epoch, generation, opening, len(resuming) != 0 && resuming[0], historySelection{})
}

func (c *Controller) queueInitialHistoryPage(parent context.Context, id, epoch, generation string, opening, resuming bool, target historySelection) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	r := &HistoryRead{Pending: true}
	c.post(func(m *Model) {
		if ctx.Err() != nil || m.ConnectionEpoch != epoch || !m.ownsSession(id) {
			return
		}
		d := m.Details[id]
		if !opening && resuming && target.item == "" && d.HasLoadedHistory() && d.History != nil && !d.History.Invalidated {
			delete(m.HistoryReads, id)
			cancel()
			return
		}
		m.HistoryReads[id] = r
		if opening {
			d = historyOpeningTail(d, m.Preferences.Normalized().HistoryPageRounds)
		} else if d.History == nil {
			d.History = &domain.HistoryWindow{Invalidated: true}
		}
		if d.Session.ThreadID == "" {
			for _, session := range m.Snapshot.Sessions {
				if session.ID == id {
					d.Session.ThreadID = session.ThreadID
				}
			}
		}
		m.Details[id] = d
		q := domain.HistoryQuery{SessionID: id, Generation: generation, Rounds: m.Preferences.Normalized().HistoryPageRounds}
		if target.item != "" {
			q.ThreadID, q.AroundID = target.thread, target.item
		} else if !opening {
			q.AroundID = m.HistoryAnchors[id]
		}
		if opening {
			if !d.HasLoadedHistory() {
				delete(m.HistoryAnchors, id)
			}
			m.FoldToolHistory[id] = true
		}
		if !c.Do(Job{Context: ctx, ConnectionEpoch: epoch, Method: "session.history", Input: q, Apply: func(m *Model, b json.RawMessage, err error) {
			defer cancel()
			if m.ConnectionEpoch != epoch || m.HistoryReads[id] != r {
				return
			}
			r.Pending = false
			if ctx.Err() != nil {
				delete(m.HistoryReads, id)
				return
			}
			var page domain.SessionDetail
			if err == nil && (json.Unmarshal(b, &page) != nil || page.Session.ID != id || page.History == nil) {
				err = errors.New("设备返回了无效的会话历史")
			}
			if err == nil {
				err = m.MergeHistoryPage(page, r, true, q)
			}
			if target.done != nil {
				target.done(err)
			}
			if err != nil {
				r.Error = err.Error()
				m.HistoryReads[id] = r
				return
			}
			if opening {
				m.FoldToolHistory[id] = true
			}
			if opening && target.item == "" || q.AroundID == "" {
				m.HistoryOpenings[id] = true
			}
		}}) {
			cancel()
			r.Pending = false
			r.Error = "设备忙，请重试加载历史"
			if target.done != nil {
				target.done(errors.New(r.Error))
			}
		}
	})
	return cancel
}

func historyOpeningTail(d domain.SessionDetail, rounds int) domain.SessionDetail {
	if d.History != nil && d.History.Newer {
		d.Items = nil
		d.History = &domain.HistoryWindow{Invalidated: true}
		d.HistoryLoaded = false
		return d
	}
	d.HistoryLoaded = d.HasLoadedHistory()
	h := &domain.HistoryWindow{Invalidated: true}
	var starts []int
	for i, item := range d.Items {
		goalContinuation := item.Kind == "activity" && item.Presentation == domain.GoalContinuationPresentation
		if item.WorkID == "" && (item.Kind == "user" || goalContinuation) {
			starts = append(starts, i)
		}
	}
	start := 0
	if len(starts) > rounds {
		boundary := len(starts) - rounds
		start = starts[boundary]
		marker := d.Items[start]
		if marker.Kind == "activity" && marker.Presentation == domain.GoalContinuationPresentation && marker.TurnID != "" {
			for i := starts[boundary-1] + 1; i < start; i++ {
				item := d.Items[i]
				if item.TurnID == marker.TurnID && item.WorkID == "" && item.Kind != "queued" {
					start = i
					break
				}
			}
		}
	}
	items := slices.Clone(d.Items[start:])
	if len(items) > 0 {
		h.First, h.Last, h.Older = items[0].Order, items[len(items)-1].Order, start > 0 || d.History != nil && d.History.Older
		if d.History != nil {
			for _, g := range d.History.Groups {
				if g.Last >= h.First {
					h.Groups = append(h.Groups, g)
				}
			}
		}
	}
	d.Items, d.History = items, h
	return d
}

func (r *HistoryRead) observe(e domain.Event) {
	if e.Type == "item.upsert" && e.Item != nil {
		if r.items == nil {
			r.items = map[string]int{}
		}
		if at, ok := r.items[e.Item.ID]; ok {
			r.Events[at] = e
			return
		}
		if len(r.Events) < 2048 {
			r.items[e.Item.ID] = len(r.Events)
		}
	}
	if len(r.Events) >= 2048 {
		r.Overflow = true
		return
	}
	r.Events = append(r.Events, e)
}

// History paging owns a separate authenticated connection. Slow old-history
// reads must not hold the command client's lock ahead of Stop or steering.
func (c *Controller) startHistoryRead(parent context.Context, current *protocol.Client, job Job) {
	if current == nil || job.epoch != current.Epoch || current.Minor < 37 {
		if job.Apply != nil {
			c.postContext(parent, func(m *Model) {
				job.Apply(m, nil, &protocol.RemoteError{Code: "notSent", Message: "设备未连接或不支持历史分页"})
			})
		}
		return
	}
	device := current.DeviceID
	dial := c.dial
	if c.paths != nil {
		route := c.paths.active()
		dial = func(ctx context.Context) (net.Conn, error) { return c.paths.dialRoute(ctx, route) }
	}
	go func() {
		ctx, cancel := context.WithCancel(parent)
		defer cancel()
		if job.Context != nil {
			stop := context.AfterFunc(job.Context, cancel)
			defer stop()
			if job.Context.Err() != nil {
				cancel()
			}
		}
		var out json.RawMessage
		dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
		// The handshake deadline must not own the SSH process. Keep the
		// transport alive for this read, including every round-window chunk.
		peer, err := protocol.Dial(dialCtx, func(_ context.Context) (net.Conn, error) { return dial(ctx) }, device)
		dialCancel()
		if err == nil {
			defer peer.Close()
			if q, ok := job.Input.(domain.HistoryQuery); ok && job.Method == "session.history" && q.Rounds > 0 && !q.SummaryOnly {
				var detail domain.SessionDetail
				detail, err = readRoundHistory(ctx, q, func(ctx context.Context, query domain.HistoryQuery) (domain.SessionDetail, error) {
					rpc, done := context.WithTimeout(ctx, 10*time.Second)
					defer done()
					var page domain.SessionDetail
					e := peer.Call(rpc, job.Method, query, &page)
					return page, e
				})
				if err == nil {
					out, err = json.Marshal(detail)
				}
			} else if q, ok := job.Input.(domain.WorkHistoryQuery); ok && job.Method == "session.work.history" {
				var detail domain.WorkDetail
				if peer.Minor < 41 {
					err = errors.New("更新设备执行服务后才能加载子 Agent 历史")
				} else {
					detail, err = readWorkHistory(ctx, q, func(ctx context.Context, query domain.WorkHistoryQuery) (domain.WorkDetail, error) {
						rpc, done := context.WithTimeout(ctx, 10*time.Second)
						defer done()
						var page domain.WorkDetail
						e := peer.Call(rpc, job.Method, query, &page)
						return page, e
					})
				}
				if err == nil {
					out, err = json.Marshal(detail)
				}
			} else {
				rpc, done := context.WithTimeout(ctx, 10*time.Second)
				err = peer.Call(rpc, job.Method, job.Input, &out)
				done()
			}
		}
		if job.Apply != nil {
			c.postContext(parent, func(m *Model) { job.Apply(m, out, err) })
		}
	}()
}

func historyToolIssue(item domain.Item) int {
	if item.Final && (item.ExitCode != nil && *item.ExitCode != 0 || item.Status == "failed" || item.Status == "declined" || item.Status == "interrupted") {
		return 1
	}
	return 0
}

func historyAcceptsItem(d domain.SessionDetail, item domain.Item) bool {
	h := d.History
	if h == nil {
		return true
	}
	for _, old := range d.Items {
		if old.ID == item.ID {
			if item.Kind == "tool" {
				for i := range h.Groups {
					g := &h.Groups[i]
					if item.Order >= g.First && item.Order <= g.Last && item.TurnID == g.TurnID {
						g.Issues += historyToolIssue(item) - historyToolIssue(old)
					}
				}
			}
			return true
		}
	}
	if item.Order < h.First || h.Newer && item.Order > h.Last {
		return false
	}
	if item.Order > h.Last {
		if item.Kind == "tool" {
			for i := range h.Groups {
				g := &h.Groups[i]
				if g.TurnID != item.TurnID {
					continue
				}
				continues := true
				for _, old := range d.Items {
					if old.Order > g.Last && (old.TurnID != item.TurnID || !domain.HistorySpacer(old)) {
						continues = false
						break
					}
				}
				if continues {
					g.Last = item.Order
					g.Count++
					g.Issues += historyToolIssue(item)
				}
			}
		}
		if h.First == 0 {
			h.First = item.Order
		}
		h.Last = item.Order
	}
	return true
}

func (m *Model) BeginHistoryRead(id string) *HistoryRead {
	r := &HistoryRead{Pending: true}
	m.HistoryReads[id] = r
	return r
}

// MergeHistoryPage changes only display history. Worker execution and the
// authoritative global event cursor remain independent of a paging request.
func (m *Model) MergeHistoryPage(page domain.SessionDetail, r *HistoryRead, replace bool, query domain.HistoryQuery) error {
	id := page.Session.ID
	if r == nil || m.HistoryReads[id] != r {
		return errors.New("历史读取已取消")
	}
	delete(m.HistoryReads, id)
	r.Pending = false
	if r.Overflow {
		return errors.New("历史更新过快，请重试加载")
	}
	current := m.Details[id]
	if page.History == nil || current.Session.ID != "" && current.Session.ThreadID != page.Session.ThreadID && (query.ThreadID != "" || current.Session.Revision > page.Session.Revision) {
		return errors.New("会话历史已改变，请重试")
	}
	p := NewModel()
	p.Sequence = page.Sequence
	p.Details[id] = page
	events := slices.Clone(r.Events)
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	for _, e := range events {
		if e.Type == "history.reset" && e.Sequence > page.Sequence {
			return errors.New("会话历史已改变，请重试")
		}
		if e.Sequence <= page.Sequence {
			continue
		}
		p.Sequence = e.Sequence - 1
		if err := p.ApplyEvent(e); err != nil {
			return err
		}
	}
	page = p.Details[id]
	if !replace && current.History != nil {
		a, b := page.History, current.History
		if query.Before > 0 && query.Before != b.First || query.After > 0 && query.After != b.Last || query.Before == 0 && query.After == 0 {
			return errors.New("历史窗口不连续，请重试")
		}
		if a.First == 0 {
			copy := *b
			b = &copy
			current.History = b
			if query.Before > 0 {
				b.Older = false
			} else {
				b.Newer = false
			}
			m.Details[id] = current
			return nil
		}
		h := *b
		h.LiveSequence = max(a.LiveSequence, b.LiveSequence)
		if a.First < b.First {
			h.First, h.Older, h.ContextID = a.First, a.Older, a.ContextID
		}
		if a.Last > b.Last {
			h.Last, h.Newer = a.Last, a.Newer
		}
		items := map[string]domain.Item{}
		for _, list := range [][]domain.Item{current.Items, page.Items} {
			for _, item := range list {
				if item.ID == h.ContextID || item.Order >= h.First && item.Order <= h.Last {
					items[item.ID] = item
				}
			}
		}
		page.Items = nil
		for _, item := range items {
			page.Items = append(page.Items, item)
		}
		sort.Slice(page.Items, func(i, j int) bool { return page.Items[i].Order < page.Items[j].Order })
		groups := map[string]domain.HistoryToolGroup{}
		for _, list := range [][]domain.HistoryToolGroup{b.Groups, a.Groups} {
			for _, g := range list {
				groups[g.ID] = g
			}
		}
		h.Groups = nil
		for _, g := range groups {
			h.Groups = append(h.Groups, g)
		}
		page.History = &h
		page.Turns = mergeHistoryTurns(current.Turns, page.Turns)
		requests := map[string]domain.PendingRequest{}
		for _, list := range [][]domain.PendingRequest{current.Requests, page.Requests} {
			for _, v := range list {
				requests[v.ID] = v
			}
		}
		page.Requests = nil
		for _, v := range requests {
			page.Requests = append(page.Requests, v)
		}
	}
	if current.Session.Revision > page.Session.Revision {
		page.Session = current.Session
		page.Plan = current.Plan
		page.Works = current.Works
		page.WorksTruncated = current.WorksTruncated
	}
	if current.Queue.Revision > page.Queue.Revision {
		page.Queue = current.Queue
	}
	m.Details[id] = page
	return nil
}

func mergeHistoryTurns(old, next []domain.Turn) []domain.Turn {
	out := slices.Clone(old)
	for _, v := range next {
		at := slices.IndexFunc(out, func(t domain.Turn) bool { return t.ID == v.ID })
		if at < 0 {
			out = append(out, v)
		} else {
			out[at] = v
		}
	}
	return out
}
