package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

var (
	mobileSendScrollAnimation  = ui.Animation{Duration: 260 * time.Millisecond, Ease: ui.EaseOut}
	mobileSendHandoffAnimation = ui.Animation{Duration: mobileReplyFade, Ease: ui.EaseOut}
)

// Only the composer that submitted the message may hold its space. A remote
// client submitting to the same turn must not resize this client's composer.
type mobileSendScroll struct {
	operation, epoch       string
	composerProgress, from float32
	progress               float32
	started                bool
	receiptWithoutRow      time.Time
}

func (s *mobileSendScroll) begin(operation, epoch string) {
	*s = mobileSendScroll{
		operation: operation, epoch: epoch,
		composerProgress: s.composerProgress, from: s.composerProgress,
	}
}

func (s *mobileSendScroll) reveal(c *ui.Context, frame ui.Element, list *ui.ListState, model *app.Model,
	items []domain.Item, rows []mobileclient.TranscriptRow, offset int, enabled bool) {
	if s.operation == "" {
		return
	}
	if !enabled || model.ConnectionEpoch != s.epoch || model.UncertainSubmissions[s.operation] {
		s.operation = ""
		return
	}

	input := "input:" + s.operation
	for i, row := range rows {
		if row.Footer != "" || items[row.First].ID != input || items[row.First].Status != "sent" {
			continue
		}
		const motionKey = "accepted-message"
		if !s.started {
			frame.Jump(motionKey, 0)
			s.started = true
		}
		s.progress = frame.AnimateTo(motionKey, 1, mobileSendScrollAnimation)
		if s.progress >= 1 {
			return
		}
		end := i + 1
		if end < len(rows) && rows[end].Footer != "" && rows[end].Receipt.AfterInput == input {
			end++
		}
		list.Reveals = append(list.Reveals, ui.ListReveal{First: offset + i, End: offset + end, Progress: s.progress})
		return
	}
	// The preceding progress row can become a taller reply footer before the
	// new message is visible. Let the list follow that height change until the
	// message and composer take over on their shared timeline.
	list.FollowAnimation = &mobileSendHandoffAnimation
	for _, item := range items {
		if item.ID == input {
			// The prior running footer may still be handing off to its summary.
			return
		}
	}
	if _, pending := model.Pending[s.operation]; pending {
		return
	}
	// A receipt can arrive a frame before its journal row. Bound that visual
	// hold so a rejected or interrupted send does not retain the old geometry.
	if s.receiptWithoutRow.IsZero() {
		s.receiptWithoutRow = c.Now()
	}
	if c.Now().Sub(s.receiptWithoutRow) >= mobileReplyFade {
		s.operation = ""
		return
	}
	c.AnimationFrame()
}
