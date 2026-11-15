package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"time"
	"unicode/utf8"
)

type historyFind struct {
	session, epoch, source, query, connection string
	loading, done                             bool
	after, revision                           int64
	matches                                   []domain.HistoryMatch
	occurrence                                int
	started                                   time.Time
	cancel                                    context.CancelFunc
	error                                     string
}

func (v *View) refreshHistoryFind() bool {
	s := v.findState()
	m := v.Model
	d := m.Details[m.SessionID]
	if s.mode != findLocal || d.History == nil || m.WorkerMinor < 37 || v.Controller == nil {
		if s.remote != nil && s.remote.cancel != nil {
			s.remote.cancel()
		}
		s.remote = nil
		return false
	}
	query := strings.TrimSpace(s.query)
	changed := s.remote == nil || s.remote.session != m.SessionID || s.remote.epoch != m.ConnectionEpoch || s.remote.source != d.Session.ThreadID || s.remote.query != query
	if changed || s.remote.done && d.Session.Revision > s.remote.revision && v.frameTime.Sub(s.remote.started) > 500*time.Millisecond {
		if s.remote != nil && s.remote.cancel != nil {
			s.remote.cancel()
		}
		s.remote = &historyFind{session: m.SessionID, epoch: m.ConnectionEpoch, source: d.Session.ThreadID, query: query, started: v.frameTime, connection: m.ConnectionID()}
		if changed {
			s.entries = nil
			s.total = 0
			s.current = -1
			s.target = findTarget{}
			s.placement = findNeedsGeometry
		}
		s.compiled = query
		s.needle = strings.ToLower(query)
	}
	r := s.remote
	if query == "" || !utf8.ValidString(query) || len(query) > 256 {
		r.done = true
		s.total = 0
		s.current = -1
		s.entries = nil
		s.target = findTarget{}
		return true
	}
	if !r.loading && !r.done && r.error == "" && m.Connected {
		v.readHistoryMatches(r)
	}
	loaded := map[string]string{}
	for _, item := range d.Items {
		loaded[item.ID] = item.Text
	}
	for i := range s.entries {
		e := &s.entries[i]
		e.text = loaded[e.id]
		if e.id == s.target.id && e.text != "" && s.placement == findAwaitingHistory {
			start, end := domain.FindLiteral(e.text, s.compiled, r.occurrence)
			if start >= 0 {
				s.target = findTarget{id: e.id, start: start, end: end}
				s.rearm()
			} else if strings.Contains(e.text, "[Output too large for a history page;") {
				r.error = "匹配位于过长输出的预览范围之外"
				s.placement = findNeedsGeometry
			}
		}
	}
	return true
}

func (v *View) readHistoryMatches(r *historyFind) {
	s := v.findState()
	q := domain.HistorySearchQuery{HistoryQuery: domain.HistoryQuery{SessionID: r.session, ThreadID: r.source, After: r.after, Generation: v.Model.Snapshot.Generation}, Query: r.query}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.loading = true
	if !v.do(app.Job{Context: ctx, Method: "session.matches", Input: q, Apply: func(m *app.Model, b json.RawMessage, err error) {
		defer cancel()
		if s.remote != r || ctx.Err() != nil || strings.TrimSpace(s.query) != r.query || s.mode != findLocal || historyConnectionEpoch(m, r.connection) != r.epoch || m.Details[r.session].Session.ThreadID != r.source {
			return
		}
		r.loading = false
		r.cancel = nil
		var page domain.HistoryMatches
		if err == nil && (json.Unmarshal(b, &page) != nil || page.SessionID != r.session || page.ThreadID != r.source || page.Query != r.query) {
			err = fmt.Errorf("查找结果与会话不匹配")
		}
		if err != nil {
			r.error = err.Error()
			return
		}
		if r.after == 0 {
			r.revision = page.Revision
		}
		for _, match := range page.Matches {
			if match.Order <= r.after || match.Count < 1 {
				r.error = "查找结果没有推进"
				return
			}
			r.after = match.Order
			r.matches = append(r.matches, match)
		}
		r.done = !page.More
		if page.More && len(page.Matches) == 0 {
			r.error = "查找结果没有推进"
			return
		}
		oldTarget := s.target
		loaded := map[string]string{}
		for _, item := range m.Details[r.session].Items {
			loaded[item.ID] = item.Text
		}
		s.entries = nil
		s.total = 0
		wanted := -1
		for _, match := range r.matches {
			e := findEntry{id: match.ID, count: match.Count, text: loaded[match.ID]}
			if match.ID == oldTarget.id {
				wanted = s.total + min(r.occurrence, match.Count-1)
			}
			s.entries = append(s.entries, e)
			s.total += match.Count
		}
		if s.total == 0 {
			s.current = -1
			s.target = findTarget{}
			return
		}
		if wanted < 0 {
			wanted = 0
		}
		v.selectFindMatch(wanted)
	}}) {
		cancel()
		r.loading = false
		r.error = "设备忙，请重试查找"
	}
}
