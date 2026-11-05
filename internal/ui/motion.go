package ui

import (
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
)

type toolDisclosureMotion struct {
	from, to float32
	start    time.Time
	duration time.Duration
}

func (m *toolDisclosureMotion) sample(now time.Time) (float32, bool) {
	fraction := float32(now.Sub(m.start)) / float32(m.duration)
	if fraction >= 1 {
		return m.to, false
	}
	return m.from + (m.to-m.from)*n.EaseOut(max(0, fraction)), true
}

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

func textHoverColor(c *n.Context, e n.Element, rest n.Color) n.Color {
	target := rest
	if e.Hovered() || e.FocusVisible() || e.Pressed() {
		target = c.Theme().Text
	}
	return e.AnimateColor("text-hover", target, motionAnimation(c, 120*time.Millisecond))
}

func motionAnimation(c *n.Context, duration time.Duration) n.Animation {
	if c.Preferences().ReduceMotion {
		duration = min(duration, 60*time.Millisecond)
	}
	return n.Animation{Duration: duration, Ease: n.EaseOut}
}

func popupMotion(c *n.Context) n.ElementTransition {
	a := motionAnimation(c, 120*time.Millisecond)
	enter, exit := n.Motion{Scale: .92}, n.Motion{Scale: .96}
	if c.Preferences().ReduceMotion {
		enter.Scale, exit.Scale = 1, 1
	}
	return n.ElementTransition{Duration: a.Duration, Ease: a.Ease, Colors: true, AnchorOrigin: true, Enter: &enter, Exit: &exit}
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

type activityPulseKey struct{}

func statusDot(c *n.Context, label string, font n.Font, color n.Color, pulse bool) n.Element {
	m := inlineFontMetrics(label, font)
	dot := n.RichText(c, n.Span{Text: "\uFFFC", ObjectWidth: 6, ObjectHeight: m.ascent, ObjectDescent: m.descent}).
		Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(20).Role(n.RoleNone)
	if pulse {
		dot.Label("进行中状态")
	} else {
		dot.Label("任务待处理")
	}
	started := *n.Local(dot, activityPulseKey{}, func() time.Time { return c.Now() })
	reduce := c.Preferences().ReduceMotion
	dot.DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
		r.Y += m.ascent - (3 + m.center)
		r.W, r.H = 6, 6
		alpha := float32(1)
		if pulse && !reduce {
			phase := p.Now().Sub(started).Seconds() / 1.4
			alpha = .4 + .6*float32((1-math.Cos(2*math.Pi*phase))/2)
			p.AnimationFrameRate(30)
		}
		p.Fill(r, color.Alpha(alpha), 3)
	})
	return dot
}
