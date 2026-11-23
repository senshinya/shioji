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

const mobileSentActionsFade = 120 * time.Millisecond

// Only the composer that submitted the message may hold its space. A remote
// client submitting to the same turn must not resize this client's composer.
type mobileSendScroll struct {
	operation, epoch       string
	workspace              string
	composerProgress, from float32
	progress, elapsed      float32
	started                bool
	steer, queued          bool
	keyboardComposer       bool
	source, composerSource ui.Rect
	composerBounds         ui.Rect
	began                  time.Time
	provisional            *domain.Item
	receiptWithoutRow      time.Time
}

func (s *mobileSendScroll) animation() ui.Animation {
	if s.steer {
		return mobileSteerScrollAnimation
	}
	return mobileSendScrollAnimation
}

func (s *mobileSendScroll) begin(operation, epoch string, source ui.Rect, steer, queued bool) {
	*s = mobileSendScroll{
		operation: operation, epoch: epoch,
		composerProgress: s.composerProgress, from: s.composerProgress,
		composerSource: s.composerBounds, composerBounds: s.composerBounds,
		steer: steer, queued: queued, began: time.Now(),
	}
	s.setSource(source)
}

func (s *mobileSendScroll) setSource(source ui.Rect) {
	if source.W > 0 && source.H > 0 {
		s.source = ui.Rect{X: source.X - 13, Y: source.Y - 9, W: source.W + 26, H: source.H + 18}
	} else if !s.queued {
		s.source = s.composerBounds
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
		if row.Footer != "" {
			continue
		}
		item := items[row.First]
		if item.ID != input {
			continue
		}
		if item.Status != "sent" {
			if s.provisional == nil {
				continue
			}
			if item.Status != "dispatching" && item.Status != "" {
				s.operation = ""
				return
			}
		}
		if s.advance(frame) {
			switch {
			case s.steer:
				list.ScrollToEnd(s.animation())
			case s.workspace != "":
				list.AnchorRow(offset+i, 100, mobileSendScrollAnimation)
			case !s.queued:
				list.ScrollToEnd(mobileSendScrollAnimation)
			}
		}
		if s.progress >= 1 || s.steer && s.queued {
			return
		}
		if s.steer && i > 0 && rows[i-1].Receipt.BeforeInput == input {
			// The receipt fades as a full row while its reserved space grows.
			list.Reveals = append(list.Reveals, ui.ListReveal{First: offset + i - 1, End: offset + i, Progress: s.progress, LayoutOnly: true})
		}
		// The bubble lifts from the composer while its row makes room for it.
		// Keep the live footer fully visible throughout that height change.
		list.Reveals = append(list.Reveals, ui.ListReveal{
			First: offset + i, End: offset + i + 1, Progress: s.progress, LayoutOnly: s.source.W > 0,
		})
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

func (s *mobileSendScroll) advance(frame ui.Element) bool {
	const motionKey = "accepted-message"
	starting := !s.started
	if starting {
		frame.Jump(motionKey, 0)
		s.started = true
	}
	animation := s.animation()
	s.elapsed = frame.AnimateTo(motionKey, 1, ui.Animation{Duration: animation.Duration, Ease: ui.Linear})
	s.progress = animation.Ease(s.elapsed)
	return starting
}

func (s *mobileSendScroll) flying(id string) bool {
	return s != nil && s.operation != "" && s.started && s.progress < 1 && "input:"+s.operation == id && s.source.W > 0
}

func (s *mobileSendScroll) actionsOpacity(id string) float32 {
	if s.flying(id) {
		fade := float32(mobileSentActionsFade) / float32(s.animation().Duration)
		return ui.EaseOut(max(0, (s.elapsed-(1-fade))/fade))
	}
	return 1
}

func (s *mobileSendScroll) replyFooterDelay(id string) time.Duration {
	if !s.flying(id) {
		return 0
	}
	remaining := time.Duration((1 - s.elapsed) * float32(s.animation().Duration))
	return max(0, remaining-mobileReplyFade)
}

func (s *mobileSendScroll) bubble(c *ui.Context, bubble ui.Element, id string) {
	if s.flying(id) {
		animation := s.animation()
		bubble.Background(palette(c).inset.Alpha(s.progress)).
			Transition(ui.ElementTransition{From: &s.source, Duration: animation.Duration,
				Ease: animation.Ease, Position: true, Lift: true})
	}
}
