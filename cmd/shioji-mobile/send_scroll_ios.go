package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

var (
	mobileSendEase             = ui.CubicBezier(.4, 0, 0, 1)
	mobileSendScrollAnimation  = ui.Animation{Duration: 480 * time.Millisecond, Ease: mobileSendEase}
	mobileSteerScrollAnimation = ui.Animation{Duration: 480 * time.Millisecond, Ease: ui.CubicBezier(.42, 0, .58, 1)}
	mobileSendHandoffAnimation = ui.Animation{Duration: mobileReplyFade, Ease: ui.EaseOut}
)

// Only the composer that submitted the message may hold its space. A remote
// client submitting to the same turn must not resize this client's composer.
type mobileSendScroll struct {
	operation, epoch       string
	workspace              string
	composerProgress, from float32
	progress               float32
	started                bool
	steer, queued          bool
	source, composerSource ui.Rect
	composerBounds         ui.Rect
	began                  time.Time
	receiptWithoutRow      time.Time
}

func (s *mobileSendScroll) animation() ui.Animation {
	if s.steer {
		return mobileSteerScrollAnimation
	}
	return mobileSendScrollAnimation
}

func (s *mobileSendScroll) begin(operation, epoch string, source ui.Rect, steer, queued bool) {
	if source.W > 0 {
		source = ui.Rect{X: source.X - 13, Y: source.Y - 9, W: source.W + 26, H: source.H + 18}
	}
	*s = mobileSendScroll{
		operation: operation, epoch: epoch,
		composerProgress: s.composerProgress, from: s.composerProgress,
		source: source, composerSource: s.composerBounds, composerBounds: s.composerBounds,
		steer: steer, queued: queued, began: time.Now(),
	}
}

func (s *mobileSendScroll) reveal(c *ui.Context, frame ui.Element, list *ui.ListState, model *app.Model,
	items []domain.Item, rows []mobileclient.TranscriptRow, offset int, enabled bool) {
	if s.operation == "" {
		return
	}
	if c.Preferences().ReduceMotion || model.ConnectionEpoch != s.epoch || model.UncertainSubmissions[s.operation] ||
		(!s.started && c.Now().Sub(s.began) > 15*time.Second) {
		s.operation = ""
		return
	}
	if !enabled {
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
			switch {
			case s.steer:
				list.ScrollToEnd(s.animation())
			case s.workspace != "":
				list.AnchorRow(offset+i, 100, mobileSendScrollAnimation)
			case !s.queued:
				list.ScrollToEnd(mobileSendScrollAnimation)
			}
		}
		s.progress = frame.AnimateTo(motionKey, 1, s.animation())
		if s.progress >= 1 || s.steer {
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

func (s *mobileSendScroll) bubble(c *ui.Context, bubble ui.Element, id string) {
	if !s.started || s.progress >= 1 || "input:"+s.operation != id {
		return
	}
	if s.source.W > 0 {
		animation := s.animation()
		bubble.Background(palette(c).inset.Alpha(s.progress)).
			Transition(ui.ElementTransition{From: &s.source, Duration: animation.Duration,
				Ease: animation.Ease, Position: true, Lift: true})
	} else {
		bubble.Opacity(s.progress).Transform(ui.Translation(0, 10*(1-s.progress)))
	}
}
