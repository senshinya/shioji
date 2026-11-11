package ui

import (
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

var shimmerShader = shader.MustCompile(`package main
var Time float
var Rest vec4
var Highlight vec4
func ColorEffect(pos vec2, color vec4) vec4 {
	width := max(14.0, rectSize().x * 0.18)
	center := fract(Time / 2.2) * (rectSize().x + width * 2.0) - width
	band := 1.0 - smoothstep(0.0, width, abs(pos.x - center))
	ink := mix(Rest.rgb, Highlight.rgb, band)
	return vec4(ink * color.a, color.a)
}`)

type shimmerEffect struct {
	started         time.Time
	rest, highlight n.Color
}

func (shimmerEffect) EffectMargin() n.Point { return n.Point{} }
func (fx shimmerEffect) PaintEffect(p *n.Painter, box n.Rect, content *n.Bitmap, inset n.Point) {
	shimmerShader.Effect(shader.Options{Uniforms: map[string]any{
		"Time": float32(p.Now().Sub(fx.started).Seconds()), "Rest": fx.rest, "Highlight": fx.highlight,
	}}).PaintEffect(p, box, content, inset)
	p.AnimationFrameRate(60)
}

func shimmer(c *n.Context, text n.Element, rest, highlight n.Color, phase ...time.Time) {
	if c.Preferences().ReduceMotion {
		return
	}
	started := *n.Local(text, "shimmer-start", func() time.Time { return c.Now() })
	if len(phase) > 0 {
		started = phase[0]
	}
	text.Effect(shimmerEffect{started: started, rest: rest, highlight: highlight})
}
