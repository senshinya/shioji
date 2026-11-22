package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/domain"
)

type replyHistoryScope struct {
	session, thread, connection string
	snapshot                    int64
	activation                  uint64
}

// Keep a high-water mark per visible history window. A virtualized row may be
// new to the view even when it was already present in the worker's history.
type replyTail struct {
	scope                replyHistoryScope
	frame                time.Time
	order, previousOrder int64
	enabled, continuous  bool
	replyID, turnID      string
	drawnAt              time.Time
	revealing            bool
	stream               replyStream
}

func (s *replyTail) observe(now time.Time, scope replyHistoryScope, order int64, item domain.Item, enabled bool) {
	continuous := enabled && s.enabled && s.scope == scope
	if !continuous || s.frame != now {
		s.previousOrder, s.continuous = s.order, continuous
	}
	mounted := item.ID == s.replyID && !s.drawnAt.IsZero() && (s.drawnAt == s.frame || s.drawnAt == now)
	if !mounted {
		s.stream, s.revealing, s.drawnAt = replyStream{}, false, time.Time{}
	}
	s.replyID, s.turnID = item.ID, item.TurnID
	added := s.stream.prepare(now, item, s.continuous && item.ID != "", mounted, item.Order > s.previousOrder)
	s.revealing = s.stream.live && (added || s.revealing)
	s.scope, s.frame, s.order, s.enabled = scope, now, order, enabled
}

func (s *replyTail) holdReceipt(c *ui.Context, turnID string) bool {
	if !s.revealing || s.turnID != turnID {
		return false
	}
	if s.drawnAt != c.Now() {
		// The footer can be laid out before its virtual reply. Recheck once
		// if the reply is outside the viewport and never reports a reveal.
		c.AnimationFrame()
	}
	return true
}

func (s *replyTail) draw(c *ui.Context, item domain.Item, options *markdown.Options) {
	var stream replyStream
	if item.ID == s.replyID {
		s.drawnAt = c.Now()
		stream = s.stream
		options.OnRevealState = func(active bool) {
			if s.revealing != active {
				s.revealing = active
				c.AnimationFrame()
			}
		}
	}
	stream.draw(c, item, options)
}

// Markdown owns pacing, selection and the reveal buffer. Prepare the latest
// reply before layout so its completion row works in either build order.
type replyStream struct {
	live       bool
	previous   string
	appendedAt time.Time
}

func (s *replyStream) prepare(now time.Time, item domain.Item, live, mounted, fresh bool) bool {
	changed := item.Text != s.previous
	switch {
	case !live:
		s.live = false
	case !mounted:
		s.live = fresh
	case changed:
		s.live = strings.HasPrefix(item.Text, s.previous)
	}
	s.previous = item.Text
	if s.live && changed {
		s.appendedAt = now
		return item.Text != ""
	}
	return false
}

func (s replyStream) draw(c *ui.Context, item domain.Item, options *markdown.Options) {
	if item.Text == "" {
		return
	}
	if !s.live {
		options.Reveal = &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}
	} else {
		options.Reveal = &markdown.Reveal{Unit: markdown.ByCharacter, Fade: mobileReplyFade, Smooth: true}
	}
	if s.live && (!item.Final || s.appendedAt == c.Now()) {
		markdown.Stream(c, item.Text, options)
	} else {
		markdown.View(c, item.Text, options)
	}
}
