package conversationui

import (
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
)

// TranscriptFollowAnimation preserves velocity as the measured tail grows.
var TranscriptFollowAnimation = n.Animation{Spring: n.SpringOptions{Duration: 240 * time.Millisecond}}

// AnimatedContentHeight eases the actual layout height. Live replies enter from zero; history and virtual-row remounts establish
// their measured baseline without replaying the content's entrance.
func AnimatedContentHeight(c *n.Context, frame n.Element, visible, initiallyVisible bool, duration time.Duration, build func() float32) float32 {
	natural := n.Local(frame, "natural-height", func() float32 { return 0 })
	baseline := n.Local(frame, "initially-visible", func() bool { return initiallyVisible })
	probing := n.Local(frame, "probe-empty", func() bool { return false })
	childOffset := n.Local(frame, "child-offset", func() float32 { return 0 })
	previous := n.Local(frame, "reported-height", func() float32 { return -1 })
	// Fold accumulated child deltas back into the channel only once the parent's
	// own height ease is idle or collapsing, so child height animations never
	// interrupt an in-flight parent transition.
	if *childOffset != 0 && (!visible || duration <= 0 || c.Preferences().ReduceMotion || frame.Settled("height")) {
		*natural = max(0, *natural+*childOffset)
		if *previous >= 0 {
			frame.Jump("height", *previous)
		}
		*childOffset = 0
	}
	target := float32(0)
	if visible {
		target = *natural
	}
	animation := n.Animation{Duration: duration, Ease: n.EaseOut}
	baseShown := target
	if duration <= 0 || c.Preferences().ReduceMotion {
		frame.Jump("height", target)
	} else {
		baseShown = frame.AnimateTo("height", target, animation)
	}
	shown := max(0, baseShown+*childOffset)
	if *natural > 0 || *childOffset > 0 || !*baseline {
		frame.Height(shown)
	}
	n.Inert(frame, !visible)
	if !visible && shown == 0 {
		*probing = false
		frame.Height(0)
		return 0
	}
	frame.Children(func() {
		content := n.Column(c.Key("natural-content")).FillWidth().Shrink(0)
		if *natural > 0 || *childOffset > 0 || !*baseline {
			content.Absolute().Left(0).Right(0).Top(0)
		}
		invalidate := c.Services().Invalidate
		content.DrawOver(func(_ *n.Painter, r n.Rect) {
			base := max(0, r.H-*childOffset)
			if frame.Valid() && math.Abs(float64(*natural-base)) > .5 {
				if visible && *natural == 0 && *baseline {
					frame.Jump("height", base)
				}
				*natural = base
				*probing = false
				invalidate()
			}
		})
		delta := float32(0)
		content.Children(func() { delta = build() })
		// A child such as a loaded image already animates its actual height.
		// Track its delta as an offset instead of calling Jump on the parent
		// channel, which would cancel and restart any in-flight parent ease.
		if visible && (*natural > 0 || *childOffset > 0) && delta != 0 {
			*childOffset += delta
			shown = max(0, baseShown+*childOffset)
			frame.Height(shown)
		}
		// A zero-height closed clip does not paint its children, but layout still
		// measures them. Probe at most one frame when empty so zero-height content
		// does not spin at display refresh rate, while later content still opens.
		if *natural == 0 && *childOffset == 0 && visible && !*baseline {
			if height := content.LayoutBounds().H; height > 0 {
				*natural = height
				*probing = false
				c.AnimationFrame()
			} else if !*probing {
				*probing = true
				c.AnimationFrame()
			} else {
				*probing = false
			}
		} else {
			*probing = false
		}
	})
	delta := float32(0)
	if *natural > 0 || *childOffset > 0 {
		if *previous >= 0 {
			delta = shown - *previous
		}
		*previous = shown
	}
	return delta
}
