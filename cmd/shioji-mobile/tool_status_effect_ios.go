package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

type mobileToolSweep struct {
	row              ui.Element
	started          time.Time
	highlight, green ui.Color
}

var mobileSweepMask = ui.RGB(255, 255, 255)

// Tint the native text's alpha mask so line wrapping, ellipsis and baseline
// remain under native layout. iOS uses tinted bitmap slices for this effect.
type mobileTextSweepEffect struct {
	started         time.Time
	rest, highlight ui.Color
}

func (mobileTextSweepEffect) EffectMargin() ui.Point { return ui.Point{} }

func (s mobileTextSweepEffect) PaintEffect(p *ui.Painter, box ui.Rect, content *ui.Bitmap, inset ui.Point) {
	p.AnimationFrameRate(60)
	if box.W <= 0 || box.H <= 0 {
		return
	}
	w, h := content.Size()
	if w <= 0 || h <= 0 {
		return
	}
	scale := p.Scale()
	originX, originY := box.X-inset.X/scale, box.Y-inset.Y/scale
	center, width := mobileSweepBand(box.W, p.Now().Sub(s.started))
	left := max(0, min(w, int(inset.X+(center-width)*scale)))
	right := max(left, min(w, int(math.Ceil(float64(inset.X+(center+width)*scale)))))
	draw := func(first, end int, tint ui.Color) {
		if first == end {
			return
		}
		p.ImageRegion(content,
			ui.Rect{X: float32(first), W: float32(end - first), H: float32(h)},
			ui.Rect{X: originX + float32(first)/scale, Y: originY, W: float32(end-first) / scale, H: float32(h) / scale},
			ui.ImageOptions{Tint: tint})
	}
	draw(0, left, s.rest)
	step := max(1, int(3*scale))
	for first := left; first < right; first += step {
		end := min(first+step, right)
		mid := (float32(first+end)/2 - inset.X) / scale
		draw(first, end, mobileSweepColor(s.rest, s.highlight, center, width, mid))
	}
	draw(right, w, s.rest)
}

// The caller paints affected text white to supply the mask's alpha.
func mobileTextSweep(c *ui.Context, target ui.Element, phase ...time.Time) bool {
	if c.Preferences().ReduceMotion {
		return false
	}
	started := *ui.Local(target, "text-sweep-start", c.Now)
	if len(phase) != 0 {
		started = phase[0]
	}
	p := palette(c)
	target.Effect(mobileTextSweepEffect{started: started, rest: p.secondary, highlight: p.ink})
	return true
}

func mobileShimmeredText(c *ui.Context, text ui.Element, phase ...time.Time) {
	if mobileTextSweep(c, text, phase...) {
		text.TextColor(mobileSweepMask)
	}
}

func newMobileToolSweep(c *ui.Context, row ui.Element, started time.Time) *mobileToolSweep {
	if c.Preferences().ReduceMotion {
		return nil
	}
	green := ui.RGB(1, 107, 59)
	if c.Theme().Dark {
		green = ui.RGB(114, 255, 165)
	}
	return &mobileToolSweep{row: row, started: started, highlight: palette(c).ink, green: green}
}

// Use the same continuous band as the Mac status row. Paint the shaped
// glyphs directly so the iOS renderer does not need an offscreen effect layer.
func (s *mobileToolSweep) band(p *ui.Painter) (center, width float32) {
	if !s.row.Valid() {
		return 0, 0
	}
	p.AnimationFrameRate(60)
	row := s.row.LayoutBounds()
	if row.W <= 0 {
		return 0, 0
	}
	center, width = mobileSweepBand(row.W, p.Now().Sub(s.started))
	return row.X + center, width
}

func mobileSweepBand(rowWidth float32, elapsed time.Duration) (center, width float32) {
	width = max(22, rowWidth*0.23)
	progress := float32(math.Mod(elapsed.Seconds()/2.2, 1))
	return progress*(rowWidth+2*width) - width, width
}

func mobileSweepColor(ink, highlight ui.Color, center, width, x float32) ui.Color {
	strength := max(0, 1-float32(math.Abs(float64(x-center)))/width)
	strength = strength * strength * (3 - 2*strength)
	return ink.Mix(highlight, strength)
}

func (s *mobileToolSweep) color(p *ui.Painter, r ui.Rect, ink ui.Color) ui.Color {
	if s == nil {
		return ink
	}
	center, width := s.band(p)
	if width <= 0 {
		return ink
	}
	highlight := s.highlight
	if int(ink.G) > int(max(ink.R, ink.B))+20 {
		highlight = s.green
	}
	return mobileSweepColor(ink, highlight, center, width, r.X+r.W/2)
}

func (s *mobileToolSweep) text(p *ui.Painter, glyphs []ui.Glyph, x, baseline float32, ink ui.Color) {
	if s == nil {
		p.Glyphs(glyphs, x, baseline, ink)
		return
	}
	center, width := s.band(p)
	if width <= 0 {
		p.Glyphs(glyphs, x, baseline, ink)
		return
	}
	// Keep contiguous dim glyphs together and shade the moving band per
	// glyph. Shaping and font fallback are still shared with layout.
	start := 0
	for i, glyph := range glyphs {
		mid := x + glyph.X + glyph.Advance/2
		if mid <= center-width || mid >= center+width {
			continue
		}
		p.Glyphs(glyphs[start:i], x, baseline, ink)
		p.Glyphs(glyphs[i:i+1], x, baseline, mobileSweepColor(ink, s.highlight, center, width, mid))
		start = i + 1
	}
	p.Glyphs(glyphs[start:], x, baseline, ink)
}

func mobileStatusFont(c *ui.Context, size float32) ui.Font {
	return ui.Font{Family: c.Theme().Font, Size: size * c.Preferences().TextScale}
}

func mobileStatusLineHeight(font ui.Font) float32 {
	return max(20, 20*font.Size/captionTextSize)
}

// Place measured ink on the row's actual text baseline, including CJK fallback
// and tabular digits. Centering two independent text boxes can shift the clock.
func mobileInlineText(c *ui.Context, label string, font ui.Font, width float32, paint func(*ui.Painter, ui.Rect, float32)) ui.Element {
	metrics := ui.ShapeText("国", font, 0).Lines()[0]
	return ui.RichText(c, ui.Span{Text: "\uFFFC", ObjectWidth: width, ObjectHeight: metrics.Ascent, ObjectDescent: metrics.Descent}).
		Font(font.Family).FontSize(font.Size).FontFeatures(font.Features).FixedLineHeight(mobileStatusLineHeight(font)).
		SingleLine().Role(ui.RoleText).Label(label).DrawObjects(func(p *ui.Painter, _ int, r ui.Rect) {
		paint(p, r, r.Y+metrics.Ascent)
	})
}

func (s *mobileToolSweep) caption(c *ui.Context, title string, transition ui.ContentTransition) {
	ink := palette(c).secondary
	font := mobileStatusFont(c, captionTextSize)
	if s == nil {
		oneLine(c, title, captionTextSize, ink).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font)).
			Shrink(1).ContentTransition(transition)
		return
	}
	layout := ui.ShapeText(title, font, 0)
	width, _ := layout.Size()
	// Keep the caller's key so adjacent captions have independent widths.
	slot := ui.Row(c).MinWidth(0).Shrink(1).ClipX().AlignItems(ui.Baseline)
	extent := ui.Local(slot, "extent", func() mobileCaptionExtent {
		return mobileCaptionExtent{target: width, shown: width}
	})
	if extent.target != width {
		extent.release = time.Time{}
		if width < extent.shown {
			extent.release = c.Now().Add(mobileReplyFade)
		}
		extent.target = width
	}
	target := width
	if wait := extent.release.Sub(c.Now()); wait > 0 {
		target = max(width, extent.shown)
		c.After(wait)
	}
	extent.shown = slot.AnimateTo("caption-width", target, ui.Animation{Duration: mobileReplyFade, Ease: ui.EaseOut})
	slot.Width(extent.shown).Children(func() {
		text := []rune(title)
		glyphs := ui.Shape(title, font)
		available := width
		var element ui.Element
		element = mobileInlineText(c.Key(title), title, font, width, func(p *ui.Painter, r ui.Rect, baseline float32) {
			space := available
			if element.Valid() {
				space = element.LayoutBounds().W
			}
			if space != available {
				available = space
				display := title
				if width > space+.5 {
					lo, hi := 0, len(text)
					for lo < hi {
						mid := (lo + hi + 1) / 2
						measured, _ := ui.ShapeText(string(text[:mid])+"…", font, 0).Size()
						if measured <= space+.5 {
							lo = mid
						} else {
							hi = mid - 1
						}
					}
					display = string(text[:lo]) + "…"
				}
				glyphs = ui.Shape(display, font)
			}
			s.text(p, glyphs, r.X, baseline, ink)
		}).MinWidth(0).Shrink(1).ClipX().Transition(mobileFeedbackFade(mobileReplyFade))
	})
}

type mobileCaptionExtent struct {
	target, shown float32
	release       time.Time
}
