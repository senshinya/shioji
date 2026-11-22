package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

type transcriptProgressKey struct{}

type mobileSubmissionProgress struct {
	operation, epoch string
	caption          string
	settled          time.Time
}

func (s *mobileSubmissionProgress) update(c *ui.Context, a *mobileApp, draft *mobileDraftEditor, items []domain.Item) {
	m := a.model
	send := &draft.sendScroll
	if s.epoch != m.ConnectionEpoch {
		*s = mobileSubmissionProgress{epoch: m.ConnectionEpoch}
	}
	if !send.queued && send.epoch == m.ConnectionEpoch && send.operation != "" && send.operation != s.operation &&
		!(send.started && send.progress >= 1) {
		*s = mobileSubmissionProgress{operation: send.operation, epoch: m.ConnectionEpoch}
	}
	s.caption = ""
	if s.operation == "" {
		return
	}
	id := s.operation
	if m.UncertainSubmissions[id] {
		s.operation = ""
		return
	}
	for _, item := range items {
		if item.ID == "input:"+id && (item.Status == "" || item.Status == "sent" || item.Status == "failed") {
			// Keep the handoff scoped to this insertion until its message motion ends.
			if send.operation != id || send.started && send.progress >= 1 {
				s.operation = ""
			}
			return
		}
	}
	if _, pending := m.Pending[id]; pending {
		s.caption = mobileWaitingCaption(m, id)
		s.settled = time.Time{}
		return
	}
	// Confirmation can precede its journal row. A rejected send already has
	// inline feedback and returns to the underlying turn immediately.
	for _, feedback := range a.operationFeedbackRecords(draft.target.workspace, draft.target.session) {
		if feedback.ID == id && feedback.Kind == app.FeedbackSubmissionFailed {
			s.operation = ""
			return
		}
	}
	if !m.Resolved[id] {
		s.operation = ""
		return
	}
	if s.settled.IsZero() {
		s.settled = c.Now()
	}
	// The receipt confirms execution, while the matching turn can arrive
	// later through the journal. Keep its slot until that turn is present.
	const journalWait = 15 * time.Second
	if remaining := journalWait - c.Now().Sub(s.settled); remaining > 0 {
		s.caption = "进行中"
		c.After(remaining)
	} else {
		s.operation = ""
	}
}

func mobileWaitingCaption(m *app.Model, operation string) string {
	if !m.Connected {
		return "等待重新连接"
	}
	switch state := m.SubmissionReceiptState(operation); {
	case state == domain.OperationPreparing, state == domain.OperationAccepted && m.Pending[operation].SessionID == "":
		return "正在准备环境"
	}
	return "正在发送"
}

// Sending, preparation and execution reuse the text and dot slots. The
// message or composer can move independently without starting an entry fade.
func mobileTranscriptProgress(c *ui.Context, caption string, started *time.Time, live bool) {
	p := palette(c)
	font := mobileStatusFont(c, captionTextSize)
	ui.Row(c).MinHeight(transcriptMetadataHeight).AlignItems(ui.Center).Children(func() {
		line := c.Key("line")
		row := ui.Row(line).MinWidth(0).Shrink(1).Gap(6).AlignItems(ui.Baseline)
		ink := p.secondary
		var sweep *mobileToolSweep
		if live {
			ink = p.active
			phase := *ui.Local(row, "progress-sweep-start", c.Now)
			if started != nil {
				phase = *started
			}
			sweep = newMobileToolSweep(line, row, phase)
		} else if caption == waitingForRequest {
			ink = p.warning
		}
		row.Children(func() {
			transcriptGlyph(line.Key("status-dot"), caption, font, transcriptGlyphSlot, 6, func(p *ui.Painter, bounds ui.Rect) {
				p.Fill(bounds, mobileBreathingInk(p, sweep.color(p, bounds, ink), live && !c.Preferences().ReduceMotion), 3)
			})
			glyphs := ui.Shape(caption, font)
			width, _ := ui.ShapeText(caption, font, 0).Size()
			mobileInlineText(line.Key("status"), caption, font, width, func(painter *ui.Painter, r ui.Rect, baseline float32) {
				sweep.text(painter, glyphs, r.X, baseline, p.secondary)
			}).MinWidth(0).Shrink(1).ClipX()
			if started != nil && !started.IsZero() {
				mobileClock(line.Key("elapsed"), *started, nil, font, sweep)
			}
		})
	})
}
