package markdown

import (
	"sort"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// RangeDecoration decorates the visible glyphs of source bytes [Start, End).
// Draw receives window-space text boxes; it may paint or observe their geometry.
type RangeDecoration struct {
	Start, End int
	Draw       func(*ui.Painter, ui.Rect)
}

func (st *viewState) decorate(c *ui.Context, el ui.Element, block, line, runes int) {
	h := st.o.DecorateRange
	if h == nil || h.Draw == nil || h.Start >= h.End {
		return
	}
	b := st.doc.Block(block)
	if h.End <= b.Start || h.Start >= b.End {
		return
	}
	inf := elemInfo{block: block, line: line}
	start := sort.Search(runes, func(i int) bool { return st.srcAt(inf, i+1, true) > h.Start })
	end := sort.Search(runes, func(i int) bool { return st.srcAt(inf, i, false) >= h.End })
	if start >= end {
		return
	}
	draw := h.Draw
	handle := ui.Local(el, "range-decoration-handle", func() ui.Handle { return ui.Handle{} })
	el.Bind(handle)
	el.Draw(func(p *ui.Painter, _ ui.Rect) {
		for _, r := range c.Resolve(*handle).TextRangeRects(start, end) {
			draw(p, r)
		}
	})
}

func spanRunes(spans []ui.Span) int {
	n := 0
	for _, span := range spans {
		n += utf8.RuneCountInString(span.Text)
	}
	return n
}
