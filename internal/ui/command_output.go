package ui

import (
	"math"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

type commandOutputAppend struct {
	start, end int
	at         time.Time
}

type commandOutputState struct {
	scroll            n.ScrollState
	text              string
	following, placed bool
	target            float32
	appends           []commandOutputAppend
}

func (v *View) commandOutput(c *n.Context, t *n.Theme, item domain.Item, heading n.Element) {
	const height = 160
	display := strings.TrimRight(item.Text, "\r\n")
	// The heading exists before stdout does. Preserve the running state there,
	// including a short command whose start/completion share one UI frame.
	state := n.Local(heading, "command-output", func() commandOutputState {
		arriving := v.toolArrivals != nil && v.toolArrivals.motions[item.ID] != nil
		return commandOutputState{text: display, following: arriving}
	})
	live := !item.Final && (item.Status == "running" || item.Status == "starting")
	if live {
		state.following = true
	}
	follow := state.following // Finish laying out/scrolling the final batch too.
	if item.Text == "" || v.Model.Collapsed["tool:"+item.ID] {
		state.text, state.appends = display, nil
		if v.Model.Collapsed["tool:"+item.ID] {
			state.following = false
		}
		return
	}
	output := n.Scroll(c.Key("output:" + item.ID)).FillWidth().MaxHeight(height).Transition(disclosureMotion(c, 160*time.Millisecond))
	if display != state.text {
		if follow && strings.HasPrefix(display, state.text) && !c.Preferences().ReduceMotion {
			state.appends = append(state.appends, commandOutputAppend{len(state.text), len(display), c.Now()})
		} else {
			state.appends = nil
		}
		state.text = display
	}
	// Fade only bytes that actually arrived; earlier output keeps its ink and
	// baseline. Completed fragments merge back into the ordinary prefix.
	spans := []n.Span{}
	from := 0
	active := state.appends[:0]
	for _, addition := range state.appends {
		progress := min(1, float32(c.Now().Sub(addition.at))/float32(180*time.Millisecond))
		if progress >= 1 || c.Preferences().ReduceMotion {
			continue
		}
		active = append(active, addition)
		if from < addition.start {
			spans = append(spans, n.Span{Text: display[from:addition.start]})
		}
		spans = append(spans, n.Span{Text: display[addition.start:addition.end], Color: t.TextMuted.Alpha(n.EaseOut(max(0, progress)))})
		from = addition.end
		c.AnimationFrame()
	}
	state.appends = active
	if from < len(display) {
		spans = append(spans, n.Span{Text: display[from:]})
	}
	var text n.Element
	output.TrackScroll(&state.scroll).Children(func() {
		text = n.RichText(c, spans...).FillWidth().Padding(8, 10).Background(codeSurface(t)).Radius(6).Font(v.Model.Preferences.CodeFont).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Selectable()
		v.decoratePlainFind(c, text, item, &state.scroll)
	})
	animation := motionAnimation(c, 160*time.Millisecond)
	invalidate := c.Services().Invalidate
	output.DrawOver(func(p *n.Painter, r n.Rect) {
		if !follow {
			return
		}
		// The viewport is still opening. Target its eventual capped height,
		// rather than scrolling old lines away to fit each smaller animation
		// frame and then bringing them back as the panel grows.
		contentHeight := text.LayoutBounds().H
		target := max(0, contentHeight-height)
		if !state.placed || math.Abs(float64(target-state.target)) > .5 || !state.scroll.Scrolling() && math.Abs(float64(target-state.scroll.Y)) > .5 {
			state.placed, state.target = true, target
			motion := animation
			if target == 0 {
				motion = n.Animation{}
			}
			state.scroll.ScrollTo(state.scroll.X, target, motion)
			invalidate()
			return
		}
		if !live && !state.scroll.Scrolling() && r.H >= min(height, contentHeight)-.5 {
			state.following = false
		}
	})
}
