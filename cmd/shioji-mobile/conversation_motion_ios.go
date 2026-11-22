package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/mobileclient"
)

var mobileComposerMotionEase = ui.CubicBezier(.4, 0, .2, 1)

func mobileFeedbackFade(duration time.Duration) ui.ElementTransition {
	return ui.ElementTransition{Duration: duration, Ease: ui.EaseOut,
		Enter: &ui.Motion{Opacity: 0}, Exit: &ui.Motion{Opacity: 0}}
}

func mobilePositionMotion(c *ui.Context) ui.Animation {
	if c.Preferences().ReduceMotion {
		return ui.Animation{}
	}
	return ui.Animation{Duration: 240 * time.Millisecond, Ease: ui.EaseOut}
}

func mobileOutputFollowMotion(c *ui.Context) ui.Animation {
	if c.Preferences().ReduceMotion {
		return ui.Animation{}
	}
	return ui.Animation{Duration: 160 * time.Millisecond, Ease: ui.EaseOut}
}

// Animate the occupied layout while keeping the outgoing contents inert.
func mobilePanelPresence(c *ui.Context, frame ui.Element, visible bool, duration time.Duration, build func(*ui.Context)) float32 {
	height := ui.Local(frame, "presence-height", func() float32 { return 0 })
	target := float32(0)
	if visible {
		target = 1
	}
	presence := target
	if c.Preferences().ReduceMotion {
		frame.Jump("presence", target)
	} else {
		presence = frame.AnimateTo("presence", target, ui.Animation{Duration: duration, Ease: ui.EaseOut})
	}
	if !visible && presence == 0 {
		frame.Height(0).Hidden(true)
		return 0
	}
	if presence < 1 && *height > 0 {
		frame.Height(*height*presence).Margin(0, 0, -composerPanelGap*(1-presence))
	}
	ui.Inert(frame, !visible)
	frame.Children(func() {
		body := c.Key("presence-body")
		natural := ui.Column(body).FillWidth().Shrink(0)
		if presence < 1 {
			natural.Absolute().Left(0).Top(0).Right(0)
		}
		natural.DrawOver(func(_ *ui.Painter, r ui.Rect) {
			if visible && r.H > 0 && *height != r.H {
				*height = r.H
				if presence < 1 {
					c.Services().Invalidate()
				}
			}
		}).Children(func() { build(body) })
	})
	return (*height + composerPanelGap) * presence
}

type mobileActivityCaption struct {
	current, candidate string
	since, keepUntil   time.Time
}

func (m *mobileActivityCaption) update(c *ui.Context, label string) string {
	if m.current == "" {
		m.current = label
	}
	if m.current == label || c.Preferences().ReduceMotion {
		m.current, m.candidate = label, ""
		return label
	}
	now := c.Now()
	transient := label == "正在思考" || label == "正在执行命令" || label == "正在执行工具"
	if m.candidate != label {
		m.candidate, m.since = label, now
	}
	wait := m.keepUntil
	if transient {
		wait = maxTime(wait, m.since.Add(180*time.Millisecond))
	}
	if now.Before(wait) {
		c.After(wait.Sub(now))
		return m.current
	}
	m.current, m.candidate = label, ""
	if transient {
		m.keepUntil = now.Add(500 * time.Millisecond)
	}
	return label
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

type mobileDraftHeight struct {
	text        string
	width, size float32
	height      float32
}

func mobileComposerExtraHeight(c *ui.Context, surface ui.Element, draft *mobileDraftEditor, text string,
	baseHeight, progress float32, queued bool) float32 {
	if draft.inputBounds.W <= 0 {
		return 0
	}
	font := ui.Font{Family: c.Theme().Font, Size: bodyTextSize * c.Preferences().TextScale}
	// Never shape an unbounded pasted document just to measure the dock.
	if len(text) > 2048 {
		text = text[:2048]
	}
	width := draft.inputBounds.W
	metric := ui.Local(surface, "draft-height", func() mobileDraftHeight { return mobileDraftHeight{} })
	if metric.text != text || metric.width != width || metric.size != font.Size {
		metric.text, metric.width, metric.size = text, width, font.Size
		lines := min(5, len(ui.ShapeText(text, font, width).Lines()))
		metric.height = float32(lines)*composerLineHeight(c) + 2*mobileclient.ComposerEditorPadding
	}
	target := max(0, min(26, metric.height-baseHeight)) * progress
	if c.Preferences().ReduceMotion {
		return surface.Jump("draft-extra-height", target)
	}
	duration := mobileReplyFade
	if queued {
		duration = 360 * time.Millisecond
	}
	return surface.AnimateTo("draft-extra-height", target, ui.Animation{Duration: duration, Ease: ui.EaseOut})
}
