package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/conversationui"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
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
	frame, previousFrame time.Time
	order, previousOrder int64
	enabled, continuous  bool
	projection           *mobileTranscriptProjection
	nextReveal           time.Time
	arrivals             map[any]time.Time
	streams              map[string]*mobileReplyPresentation
	reasoningFresh       map[string]bool
	receipts             map[string]time.Time
	requests             map[string]bool
}

type mobileReplyPresentation struct {
	stream      replyStream
	turn        string
	drawnAt     time.Time
	revealAfter time.Time
	revealing   bool
}

const mobileReceiptEntrance = 200 * time.Millisecond

func (s *replyTail) observe(now time.Time, scope replyHistoryScope, order int64, items []domain.Item, enabled bool, projection *mobileTranscriptProjection) {
	continuous := enabled && s.enabled && s.scope == scope
	if !continuous || s.frame != now {
		s.previousFrame = s.frame
		s.previousOrder, s.continuous = s.order, continuous
	}
	// A new model revision builds a new projection, even when it updates a
	// journal item in place. Reuse the entries between those model updates.
	if projection != nil && projection == s.projection && enabled == s.enabled && scope == s.scope &&
		(s.nextReveal.IsZero() || now.Before(s.nextReveal)) {
		s.frame = now
		return
	}
	if !continuous {
		clear(s.streams)
		clear(s.reasoningFresh)
		clear(s.arrivals)
		clear(s.receipts)
		clear(s.requests)
	}
	s.projection = projection
	s.nextReveal = time.Time{}
	if s.streams == nil {
		s.streams = make(map[string]*mobileReplyPresentation)
		s.receipts = make(map[string]time.Time)
	}
	if s.reasoningFresh == nil {
		s.reasoningFresh = make(map[string]bool)
	}
	known := make(map[string]bool)
	requests := make(map[string]bool, len(s.requests))
	var receiptTurn string
	var receiptUntil time.Time
	for _, item := range items {
		if domain.ReasoningSummary(item) && item.ID != "" {
			if _, seen := s.reasoningFresh[item.ID]; !seen {
				s.reasoningFresh[item.ID] = continuous || conversationui.ReasoningSummaryText(item) == ""
			}
			continue
		}
		if item.Kind == "request" || item.Kind == "request_result" {
			hadReceipt, seen := s.requests[item.ID]
			inline := item.Kind == "request" && domain.RequestState(item.Status) == domain.RequestAnswered &&
				domain.HasInlineRequestReceipt(item.Presentation) && len(item.Parts) > 0
			if item.Kind == "request" {
				requests[item.ID] = inline
			}
			if continuous && (inline || item.Kind == "request_result") && s.receipts[item.ID].IsZero() &&
				(item.Order > s.previousOrder || seen && !hadReceipt) {
				s.receipts[item.ID] = now
			}
			if at := s.receipts[item.ID]; !at.IsZero() {
				receiptTurn = item.TurnID
				receiptUntil = at.Add(mobileReceiptEntrance)
			}
			known[item.ID] = true
			continue
		}
		if item.Kind != "assistant" || item.ID == "" {
			continue
		}
		known[item.ID] = true
		entry := s.streams[item.ID]
		fresh := continuous && item.Order > s.previousOrder
		if entry == nil {
			entry = &mobileReplyPresentation{turn: item.TurnID}
			if fresh && item.TurnID == receiptTurn && now.Before(receiptUntil) {
				entry.revealAfter = receiptUntil
			}
			s.streams[item.ID] = entry
		}
		mounted := !entry.drawnAt.IsZero() && (entry.drawnAt == s.frame || entry.drawnAt == now)
		waiting := !entry.revealAfter.IsZero() && now.Before(entry.revealAfter)
		// While waiting to reveal the reply, receive deltas normally but don't
		// establish them as the already painted prefix.
		if waiting {
			entry.revealing = true
			if s.nextReveal.IsZero() || entry.revealAfter.Before(s.nextReveal) {
				s.nextReveal = entry.revealAfter
			}
			continue
		}
		deferred := !entry.revealAfter.IsZero()
		entry.revealAfter = time.Time{}
		added := entry.stream.prepare(now, item, continuous, mounted, fresh || deferred)
		entry.revealing = entry.stream.live && (added || entry.revealing)
	}
	for id := range s.streams {
		if !known[id] {
			delete(s.streams, id)
		}
	}
	for id := range s.receipts {
		if !known[id] {
			delete(s.receipts, id)
		}
	}
	s.requests = requests
	s.scope, s.frame, s.order, s.enabled = scope, now, order, enabled
}

func (s *replyTail) holdReceipt(c *ui.Context, turnID string) bool {
	for _, entry := range s.streams {
		if entry.turn == turnID && entry.revealing && (entry.drawnAt == c.Now() || !entry.drawnAt.IsZero() && entry.drawnAt == s.previousFrame || c.Now().Before(entry.revealAfter) || entry.stream.appendedAt == c.Now()) {
			c.AnimationFrame()
			return true
		}
	}
	return false
}

func (s *replyTail) receiptProgress(c *ui.Context, id string) float32 {
	at := s.receipts[id]
	if at.IsZero() || c.Preferences().ReduceMotion {
		return 1
	}
	progress := min(1, float32(c.Now().Sub(at))/float32(mobileReceiptEntrance))
	if progress < 1 {
		c.AnimationFrame()
	}
	return ui.EaseOut(max(0, progress))
}

func (s *replyTail) draw(c *ui.Context, item domain.Item, options *markdown.Options, list *ui.ListState, embedDelta *float32) {
	var stream replyStream
	if entry := s.streams[item.ID]; entry != nil {
		entry.drawnAt = c.Now()
		if c.Now().Before(entry.revealAfter) {
			c.AnimationFrame()
			return
		}
		stream = entry.stream
		options.OnRevealState = func(active bool) {
			if entry.revealing != active {
				entry.revealing = active
				c.AnimationFrame()
			}
		}
	}
	measured := c.Key("reply-height")
	frame := ui.Column(measured).FillWidth().Clip()
	duration := time.Duration(0)
	if stream.live {
		duration = mobileReplyFade
	}
	delta := conversationui.AnimatedContentHeight(measured, frame, true, !stream.live, duration, func() float32 {
		*embedDelta = 0
		stream.draw(measured.Key("text"), item, options)
		if item.Text == "" && options.OnRevealState != nil {
			options.OnRevealState(false)
		}
		return *embedDelta
	})
	if delta != 0 {
		list.FollowAnimation = nil
	}
}

// New event rows get real space as they enter. Replies grow inside their own
// measured clip, and accepted local inputs use the composer handoff.
func (s *replyTail) revealEvents(c *ui.Context, state *ui.ListState, rows []mobileclient.TranscriptRow, items []domain.Item, offset int, skip ...func(mobileclient.TranscriptRow) bool) {
	if c.Preferences().ReduceMotion {
		clear(s.arrivals)
		return
	}
	fresh := s.continuous && s.order > s.previousOrder
	if !fresh && len(s.arrivals) == 0 {
		return
	}
	for index, row := range rows {
		if row.Footer != "" {
			continue
		}
		if len(skip) > 0 && skip[0] != nil && skip[0](row) {
			continue
		}
		item := items[row.First]
		switch item.Kind {
		case "user", "command", "request":
			continue
		case "assistant":
			if len(item.Questions) == 0 && (item.Text != "" || len(item.Parts) == 0) {
				continue // prose already animates its measured content height
			}
		}
		index += offset
		key := state.Key(index)
		start, entering := s.arrivals[key]
		if !entering {
			if !fresh || item.Order <= s.previousOrder {
				continue
			}
			start = c.Now()
			if item.Kind == "request_result" {
				start = s.receipts[item.ID]
				if start.IsZero() {
					continue
				}
			}
			if s.arrivals == nil {
				s.arrivals = make(map[any]time.Time)
			}
			s.arrivals[key] = start
		}
		duration := mobileReplyFade
		if item.Kind == "request_result" {
			duration = mobileReceiptEntrance
		}
		progress := float32(c.Now().Sub(start)) / float32(duration)
		if progress >= 1 {
			delete(s.arrivals, key)
			continue
		}
		state.Reveals = append(state.Reveals, ui.ListReveal{First: index, End: index + 1, Progress: ui.EaseOut(progress)})
		c.AnimationFrame()
	}
	for key, start := range s.arrivals {
		if c.Now().Sub(start) >= max(mobileReplyFade, mobileReceiptEntrance) {
			delete(s.arrivals, key)
		}
	}
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
