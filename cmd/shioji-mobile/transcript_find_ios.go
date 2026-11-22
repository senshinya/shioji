package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

const (
	inConversationFind = "在会话中查找"
	inParallelFind     = "在右栏会话中查找"
)

type mobileFindLocation struct {
	app.SessionLocation
	thread string
}

type mobileConversationFind struct {
	model      *app.Model
	owner      *mygo.Window
	source     domain.Session
	location   mobileFindLocation
	generation string
	right      bool
	query      string
	focus      bool
	run        *transcriptFindRun
}

type transcriptFindRun struct {
	term, epoch   string
	remote        bool
	revision      int64
	ready         time.Time
	ctx           context.Context
	cancel        context.CancelFunc
	stopRead      func()
	busy, more    bool
	after         int64
	indexRevision int64
	matches       []domain.HistoryMatch
	total         int
	current       int
	item          string
	occurrence    int
	message       string
	readError     string
	mark          *transcriptFindMark
}

type transcriptFindMark struct {
	item       string
	text       string
	start, end int
	reveal     bool
	aligned    bool
	row        int
}

func (a *mobileApp) findLocation(s domain.Session) mobileFindLocation {
	return mobileFindLocation{app.SessionLocation{DeviceID: a.model.Snapshot.DeviceID,
		WorkspaceID: s.WorkspaceID, SessionID: s.ID}, s.ThreadID}
}

func (a *mobileApp) findVisible(owner *mygo.Window, sid string, right bool) bool {
	if sid == "" || !a.pageCurrent(owner) {
		return false
	}
	if !right {
		return a.model.SessionID == sid
	}
	root := a.splitRoot()
	if owner != root.detail || !root.wide || root.review != nil && !root.reviewBelongsToParallel(root.review) ||
		root.model.SessionID == "" {
		return false
	}
	if a.parallelOwner != nil {
		return a.model.SessionID == sid
	}
	return root.parallelDevice == nil && a.parallel == sid && a.model.SessionID != sid
}

func (a *mobileApp) findSlot(right bool) **mobileConversationFind {
	if right {
		return &a.parallelFind
	}
	return &a.find
}

func (a *mobileApp) openConversationFind(owner *mygo.Window, source domain.Session, right bool) {
	if !a.findVisible(owner, source.ID, right) {
		return
	}
	slot := a.findSlot(right)
	if f := *slot; f != nil && f.matchesScope(a, owner, source) {
		f.focus = true
	} else {
		closeFind(slot)
		*slot = a.newConversationFind(owner, source, right)
		(*slot).focus = true
	}
	a.refresh()
}

func (a *mobileApp) openConversationFindFromActions(page, parent *mygo.Window, sid string) {
	if !a.pageCurrent(page) || a.sessionByID(sid).ID == "" {
		return
	}
	if a.wide && a.detail != nil && a.chat == nil {
		a.openSession(sid)
		a.openConversationFind(a.detail, a.session(), false)
		if parent == a.home {
			// Actions opened from the sidebar are on its own navigation stack.
			a.callUIKit("showPage", map[string]uintptr{"window": a.home.NativeHandle()}, nil)
		}
		a.callUIKit("showPage", map[string]uintptr{"window": a.detail.NativeHandle()}, nil)
		return
	}
	a.openSessionFrom(page, sid)
	if a.chat != nil {
		a.openConversationFind(a.chat, a.session(), false)
	}
}

func (a *mobileApp) newConversationFind(owner *mygo.Window, source domain.Session, right bool) *mobileConversationFind {
	return &mobileConversationFind{owner: owner, model: a.model, source: source, location: a.findLocation(source),
		generation: a.model.Snapshot.Generation, right: right}
}

func (a *mobileApp) closeConversationFind() { closeFind(&a.find) }

func (a *mobileApp) takeFindShortcut(owner *mygo.Window) bool {
	if owner == nil || a.findShortcutPage != owner {
		return false
	}
	a.findShortcutPage = nil
	return a.pageCurrent(owner)
}

func closeFind(slot **mobileConversationFind) {
	if f := *slot; f != nil {
		f.stop()
		*slot = nil
	}
}

func (a *mobileApp) suspendParallelFind() {
	if f := a.parallelFind; f != nil {
		root := a.splitRoot()
		if root.parallelFindQueries == nil {
			root.parallelFindQueries = make(map[mobileFindLocation]string)
		}
		root.parallelFindQueries[f.location] = f.query
		closeFind(&a.parallelFind)
	}
}

func (a *mobileApp) suspendRightPaneFind() {
	if pane := a.parallelDevice; pane != nil {
		pane.suspendParallelFind()
	} else {
		a.suspendParallelFind()
	}
}

func (f *mobileConversationFind) stop() {
	if r := f.run; r != nil {
		r.cancel()
		if r.stopRead != nil {
			r.stopRead()
		}
	}
}

func (f *mobileConversationFind) matchesScope(a *mobileApp, owner *mygo.Window, source domain.Session) bool {
	return f.owner == owner && a.findVisible(owner, source.ID, f.right) && f.model == a.model &&
		f.generation == a.model.Snapshot.Generation && a.findLocation(source) == f.location
}

func (a *mobileApp) conversationFind(c *ui.Context, parent *mygo.Window, source domain.Session, detail domain.SessionDetail,
	right bool, shortcut func(ui.Modifiers, ui.Key) bool) *mobileConversationFind {
	slot := a.findSlot(right)
	f := *slot
	if f != nil && !f.matchesScope(a, parent, source) {
		closeFind(slot)
		f = nil
	}
	if f == nil && right && a.findVisible(parent, source.ID, true) {
		root := a.splitRoot()
		key := a.findLocation(source)
		if query, ok := root.parallelFindQueries[key]; ok {
			delete(root.parallelFindQueries, key)
			f = a.newConversationFind(parent, source, true)
			f.query = query
			*slot = f
		}
	}
	// A focused MyGo surface can receive the hardware key before UIKit's command.
	if shortcut(ui.Cmd, ui.KeyF) && (f == nil || !f.focus) {
		a.openConversationFind(parent, source, right)
		f = *slot
	}
	if f == nil {
		return nil
	}
	closePanel := func() { closeFind(slot) }
	fieldLabel, closeLabel, buttonPrefix := inConversationFind, "关闭会话查找", ""
	if right {
		fieldLabel, closeLabel, buttonPrefix = inParallelFind, "关闭右栏查找", "右栏"
	}
	f.refresh(c, a, source.Revision, detail)
	p := palette(c)
	r := f.run
	showCount := r.current >= 0
	controls := mobileContent(c.Key("find"), contentMaxWidth).Padding(4, pageInset, 2).Gap(4).Transition(mobilePanelTransition)
	controls.Children(func() {
		row := ui.Row(c.Key("find-field")).FillWidth().MinHeight(mobileTapSize(c)).Padding(0, 1, 0, 10).
			Radius(cardRadius).Background(p.inset).AlignItems(ui.Center).Gap(4)
		row.Children(func() {
			ui.Icon(c, searchSymbol).Size(16, 16).TextColor(p.secondary).Shrink(0).PassThrough()
			field := ui.TextInputBase(c.Key("find-query"), &f.query).Label(fieldLabel).Placeholder("查找…").
				Grow(1).MinWidth(0).Height(mobileTapSize(c)).PaddingX(0).FontSize(controlTextSize * c.Preferences().TextScale).
				TextColor(p.ink).OnTextKey(func(key ui.TextKey) bool {
				switch key.Key {
				case ui.KeyEnter:
					if strings.TrimSpace(key.Text()) == r.term {
						step := 1
						if key.Mods&ui.Shift != 0 {
							step = -1
						}
						f.step(a, step)
					}
					return true
				case ui.KeyEscape:
					closePanel()
					return true
				}
				return false
			})
			if f.focus {
				field.Focus(ui.FocusOptions{PreventScroll: true})
				f.focus = false
			}
			if showCount {
				total := fmt.Sprint(r.total)
				if r.more || r.busy {
					total += "+"
				}
				count := fmt.Sprintf("%d / %s", r.current+1, total)
				oneLine(c.Key("match-count"), count, captionTextSize, p.secondary).Shrink(0)
			}
			for _, delta := range []int{-1, 1} {
				title, icon := "上一处匹配", upSymbol
				if delta > 0 {
					title, icon = "下一处匹配", downSymbol
				}
				index := r.current + delta
				disabled := !showCount || r.more && (index < 0 || index >= r.total)
				iconButton(c.Key(delta), buttonPrefix+title, icon, 15, func() { f.step(a, delta) }).KeepFocus().Disabled(disabled)
			}
			iconButton(c.Key("close-find"), closeLabel, closeSymbol, 15, closePanel)
		})
		caption := r.message
		switch {
		case r.readError != "":
			caption = r.readError
		case !r.ready.IsZero() || (r.more || r.busy) && !showCount:
			caption = "正在查找…"
		case r.stopRead != nil:
			caption = "正在读取匹配消息…"
		case caption == "" && r.term != "" && !showCount:
			caption = "没有匹配"
		}
		if r.term != "" && !r.remote && !a.preview && detail.History != nil && (detail.History.Older || detail.History.Newer) {
			if caption != "" {
				caption += " · "
			}
			caption += "仅查找已加载的消息"
		}
		if caption != "" {
			ui.Row(c.Key("find-status")).MinHeight(20).PaddingX(10).Gap(6).AlignItems(ui.Center).Children(func() {
				label(c, caption, captionTextSize, p.secondary).Grow(1).MinWidth(0)
				if r.message != "" && r.remote || r.readError != "" && r.item != "" {
					action(c, "重试查找", func() {
						if r.readError != "" {
							f.selectMatch(a, r.current)
						} else {
							f.stop()
							f.run = nil
						}
					}).KeepFocus()
				}
			})
		}
	})
	if shortcut(ui.Cmd, ui.KeyG) {
		f.step(a, 1)
	}
	if shortcut(ui.Cmd|ui.Shift, ui.KeyG) {
		f.step(a, -1)
	}
	return f
}

func (f *mobileConversationFind) refresh(c *ui.Context, a *mobileApp, revision int64, detail domain.SessionDetail) {
	term := strings.TrimSpace(f.query)
	remote := a.model.Connected && a.model.WorkerMinor >= 37 && a.controller != nil
	r := f.run
	sameQuery := r != nil && r.term == term && r.remote == remote && r.epoch == a.model.ConnectionEpoch
	changedHistory := sameQuery && r.revision != revision && !r.busy && !r.more && r.message == "" && r.stopRead == nil
	if !sameQuery || changedHistory {
		f.stop()
		ctx, cancel := context.WithCancel(context.Background())
		old := r
		r = &transcriptFindRun{term: term, epoch: a.model.ConnectionEpoch, remote: remote, revision: revision,
			ctx: ctx, cancel: cancel, current: -1}
		if changedHistory {
			r.item, r.occurrence, r.mark = old.item, old.occurrence, old.mark
		}
		f.run = r
		switch {
		case term == "":
		case len(term) > 256 || !utf8.ValidString(term):
			r.message = "查找内容最多 256 字节"
		default:
			r.ready = c.Now().Add(160 * time.Millisecond)
		}
	}
	if !r.ready.IsZero() {
		if wait := r.ready.Sub(c.Now()); wait > 0 {
			c.After(wait)
			return
		}
		r.ready = time.Time{}
		if !r.remote {
			if a.previewFind {
				detail.Items = append(previewFindWindow(detail, "older-reply").Items, previewFindWindow(detail, "").Items...)
			}
			r.matches, r.total = findLoadedMessages(detail, term)
			f.selectInitialMatch(a, r)
		} else {
			r.more = true
		}
	}
	if r.more && !r.busy && r.message == "" {
		f.readMatches(a, r)
	}
}

func findLoadedMessages(detail domain.SessionDetail, term string) ([]domain.HistoryMatch, int) {
	var matches []domain.HistoryMatch
	total := 0
	needle := strings.ToLower(term)
	for _, item := range mobileclient.ConversationItems(detail) {
		switch item.Kind {
		case "user", "assistant", "tool", "activity", "reasoning":
			if count := strings.Count(strings.ToLower(item.Text), needle); count > 0 {
				matches = append(matches, domain.HistoryMatch{ID: item.ID, Order: item.Order, Count: count})
				total += count
			}
		}
	}
	return matches, total
}

func (f *mobileConversationFind) readMatches(a *mobileApp, r *transcriptFindRun) {
	q := domain.HistorySearchQuery{HistoryQuery: domain.HistoryQuery{SessionID: f.source.ID, ThreadID: f.source.ThreadID,
		Generation: f.generation, After: r.after}, Query: r.term}
	r.busy = true
	if a.controller.Do(app.Job{Context: r.ctx, ConnectionEpoch: r.epoch, Method: "session.matches", Input: q,
		Apply: func(m *app.Model, data json.RawMessage, err error) {
			if !f.current(a, r) {
				return
			}
			r.busy = false
			var page domain.HistoryMatches
			if err == nil {
				err = json.Unmarshal(data, &page)
			}
			if err == nil && (page.SessionID != q.SessionID || page.ThreadID != q.ThreadID || page.Query != q.Query) {
				err = errors.New("查找结果与会话不匹配")
			}
			if err == nil && page.More && len(page.Matches) == 0 {
				err = errors.New("查找结果未继续推进")
			}
			if err == nil && r.after > 0 && page.Revision != r.indexRevision {
				r.busy, r.more, r.revision = false, false, -1
				f.owner.Invalidate()
				return
			}
			after := r.after
			for _, match := range page.Matches {
				if match.Order <= after || match.Count < 1 || match.ID == "" {
					err = errors.New("查找结果未继续推进")
					break
				}
				after = match.Order
			}
			if err != nil {
				r.more, r.message = false, err.Error()
				f.owner.Invalidate()
				return
			}
			r.after, r.more, r.indexRevision = after, page.More, page.Revision
			detail := m.Details[q.SessionID]
			hidden := hiddenFindItems(detail)
			for _, match := range page.Matches {
				if !hidden[match.ID] {
					r.matches = append(r.matches, match)
					r.total += match.Count
				}
			}
			f.selectInitialMatch(a, r)
			f.owner.Invalidate()
		},
	}) {
		return
	}
	r.busy, r.more, r.message = false, false, "设备忙，请重试查找"
	f.owner.Invalidate()
}

func hiddenFindItems(detail domain.SessionDetail) map[string]bool {
	hidden := make(map[string]bool)
	visible := make(map[string]bool)
	for _, item := range mobileclient.ConversationItems(detail) {
		visible[item.ID] = true
	}
	for _, item := range detail.Items {
		if !visible[item.ID] {
			hidden[item.ID] = true
		}
	}
	for _, work := range detail.Works {
		if work.SourceItemID != "" {
			hidden[work.SourceItemID] = true
		}
	}
	return hidden
}

func (f *mobileConversationFind) current(a *mobileApp, r *transcriptFindRun) bool {
	return r != nil && *a.findSlot(f.right) == f && f.run == r && r.ctx.Err() == nil &&
		r.term == strings.TrimSpace(f.query) && r.epoch == a.model.ConnectionEpoch &&
		f.matchesScope(a, f.owner, a.sessionByID(f.source.ID))
}

func (f *mobileConversationFind) step(a *mobileApp, delta int) {
	r := f.run
	if r == nil || r.current < 0 {
		return
	}
	index := r.current + delta
	if r.more && (index < 0 || index >= r.total) {
		return
	}
	f.selectMatch(a, (index+r.total)%r.total)
}

func (f *mobileConversationFind) selectInitialMatch(a *mobileApp, r *transcriptFindRun) {
	if r.current >= 0 {
		return
	}
	offset := 0
	for _, entry := range r.matches {
		if entry.ID == r.item {
			f.selectMatch(a, offset+min(r.occurrence, entry.Count-1))
			return
		}
		offset += entry.Count
	}
	if r.total > 0 && (r.item == "" || !r.more) {
		f.selectMatch(a, 0)
	} else if !r.more {
		r.mark = nil
	}
}

func (f *mobileConversationFind) selectMatch(a *mobileApp, index int) {
	r := f.run
	if !f.current(a, r) || index < 0 || index >= r.total {
		return
	}
	if r.stopRead != nil {
		r.stopRead()
		r.stopRead = nil
	}
	r.current, r.readError = index, ""
	for i, entry := range r.matches {
		if index < entry.Count {
			f.locateMatch(a, r, i, index)
			return
		}
		index -= entry.Count
	}
}

func (f *mobileConversationFind) locateMatch(a *mobileApp, r *transcriptFindRun, index, occurrence int) {
	entry := r.matches[index]
	r.item, r.occurrence = entry.ID, occurrence
	locate := func() {
		if !f.current(a, r) || r.item != entry.ID {
			return
		}
		items := mobileclient.ConversationItems(a.model.Details[f.source.ID])
		if i := slices.IndexFunc(items, func(item domain.Item) bool { return item.ID == entry.ID }); i >= 0 {
			start, end := domain.FindLiteral(items[i].Text, r.term, occurrence)
			if r.mark == nil || r.mark.item != entry.ID || r.mark.text != items[i].Text || r.mark.start != start {
				r.mark = &transcriptFindMark{item: entry.ID, text: items[i].Text, start: start, end: end, reveal: true}
			}
			if start < 0 {
				r.readError = "匹配位置超出当前可读内容"
			}
		}
	}
	detail := a.model.Details[f.source.ID]
	if slices.ContainsFunc(mobileclient.ConversationItems(detail), func(item domain.Item) bool { return item.ID == entry.ID }) {
		locate()
		return
	}
	r.mark = nil
	detail.Session = a.sessionByID(f.source.ID)
	current := func() bool { return f.current(a, r) && r.item == entry.ID }
	r.stopRead = a.beginTranscriptHistory(f.source.ID, detail, domain.HistoryQuery{AroundID: entry.ID}, f.owner, current, func(err error) {
		r.stopRead = nil
		if !f.current(a, r) {
			return
		}
		switch {
		case err == errTranscriptItemHidden:
			current := r.current - occurrence
			r.matches = slices.Delete(r.matches, index, index+1)
			r.total -= entry.Count
			r.current = -1
			if r.total != 0 {
				f.selectMatch(a, min(current, r.total-1))
			}
		case err != nil:
			r.readError = err.Error()
		default:
			locate()
		}
	})
	if r.stopRead == nil && r.mark == nil && r.current >= 0 && r.readError == "" {
		r.readError = "暂时无法读取匹配消息"
	}
}

func (f *mobileConversationFind) decoration(c *ui.Context, item domain.Item, state *ui.ListState, bounds func() ui.Rect) *markdown.RangeDecoration {
	mark := f.run.mark
	if mark == nil || item.ID != mark.item || mark.text != item.Text || mark.start < 0 {
		return nil
	}
	highlight := ui.RGBA(255, 212, 56, .58)
	if c.Theme().Dark {
		highlight = ui.RGBA(255, 201, 38, .37)
	}
	return &markdown.RangeDecoration{Start: mark.start, End: mark.end, Draw: func(p *ui.Painter, box ui.Rect) {
		if f.run == nil || f.run.mark != mark {
			return
		}
		if !mark.aligned {
			viewport, row := state.Bounds(c), bounds()
			if viewport.H <= 0 || row.H <= 0 {
				return
			}
			mark.aligned = true
			if box.Y < viewport.Y+4 || box.Y+box.H > viewport.Y+viewport.H-4 {
				y := max(8, viewport.H*.25) - (box.Y - row.Y)
				state.ScrollToRowWithinContent(mark.row, y, mobilePositionMotion(c))
				f.owner.Invalidate()
			}
		}
		p.Fill(ui.Rect{X: box.X - 1, Y: box.Y, W: box.W + 2, H: box.H}, highlight, 3)
	}}
}

func decorateTranscriptText(c *ui.Context, el ui.Element, text string, mark *markdown.RangeDecoration, displayOffset func(int, bool) int) {
	if mark == nil || mark.Start < 0 || mark.End > len(text) {
		return
	}
	start, end := utf8.RuneCountInString(text[:mark.Start]), utf8.RuneCountInString(text[:mark.End])
	if displayOffset != nil {
		start, end = displayOffset(start, false), displayOffset(end, true)
	}
	handle := ui.Local(el, "find-text-handle", func() ui.Handle { return ui.Handle{} })
	el.Bind(handle)
	el.Draw(func(p *ui.Painter, _ ui.Rect) {
		for _, box := range c.Resolve(*handle).TextRangeRects(start, end) {
			mark.Draw(p, box)
		}
	})
}
