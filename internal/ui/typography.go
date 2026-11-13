package ui

import (
	"sync"
	"time"
	"unicode"

	n "github.com/egoist/mygo/ui"
)

const messageFooterGap = 8
const messageFooterActionWidth = 16

type iconReference struct {
	font n.Font
	text string
}

var iconReferenceCenters sync.Map

type iconFontMetrics struct {
	center, ascent, descent float32
}

// Latin uses the cap-height center; Chinese uses the visible ideographic body
// of the system's fallback font. Both retain the text's baseline and leading.
func inlineFontMetrics(label string, font n.Font) iconFontMetrics {
	reference := "H"
	for _, r := range label {
		if unicode.Is(unicode.Han, r) {
			reference = "国"
			break
		}
	}
	key := iconReference{font, reference}
	center, ok := iconReferenceCenters.Load(key)
	if !ok {
		ink := font.InkBounds(reference)
		line := n.ShapeText(reference, font, 0).Lines()[0]
		center, _ = iconReferenceCenters.LoadOrStore(key, iconFontMetrics{-(ink.Y + ink.H/2), line.Ascent, line.Descent})
	}
	return center.(iconFontMetrics)
}

// Center the visible reference glyph rather than the line's ascent/descent box.
// Leading stays symmetric, so the correction is independent of line height.
func inlineCenterOffset(label string, font n.Font) float32 {
	m := inlineFontMetrics(label, font)
	return m.center - (m.ascent-m.descent)/2
}

func inlineIconSpan(label string, font n.Font, size, gap float32) n.Span {
	above := size/2 + inlineFontMetrics(label, font).center
	return n.Span{Text: "\uFFFC", ObjectWidth: size + gap, ObjectHeight: above, ObjectDescent: size - above}
}

func inlineIconText(c *n.Context, icon *n.SVG, label string, font n.Font, size, gap float32, color n.Color) n.Element {
	return n.RichText(c,
		inlineIconSpan(label, font, size, gap),
		n.Span{Text: label},
	).Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(20).SingleLine().TextColor(color).Label(label).
		DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
			r.W = size
			p.Icon(icon, r, color)
		})
}

// A separate icon with text-baseline metrics preserves stable layout columns.
func inlineSymbol(c *n.Context, icon *n.SVG, label string, font n.Font, size float32, color n.Color, rotation ...float32) n.Element {
	return inlineSymbolWithAnimation(c, icon, label, font, size, color, motionAnimation(c, 160*time.Millisecond), rotation...)
}

func inlineSymbolWithAnimation(c *n.Context, icon *n.SVG, label string, font n.Font, size float32, color n.Color, animation n.Animation, rotation ...float32) n.Element {
	m := inlineFontMetrics(label, font)
	e := n.RichText(c, n.Span{Text: "\uFFFC", ObjectWidth: size, ObjectHeight: m.ascent, ObjectDescent: m.descent}).
		Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(20).Role(n.RoleNone)
	angle := float32(0)
	if len(rotation) > 0 {
		angle = e.AnimateTo("disclosure-angle", rotation[0], animation)
	}
	return e.DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
		r.Y += m.ascent - (size/2 + m.center)
		r.H = size
		p.Transform(n.Rotation(angle), r.X+r.W/2, r.Y+r.H/2, func() { p.Icon(icon, r, color) })
	})
}
