package editor

import "github.com/egoist/mygo/ui"

type DiffTextHighlight struct {
	Side       DiffSide
	Start, End int
	Color      ui.Color
}

func (d *DiffState) highlightBackgrounds(side DiffSide) []TextBackground {
	var out []TextBackground
	for _, h := range d.TextHighlights {
		if h.Side != side {
			continue
		}
		source := d.sourceMap(side)
		if h.Start < 0 || h.End <= h.Start || h.End > len(source.text) {
			continue
		}
		out = append(out, TextBackground{Start: source.normalizedOffset(h.Start), End: source.normalizedOffset(h.End), Color: h.Color})
	}
	return out
}
