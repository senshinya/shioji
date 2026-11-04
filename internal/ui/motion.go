package ui

import (
	"time"

	n "github.com/egoist/mygo/ui"
)

// All custom native controls share one interruptible, neutral feedback layer.
// It paints below content, preserving selected surfaces, glyph colors and layout.
func control(c *n.Context, radius ...float32) n.Element {
	r := float32(6)
	if len(radius) > 0 {
		r = radius[0]
	}
	return controlFeedback(c, n.ButtonBase(c), r)
}

func controlFeedback(c *n.Context, e n.Element, radius float32, primary ...bool) n.Element {
	alpha := float32(0)
	if e.Hovered() || e.FocusVisible() {
		alpha = .055
	}
	if e.Pressed() {
		alpha = .11
	}
	shade := n.RGBA(0, 0, 0, alpha)
	if c.Theme().Dark {
		shade = n.RGBA(255, 255, 255, alpha)
	}
	if len(primary) > 0 && primary[0] {
		ink := c.Theme().AccentText
		shade = n.RGBA(ink.R, ink.G, ink.B, alpha)
	}
	color := e.AnimateColor("control-feedback", shade, motionAnimation(c, 120*time.Millisecond))
	return e.Draw(func(p *n.Painter, r n.Rect) {
		// An exit snapshot can paint after this frame-scoped control expires.
		if e.Valid() && !e.IsDisabled() {
			p.Fill(r, color, radius)
		}
	})
}

func motionAnimation(c *n.Context, duration time.Duration) n.Animation {
	if c.Preferences().ReduceMotion {
		duration = min(duration, 60*time.Millisecond)
	}
	return n.Animation{Duration: duration, Ease: n.EaseOut}
}

func popupMotion(c *n.Context) n.ElementTransition {
	a := motionAnimation(c, 120*time.Millisecond)
	enter, exit := n.Motion{Y: -3}, n.Motion{Y: -2}
	if c.Preferences().ReduceMotion {
		enter.Y, exit.Y = 0, 0
	}
	return n.ElementTransition{Duration: a.Duration, Ease: a.Ease, Colors: true, Enter: &enter, Exit: &exit}
}

func popover(c *n.Context, trigger n.Element, open *bool, build func(n.Element)) n.Element {
	panel := n.PopoverBase(c, trigger, open, func(panel n.Element) {
		panel.Transition(popupMotion(c))
		build(panel)
	})
	if panel.Dismissed() {
		trigger.Focus()
	}
	return panel
}

func disclosureMotion(c *n.Context, duration time.Duration) n.ElementTransition {
	a := motionAnimation(c, duration)
	pose := n.Motion{Collapse: !c.Preferences().ReduceMotion}
	return n.ElementTransition{Duration: a.Duration, Ease: a.Ease, Size: !c.Preferences().ReduceMotion, Colors: true, Enter: &pose, Exit: &pose}
}

func drawerMotion(c *n.Context) n.ElementTransition {
	a := motionAnimation(c, 200*time.Millisecond)
	pose := n.Motion{X: 12}
	if c.Preferences().ReduceMotion {
		pose.X = 0
	}
	return n.ElementTransition{Duration: a.Duration, Ease: a.Ease, Position: !c.Preferences().ReduceMotion, Size: !c.Preferences().ReduceMotion, Colors: true, Enter: &pose, Exit: &pose}
}

func columnMotion(c *n.Context, width float32) n.ElementTransition {
	motion := disclosureMotion(c, 200*time.Millisecond)
	if !c.Preferences().ReduceMotion {
		motion.Position = true
		motion.Enter.X, motion.Exit.X = width, width
	}
	return motion
}

func fieldFeedback(c *n.Context, e n.Element) n.Element {
	border := c.Theme().Border
	if e.Hovered() {
		border = border.Mix(c.Theme().TextMuted, .3)
	}
	if e.FocusWithin() {
		border = border.Mix(c.Theme().TextMuted, .55)
	}
	return e.Border(1, e.AnimateColor("field-border", border, motionAnimation(c, 120*time.Millisecond)))
}

func textField(c *n.Context, value *string) n.Element {
	t := c.Theme()
	return fieldFeedback(c, n.TextInputBase(c, value).Padding(t.Space(1.5), t.Space(2.5)).Radius(t.Radius).Background(t.Surface).Border(1, t.Border))
}
