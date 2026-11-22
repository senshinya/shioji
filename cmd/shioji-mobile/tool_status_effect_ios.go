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

// Draw the sweep with the row's native primitives. A layer effect on the
// entire row drops the text and icons in the iOS renderer.
func (s *mobileToolSweep) color(p *ui.Painter, r ui.Rect, ink ui.Color) ui.Color {
	if s == nil {
		return ink
	}
	p.AnimationFrameRate(60)
	row := s.row.LayoutBounds()
	if row.W <= 0 {
		return ink
	}
	width := max(22, row.W*0.23)
	progress := float32(math.Mod(p.Now().Sub(s.started).Seconds()/2.2, 1))
	center := progress*(row.W+2*width) - width
	distance := float32(math.Abs(float64(r.X + r.W/2 - row.X - center)))
	strength := max(0, 1-distance/width)
	strength = strength * strength * (3 - 2*strength)
	glint := s.highlight
	if int(ink.G) > int(max(ink.R, ink.B))+20 {
		glint = s.green
	}
	return ink.Mix(glint, strength)
}

const mobileToolCaptionSize = 11

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
	font := mobileStatusFont(c, mobileToolCaptionSize)
	if s == nil {
		oneLine(c, title, mobileToolCaptionSize, ink).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font)).
			Shrink(1).ContentTransition(transition)
		return
	}
	layout := ui.ShapeText(title, font, 0)
	width, _ := layout.Size()
	text := []rune(title)
	display := layout
	displayBaseline := layout.Lines()[0].Baseline
	available := width
	var element ui.Element
	element = mobileInlineText(c, title, font, width, func(p *ui.Painter, r ui.Rect, baseline float32) {
		if space := element.LayoutBounds().W; space != available {
			available = space
			display = layout
			for length := len(text) - 1; length >= 0; length-- {
				if measured, _ := display.Size(); measured <= space+.5 {
					break
				}
				display = ui.ShapeText(string(text[:length])+"…", font, 0)
			}
			displayBaseline = display.Lines()[0].Baseline
		}
		r.W = min(r.W, available)
		p.TextLayout(display, r.X, baseline-displayBaseline, s.color(p, r, ink))
	}).MinWidth(0).Shrink(1).ClipX()
}
