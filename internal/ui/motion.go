package ui

import (
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
)

// A non-bouncing spring preserves velocity as streamed layout keeps growing.
// Keep it shorter than message navigation, so following does not trail behind.
var conversationFollowAnimation = n.Animation{Spring: n.SpringOptions{Duration: 240 * time.Millisecond}}

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

func fadeMotion(c *n.Context, duration time.Duration) n.ElementTransition {
	a := motionAnimation(c, duration)
	return n.ElementTransition{Duration: a.Duration, Ease: a.Ease, Colors: true, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}}
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

// Reveal the directory menu downward from its top edge. Its contents retain
// their final layout and opacity while the actual panel height opens/closes.
func workspacePopupMotion(c *n.Context) n.ElementTransition {
	if c.Preferences().ReduceMotion {
		return n.ElementTransition{Duration: 0}
	}
	pose := n.Motion{Collapse: true, Opacity: 1}
	return n.ElementTransition{Duration: 180 * time.Millisecond, Ease: n.EaseOut, Size: true, Enter: &pose, Exit: &pose}
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
	return fieldFeedback(c, n.TextInputBase(c, value).SelectionColor(editorSelection(t)).Padding(t.Space(1.5), t.Space(2.5)).Radius(t.Radius).Background(t.Surface).Border(1, t.Border))
}

type activityPulseKey struct{}

func statusDot(c *n.Context, label string, font n.Font, color n.Color, pulse bool, phase ...time.Time) n.Element {
	return statusDotSize(c, label, font, color, 6, pulse, phase...)
}
func statusDotSize(c *n.Context, label string, font n.Font, color n.Color, size float32, pulse bool, phase ...time.Time) n.Element {
	m := inlineFontMetrics(label, font)
	dot := n.RichText(c, n.Span{Text: "\uFFFC", ObjectWidth: size, ObjectHeight: m.ascent, ObjectDescent: m.descent}).
		Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(20).Role(n.RoleNone)
	if pulse {
		dot.Label("进行中状态")
	} else {
		dot.Label("任务待处理")
	}
	started := *n.Local(dot, activityPulseKey{}, func() time.Time { return c.Now() })
	if len(phase) > 0 {
		started = phase[0]
	}
	reduce := c.Preferences().ReduceMotion
	dot.DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
		r.Y += m.ascent - (size/2 + m.center)
		r.W, r.H = size, size
		alpha := float32(1)
		if pulse && !reduce {
			phase := p.Now().Sub(started).Seconds() / 1.4
			alpha = .4 + .6*float32((1-math.Cos(2*math.Pi*phase))/2)
			p.AnimationFrameRate(30)
		}
		p.Fill(r, color.Alpha(alpha), size/2)
	})
	return dot
}

func animationPointer(c *n.Context, duration time.Duration) *n.Animation {
	a := motionAnimation(c, duration)
	return &a
}

// Animate the real height, not just painted bounds. Initial history mounts at
// its measured height; subsequent changes and visibility use that same clock.
func animatedContentHeight(c *n.Context, frame n.Element, visible bool, duration time.Duration, build func() float32) {
	animatedContentHeightAtMount(c, frame, visible, visible, duration, build)
}

// Transcript content owns its height animation. The list must apply that
// movement directly rather than add a second live-output follow animation.
func (v *View) transcriptContentHeight(c *n.Context, frame n.Element, visible, initiallyVisible bool, duration time.Duration, build func() float32) {
	delta := animatedContentHeightAtMount(c, frame, visible, initiallyVisible, duration, build)
	if delta != 0 {
		if list := v.lists[v.Model.SessionID]; list != nil {
			list.FollowAnimation = nil
		}
	}
}

// Live replies enter from zero; history and virtual-row remounts establish
// their measured baseline without replaying the content's entrance.
func animatedContentHeightAtMount(c *n.Context, frame n.Element, visible, initiallyVisible bool, duration time.Duration, build func() float32) float32 {
	natural := n.Local(frame, "natural-height", func() float32 { return 0 })
	baseline := n.Local(frame, "initially-visible", func() bool { return initiallyVisible })
	target := float32(0)
	if visible {
		target = *natural
	}
	animation := motionAnimation(c, duration)
	if c.Preferences().ReduceMotion {
		animation.Duration = 0
	}
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
			if math.Abs(float64(*natural-r.H)) > .5 {
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
