package conversationui

import (
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
)

// AnimatedContentHeight eases the actual layout height. Live replies enter from zero; history and virtual-row remounts establish
// their measured baseline without replaying the content's entrance.
func AnimatedContentHeight(c *n.Context, frame n.Element, visible, initiallyVisible bool, duration time.Duration, build func() float32) float32 {
	natural := n.Local(frame, "natural-height", func() float32 { return 0 })
	baseline := n.Local(frame, "initially-visible", func() bool { return initiallyVisible })
	target := float32(0)
	if visible {
		target = *natural
	}
	animation := n.Animation{Duration: duration, Ease: n.EaseOut}
	shown := target
	if duration <= 0 || c.Preferences().ReduceMotion {
		frame.Jump("height", target)
	} else {
		shown = frame.AnimateTo("height", target, animation)
	}
	if *natural > 0 || !*baseline {
		frame.Height(shown)
	}
	n.Inert(frame, !visible)
	if !visible && shown == 0 {
		frame.Height(0)
		return 0
	}
	frame.Children(func() {
		content := n.Column(c.Key("natural-content")).FillWidth().Shrink(0)
		if *natural > 0 || !*baseline {
			content.Absolute().Left(0).Right(0).Top(0)
		}
		invalidate := c.Services().Invalidate
		content.DrawOver(func(_ *n.Painter, r n.Rect) {
			if frame.Valid() && math.Abs(float64(*natural-r.H)) > .5 {
				if visible && *natural == 0 && *baseline {
					frame.Jump("height", r.H)
				}
				*natural = r.H
				invalidate()
			}
		})
		delta := float32(0)
		content.Children(func() { delta = build() })
		// A child such as a loaded image already animates its actual height.
		// Apply that frame's delta directly; easing it again would clip captions
		// and make the following transcript lag behind the image.
		if visible && *natural > 0 && delta != 0 {
			shown = frame.Jump("height", max(0, shown+delta))
			frame.Height(shown)
		}
		// A zero-height closed clip does not paint its children, but layout still
		// measures them. Use that measurement on the next frame to start opening.
		if *natural == 0 && visible && !*baseline {
			if height := content.LayoutBounds().H; height > 0 {
				*natural = height
			}
			c.AnimationFrame()
		}
	})
	previous := n.Local(frame, "reported-height", func() float32 { return -1 })
	delta := float32(0)
	if *natural > 0 {
		if *previous >= 0 {
			delta = shown - *previous
		}
		*previous = shown
	}
	return delta
}
