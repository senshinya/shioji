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
	uniforms        map[string]any
}

func (shimmerEffect) EffectMargin() n.Point { return n.Point{} }
func (fx shimmerEffect) PaintEffect(p *n.Painter, box n.Rect, content *n.Bitmap, inset n.Point) {
	u := fx.uniforms
	if u == nil {
		u = make(map[string]any, 3)
	}
	u["Time"] = float32(p.Now().Sub(fx.started).Seconds())
	u["Rest"] = fx.rest
	u["Highlight"] = fx.highlight
	shimmerShader.Effect(shader.Options{Uniforms: u}).PaintEffect(p, box, content, inset)
	p.AnimationFrameRate(60)
}

func shimmer(c *n.Context, text n.Element, rest, highlight n.Color, phase ...time.Time) {
	if c.Preferences().ReduceMotion {
		return
	}
	started := *n.Local(text, "shimmer-start", func() time.Time { return c.Now() })
	uniforms := *n.Local(text, "shimmer-uniforms", func() map[string]any { return make(map[string]any, 3) })
	if len(phase) > 0 {
		started = phase[0]
	}
	text.Effect(shimmerEffect{started: started, rest: rest, highlight: highlight, uniforms: uniforms})
}

// Apply one running-clock sweep to all of the row's painted ink, including
// icons and the live timer. Preserve premultiplied alpha and the dot's hue.
var toolStatusSweepShader = shader.MustCompile(`package main
var Time float
var Strength float
var Highlight vec4
var Green vec4
func ColorEffect(pos vec2, color vec4) vec4 {
    width := max(22.0, rectSize().x * 0.23)
    center := fract(Time / 2.2) * (rectSize().x + width * 2.0) - width
    band := 1.0 - smoothstep(0.0, width, abs(pos.x - center))
    greenDominance := (color.g - max(color.r, color.b)) / max(color.a, 0.001)
    isGreen := smoothstep(0.05, 0.22, greenDominance)
    ink := mix(Highlight.rgb, Green.rgb, isGreen) * color.a
    return vec4(mix(color.rgb, ink, band * Strength), color.a)
}
`)

type toolStatusSweepEffect struct {
	started          time.Time
	highlight, green n.Color
	extent           float32
	uniforms         map[string]any
}

func (toolStatusSweepEffect) EffectMargin() n.Point { return n.Point{} }

func (fx toolStatusSweepEffect) PaintEffect(p *n.Painter, box n.Rect, content *n.Bitmap, inset n.Point) {
	u := fx.uniforms
	if u == nil {
		u = make(map[string]any, 4)
	}
	u["Time"] = float32(p.Now().Sub(fx.started).Seconds())
	u["Strength"] = fx.extent
	u["Highlight"] = fx.highlight
	u["Green"] = fx.green
	toolStatusSweepShader.Effect(shader.Options{Uniforms: u}).PaintEffect(p, box, content, inset)
	p.AnimationFrameRate(60)
}

func shimmerToolStatus(c *n.Context, line n.Element, started time.Time, highlight n.Color, extent float32) {
	if c.Preferences().ReduceMotion || extent <= 0 {
		return
	}
	green := n.RGB(1, 107, 59)
	if c.Theme().Background.R < 115 {
		green = n.RGB(114, 255, 165)
	}
	uniforms := *n.Local(line, "tool-sweep-uniforms", func() map[string]any { return make(map[string]any, 4) })
	line.Effect(toolStatusSweepEffect{started: started, highlight: highlight, green: green, extent: extent, uniforms: uniforms})
}
