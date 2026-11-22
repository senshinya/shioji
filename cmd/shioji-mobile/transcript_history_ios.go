package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type transcriptEdge uint8

const (
	transcriptOlder transcriptEdge = iota
	transcriptNewer
)

type transcriptTurnKey string
type transcriptReplyKey string

var errTranscriptItemHidden = errors.New("此条记录不在会话正文中")

type transcriptPrefetch struct {
	direction float32
	inputAt   time.Time
	edge      transcriptEdge
	cancel    func()
}

func (p *transcriptPrefetch) stop() {
	p.direction = 0
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

func (a *mobileApp) prefetchTranscript(c *ui.Context, list ui.Element, state *ui.ListState, p *transcriptPrefetch,
	parent *mygo.Window, detail domain.SessionDetail, current func() bool, allow bool) {
	list.OnUnmount(p.stop)
	h := detail.History
	if !allow || h == nil || h.Invalidated || (!h.Older && !h.Newer) || !a.model.Connected || (!a.preview && a.model.WorkerMinor < 37) {
		p.stop()
		return
	}
	sid := detail.Session.ID
	list.HandleInput(func(event ui.InputEvent) bool {
		read := a.model.HistoryReads[sid]
		if event.Kind == ui.InputScroll && event.DY != 0 && (read == nil || !read.Pending && read.Error == "") {
			p.direction, p.inputAt = event.DY, event.Time
			parent.Invalidate()
		}
		return false
	})
	before, after := state.ScrollEdges()
	height := state.Bounds(c).H
	read := a.model.HistoryReads[sid]
	if height <= 0 || p.direction == 0 || c.Now().Sub(p.inputAt) > 350*time.Millisecond || read != nil && (read.Pending || read.Error != "") {
		return
	}
	if p.direction < 0 && h.Older && before <= max(240, 2*height) {
		a.startTranscriptPage(p, parent, detail, transcriptOlder, current)
	} else if p.direction > 0 && h.Newer && after <= max(240, .75*height) {
		a.startTranscriptPage(p, parent, detail, transcriptNewer, current)
	}
}

func (a *mobileApp) startTranscriptPage(p *transcriptPrefetch, parent *mygo.Window, detail domain.SessionDetail, edge transcriptEdge, current func() bool) {
	p.edge, p.direction = edge, 0
	var query domain.HistoryQuery
	if !detail.History.Invalidated {
		if edge == transcriptOlder {
			query.Before = detail.History.First
		} else {
			query.After = detail.History.Last
		}
	}
	p.cancel = a.beginTranscriptHistory(detail.Session.ID, detail, query, parent, current, func(error) { p.cancel = nil })
}

func (a *mobileApp) transcriptHistoryControl(c *ui.Context, parent *mygo.Window, detail domain.SessionDetail,
	p *transcriptPrefetch, edge transcriptEdge, current func() bool) {
	sid := detail.Session.ID
	read := a.model.HistoryReads[sid]
	failedEdge := p.edge
	if !detail.History.Older {
		failedEdge = transcriptNewer
	} else if !detail.History.Newer {
		failedEdge = transcriptOlder
	}
	if read == nil || read.Pending || read.Error == "" || edge != failedEdge {
		ui.Spacer(c.Key(edge)).Height(0)
		return
	}
	control := mobileContent(c.Key(edge), contentMaxWidth).Padding(0, pageInset)
	control.Children(func() {
		label(c, read.Error, captionTextSize, palette(c).warning)
		action(c, "重试加载历史", func() { a.startTranscriptPage(p, parent, detail, edge, current) }).
			AlignSelf(ui.Start).PaddingX(0).Disabled(!a.model.Connected)
	})
}

func (a *mobileApp) readTranscriptHistory(sid string, detail domain.SessionDetail, query domain.HistoryQuery) {
	a.requestTranscriptHistory(sid, detail, query, nil, nil)
}

func (a *mobileApp) requestTranscriptHistory(sid string, detail domain.SessionDetail, query domain.HistoryQuery,
	owner *mygo.Window, done func(error)) {
	a.beginTranscriptHistory(sid, detail, query, owner, func() bool { return a.model.SessionID == sid }, done)
}

// A targeted read belongs to the surface that started it. Cancelling the
// read also releases the pending slot so another target can load immediately.
func (a *mobileApp) beginTranscriptHistory(sid string, detail domain.SessionDetail, query domain.HistoryQuery,
	owner *mygo.Window, current func() bool, done func(error)) func() {
	m := a.model
	if !m.Connected || m.HistoryReads[sid] != nil && m.HistoryReads[sid].Pending {
		return nil
	}
	query.SessionID, query.ThreadID = sid, detail.Session.ThreadID
	query.Generation, query.Rounds = m.Snapshot.Generation, m.Preferences.Normalized().HistoryPageRounds
	epoch := m.ConnectionEpoch
	latest := query.Before == 0 && query.After == 0 && query.AroundID == ""
	read := m.BeginHistoryRead(sid)
	ctx := context.Background()
	cleanup := func() {}
	abort := func() {}
	if owner != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		abort = func() {
			cancel()
			if m.HistoryReads[sid] == read {
				delete(m.HistoryReads, sid)
			}
		}
		unregister := owner.OnClosed(abort)
		cleanup = func() { unregister(); cancel() }
		stop := abort
		abort = func() { stop(); unregister() }
	}
	finish := func(page domain.SessionDetail, err error) {
		defer cleanup()
		if m.HistoryReads[sid] != read {
			return
		}
		if owner != nil && (!a.pageCurrent(owner) || !current() || m.Snapshot.Generation != query.Generation || m.ConnectionEpoch != epoch) {
			delete(m.HistoryReads, sid)
			return
		}
		read.Pending = false
		if err == nil {
			if page.Session.ID != sid || page.Session.WorkspaceID != detail.Session.WorkspaceID || page.Session.ThreadID != detail.Session.ThreadID {
				err = errors.New("会话历史已改变，请重试")
			} else if query.AroundID != "" && !slices.ContainsFunc(mobileclient.ConversationItems(page), func(item domain.Item) bool {
				return item.ID == query.AroundID
			}) {
				err = errTranscriptItemHidden
			} else {
				err = m.MergeHistoryPage(page, read, latest || query.AroundID != "", query)
				if err == nil && latest && a.history[sid] != nil {
					a.history[sid].ScrollToEnd()
				}
			}
		}
		if err == errTranscriptItemHidden && owner != nil {
			delete(m.HistoryReads, sid)
		} else if err != nil {
			read.Error = err.Error()
			m.HistoryReads[sid] = read
		}
		if done != nil {
			done(err)
		}
	}
	if a.preview {
		if a.previewFind && (latest || query.AroundID != "" || query.After != 0) {
			finish(previewFindWindow(detail, query.AroundID), nil)
		} else if query.After != 0 {
			finish(previewRecentTranscript(detail), nil)
		} else if latest {
			if detail.History != nil && detail.History.Newer {
				detail = previewRecentTranscript(detail)
			}
			finish(detail, nil)
		} else {
			older := previewOlderTranscript(detail)
			if query.AroundID != "" {
				older.History.Newer = true
			}
			finish(older, nil)
		}
	} else if a.controller == nil || !a.controller.Do(app.Job{Context: ctx, Method: "session.history", Input: query, ConnectionEpoch: epoch,
		Apply: func(current *app.Model, data json.RawMessage, err error) {
			if current != m || a.model != m {
				cleanup()
				return
			}
			var page domain.SessionDetail
			if err == nil {
				err = json.Unmarshal(data, &page)
			}
			finish(page, err)
		},
	}) {
		finish(domain.SessionDetail{}, errors.New("设备忙，请重试加载历史"))
	}
	a.refresh()
	if !read.Pending {
		return nil
	}
	return abort
}
