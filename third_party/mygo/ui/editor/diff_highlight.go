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

// UnifiedTextFragments maps the unchanged portions of a base-side quote to
// the new-side rows that draw them in a unified view. Deleted portions stay
// on the base side. Returned offsets are in the original, non-normalized
// buffers. Call after Comparing is false.
func (d *DiffState) UnifiedTextFragments(part DiffTextFragment) []DiffTextFragment {
	if d.Layout != DiffUnified || part.Side != DiffOld {
		return []DiffTextFragment{part}
	}
	d.ensure()
	oldSource, newSource := d.sourceMap(DiffOld), d.sourceMap(DiffNew)
	start, end := oldSource.normalizedOffset(part.Start), oldSource.normalizedOffset(part.End)
	if end <= start {
		return nil
	}
	old, newText := d.Old.Snapshot(), d.New.Snapshot()
	var projected []DiffTextFragment
	for line, last := old.LineOf(start), old.LineOf(end-1); line <= last; line++ {
		from := old.LineStart(line)
		to := old.Len()
		if line+1 < old.LineCount() {
			to = old.LineStart(line + 1)
		}
		first, final := max(start, from), min(end, to)
		side, source := DiffOld, oldSource
		if mapped, ok := d.model.newLineOf(line); ok {
			delta := newText.LineStart(mapped) - from
			side, source = DiffNew, newSource
			first, final = first+delta, final+delta
		}
		first, final = source.sourceOffset(first), source.sourceOffset(final)
		if n := len(projected); n > 0 && projected[n-1].Side == side && projected[n-1].End == first {
			projected[n-1].End = final
		} else {
			projected = append(projected, DiffTextFragment{Side: side, Start: first, End: final})
		}
	}
	return projected
}
