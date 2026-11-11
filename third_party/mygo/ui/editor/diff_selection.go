package editor

import (
	"github.com/egoist/mygo/ui"
	"sort"
	"strings"
	"unicode/utf8"
)

// DiffTextPoint identifies a byte in one original input (before CRLF
// normalization). DiffTextFragment is half-open and never includes a gutter.
type DiffTextPoint struct {
	Side   DiffSide
	Offset int
}
type DiffTextFragment struct {
	Side               DiffSide
	Start, End         int
	StartLine, EndLine int // one-based source lines
	Text               string
}
type DiffTextSelection struct {
	Fragments []DiffTextFragment
	Text      string
	Bounds    ui.Rect // window coordinates, at the selection's reading end
	Dragging  bool
}
type diffTextSelectionState struct {
	fragments              []DiffTextFragment
	computed               bool
	anchor, head           DiffTextPoint // normalized byte offsets, internally
	active, dragging       bool
	old, new               *Buffer
	oldVersion, newVersion uint64
	preferredX             float32
	hasPreferredX          bool
}
type diffTextRow struct {
	side DiffSide
	line int
}

func (d *DiffState) selectionBuffer(side DiffSide) *Buffer {
	if side == DiffOld {
		return d.Old
	}
	return d.New
}

type diffSourceMap struct {
	buffer  *Buffer
	version uint64
	text    string
	crlf    []int // normalized offsets of CRLF newline characters
}

func (d *DiffState) sourceMap(side DiffSide) *diffSourceMap {
	buffer := d.selectionBuffer(side)
	text, version := buffer.selectionSource()
	m := &d.sourceMaps[side]
	if m.buffer == buffer && m.version == version && m.text == text {
		return m
	}
	*m = diffSourceMap{buffer: buffer, version: version, text: text}
	for i := 0; i+1 < len(text); i++ {
		if text[i] == '\r' && text[i+1] == '\n' {
			m.crlf = append(m.crlf, i-len(m.crlf))
			i++
		}
	}
	return m
}
func (m *diffSourceMap) sourceOffset(offset int) int {
	return offset + sort.Search(len(m.crlf), func(i int) bool { return m.crlf[i] >= offset })
}
func (m *diffSourceMap) normalizedOffset(offset int) int {
	offset = max(0, min(offset, len(m.text)))
	return offset - sort.Search(len(m.crlf), func(i int) bool { return m.crlf[i]+i >= offset })
}
func (d *DiffState) SetTextSelection(anchor, head DiffTextPoint) {
	d.ensure()
	anchor.Offset = d.sourceMap(anchor.Side).normalizedOffset(anchor.Offset)
	head.Offset = d.sourceMap(head.Side).normalizedOffset(head.Offset)
	d.setTextSelection(anchor, head)
}

// SetTextSelectionFragments restores an exact ordered quote, including gaps
// hidden when it was captured. Expanding context never enlarges that quote.
func (d *DiffState) SetTextSelectionFragments(parts []DiffTextFragment) bool {
	d.ensure()
	if len(parts) == 0 {
		d.ClearTextSelection()
		return false
	}
	fragments := make([]DiffTextFragment, 0, len(parts))
	for _, part := range parts {
		if part.Side != DiffOld && part.Side != DiffNew {
			return false
		}
		source := d.sourceMap(part.Side)
		if part.Start < 0 || part.End <= part.Start || part.End > len(source.text) || !utf8.ValidString(source.text[part.Start:part.End]) {
			return false
		}
		part.Start, part.End = source.normalizedOffset(part.Start), source.normalizedOffset(part.End)
		snap := d.selectionBuffer(part.Side).Snapshot()
		part.StartLine, part.EndLine = snap.LineOf(part.Start)+1, snap.LineOf(max(part.Start, part.End-1))+1
		part.Text = ""
		fragments = append(fragments, part)
	}
	first, last := fragments[0], fragments[len(fragments)-1]
	d.setTextSelection(DiffTextPoint{first.Side, first.Start}, DiffTextPoint{last.Side, last.End})
	d.textSelection.fragments, d.textSelection.computed = fragments, true
	return true
}
func (d *DiffState) setTextSelection(anchor, head DiffTextPoint) {
	d.textSelection = diffTextSelectionState{anchor: anchor, head: head, active: true, old: d.Old, new: d.New, oldVersion: d.Old.Version(), newVersion: d.New.Version()}
}
func (d *DiffState) ClearTextSelection() { d.textSelection = diffTextSelectionState{} }
func (d *DiffState) textSelectionValid() bool {
	s := &d.textSelection
	if !s.active {
		return false
	}
	if s.old != d.Old || s.new != d.New || s.oldVersion != d.Old.Version() || s.newVersion != d.New.Version() {
		d.ClearTextSelection()
		return false
	}
	return true
}
func (d *DiffState) selectionRows() []diffTextRow {
	d.ensure()
	rows := []diffTextRow{}
	next := 0
	addNew := func(from, to int) {
		for line := from; line < to; line++ {
			if d.ExpandUnchanged || !d.eds[1].view.heights.isHidden(line) {
				rows = append(rows, diffTextRow{DiffNew, line})
			}
		}
	}
	for _, h := range d.model.hunks {
		addNew(next, h.B0)
		for line := h.A0; line < h.A1; line++ {
			rows = append(rows, diffTextRow{DiffOld, line})
		}
		addNew(h.B0, h.B1)
		next = h.B1
	}
	addNew(next, d.New.LineCount())
	return rows
}

// selectionFragments uses normalized offsets internally, retaining visual
// old/new ordering. Public fragments translate back to original source bytes.
func (d *DiffState) selectionFragments() []DiffTextFragment {
	if !d.textSelectionValid() {
		return nil
	}
	if d.textSelection.computed {
		return d.textSelection.fragments
	}
	d.textSelection.computed = true
	rows := d.selectionRows()
	anchor, head := d.textSelection.anchor, d.textSelection.head
	index := func(point DiffTextPoint) int {
		line := d.selectionBuffer(point.Side).Snapshot().LineOf(point.Offset)
		for i, row := range rows {
			if row.side == point.Side && row.line == line {
				return i
			}
		}
		return -1
	}
	a, z := index(anchor), index(head)
	if a < 0 || z < 0 {
		return nil
	}
	if a > z || a == z && anchor.Offset > head.Offset {
		a, z = z, a
		anchor, head = head, anchor
	}
	var out []DiffTextFragment
	for i := a; i <= z; i++ {
		row := rows[i]
		snap := d.selectionBuffer(row.side).Snapshot()
		start, end := snap.LineStart(row.line), min(snap.Len(), snap.LineEnd(row.line)+1)
		if i == a {
			start = anchor.Offset
		}
		if i == z {
			end = head.Offset
		}
		if end <= start {
			continue
		}
		if len(out) > 0 && out[len(out)-1].Side == row.side && out[len(out)-1].End == start {
			out[len(out)-1].End = end
			out[len(out)-1].EndLine = row.line + 1
			continue
		}
		out = append(out, DiffTextFragment{Side: row.side, Start: start, End: end, StartLine: row.line + 1, EndLine: row.line + 1})
	}
	d.textSelection.fragments = out
	return out
}
func (d *DiffState) SelectedText() (DiffTextSelection, bool) {
	var out DiffTextSelection
	for _, f := range d.selectionFragments() {
		source := d.sourceMap(f.Side)
		f.Start, f.End = source.sourceOffset(f.Start), source.sourceOffset(f.End)
		f.Text = source.text[f.Start:f.End]
		if len(out.Fragments) > 0 && !strings.HasSuffix(out.Text, "\n") {
			out.Text += "\n"
		}
		out.Text += f.Text
		out.Fragments = append(out.Fragments, f)
	}
	out.Dragging = d.textSelection.dragging
	if len(out.Fragments) == 0 {
		return out, false
	}
	out.Bounds = d.textSelectionBounds()
	return out, true
}
func (d *DiffState) copyTextSelection() bool {
	selection, ok := d.SelectedText()
	if !ok {
		return false
	}
	d.editorOf(DiffNew).services.WriteClipboard(selection.Text)
	return true
}
func (d *DiffState) selectionPoint(ed *editor, x, y float32) (DiffTextPoint, bool) {
	ed.sync()
	cx, cy := ed.toContent(x, y)
	if block, r, ok := ed.blockAt(cy); ok {
		if block.kind != blockDeleted {
			return DiffTextPoint{}, false
		}
		view := &d.oldView
		top := r.Y
		for line := block.from; line < block.to; line++ {
			layout := view.line(line)
			height := float32(len(layout.rows)) * view.lineHeight
			if y < top+height || line == block.to-1 {
				row := min(max(0, int((y-top)/view.lineHeight)), len(layout.rows)-1)
				display := view.dispAt(layout, &layout.rows[row], cx)
				return DiffTextPoint{DiffOld, layout.start + layout.fromDisp(display)}, true
			}
			top += height + d.annotationHeight(DiffOld, line)
		}
	}
	return DiffTextPoint{DiffNew, ed.offsetAt(cx, cy)}, true
}
func (d *DiffState) textSelectionBounds() ui.Rect {
	fs := d.selectionFragments()
	if len(fs) == 0 {
		return ui.Rect{}
	}
	f := fs[len(fs)-1]
	ed := &d.eds[1]
	if ed.view.lineHeight <= 0 {
		return ui.Rect{}
	}
	offset := max(f.Start, f.End-1)
	if f.Side == DiffNew {
		r, ok := ed.caretRect(offset)
		if !ok {
			return ui.Rect{}
		}
		r.X += ed.r.X + ed.textX - ed.scrollX
		r.Y += ed.r.Y - float32(ed.scrollY)
		return r
	}
	view := &d.oldView
	line := view.text.LineOf(offset)
	layout := view.line(line)
	disp := layout.toDisp(offset-layout.start, true)
	row := layout.rowOf(disp, false)
	for i := range ed.view.heights.blocks {
		b := &ed.view.heights.blocks[i]
		if b.kind != blockDeleted || line < b.from || line >= b.to {
			continue
		}
		above, below := ed.view.heights.blocksOf(b.line)
		geom := ed.view.heights.geometry(b.line)
		top, blocks := geom.top, above
		if b.below {
			top, blocks = geom.below, below
		}
		for _, item := range blocks {
			if item.kind == b.kind && item.from == b.from && item.to == b.to {
				break
			}
			top += float64(item.height)
		}
		for l := b.from; l < line; l++ {
			top += float64(len(view.line(l).rows))*float64(view.lineHeight) + float64(d.annotationHeight(DiffOld, l))
		}
		return ui.Rect{X: ed.r.X + ed.textX - ed.scrollX + view.xOf(layout, &layout.rows[row], disp), Y: ed.r.Y + float32(top-ed.scrollY) + float32(row)*view.lineHeight, W: 2, H: view.lineHeight}
	}
	return ui.Rect{}
}
func (d *DiffState) notifyTextSelection() {
	if d.OnTextSelection != nil {
		selection, _ := d.SelectedText()
		d.OnTextSelection(selection)
	}
}
func (d *DiffState) textSelectionInput(ed *editor, ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputPointerDown:
		if ev.Button != 0 || ed.regionAt(ev.X, ev.Y) != regionText {
			return false
		}
		point, ok := d.selectionPoint(ed, ev.X, ev.Y)
		if !ok {
			d.ClearTextSelection()
			return false
		}
		anchor := point
		if ev.Mods&ui.Shift != 0 && d.textSelectionValid() {
			anchor = d.textSelection.anchor
		}
		d.setTextSelection(anchor, point)
		d.textSelection.dragging = true
		ed.gesture.active = false
		if ev.Clicks == 2 {
			view := &ed.view
			if point.Side == DiffOld {
				view = &d.oldView
			}
			a, z := wordAt(view.text, point.Offset, ed.wordChars())
			d.textSelection.anchor.Offset, d.textSelection.head.Offset = a, z
		}
		if ev.Clicks >= 3 {
			snap := d.selectionBuffer(point.Side).Snapshot()
			line := snap.LineOf(point.Offset)
			d.textSelection.anchor.Offset, d.textSelection.head.Offset = snap.LineStart(line), snap.LineEnd(line)
		}
		d.notifyTextSelection()
		return true
	case ui.InputPointerMove:
		if !d.textSelection.dragging {
			return false
		}
		if dy := past(ev.Y, 0, ed.viewH); dy != 0 {
			ed.setScrollShared(ed.scrollY + float64(edgeSpeed(dy)))
		}
		if point, ok := d.selectionPoint(ed, ev.X, ev.Y); ok {
			d.textSelection.head = point
			d.textSelection.computed = false
		}
		d.notifyTextSelection()
		return true
	case ui.InputPointerUp:
		if !d.textSelection.dragging {
			return false
		}
		d.textSelection.dragging = false
		d.notifyTextSelection()
		return true
	case ui.InputCommand:
		if ev.Text == "selectAll" {
			d.selectAllText()
			return true
		}
		if ev.Text == "copy" {
			return d.copyTextSelection()
		}
	case ui.InputKeyDown:
		if ev.Mods&ui.Cmd != 0 && ev.Key == ui.KeyA {
			d.selectAllText()
			return true
		}

		if ev.Mods&ui.Cmd != 0 && ev.Key == ui.KeyC {
			return d.copyTextSelection()
		}
		if ev.Key == ui.KeyEscape {
			d.ClearTextSelection()
			d.notifyTextSelection()
			return false
		}
		if !d.textSelectionValid() {
			return false
		}
		if ev.Key == ui.KeyLeft || ev.Key == ui.KeyRight || ev.Key == ui.KeyUp || ev.Key == ui.KeyDown || ev.Key == ui.KeyHome || ev.Key == ui.KeyEnd {
			point := d.textSelection.head
			snap := d.selectionBuffer(point.Side).Snapshot()
			line := snap.LineOf(point.Offset)
			if ev.Mods&(ui.Shift|ui.Alt|ui.Cmd) == 0 && (ev.Key == ui.KeyLeft || ev.Key == ui.KeyRight) {
				parts := d.selectionFragments()
				if len(parts) > 0 {
					part := parts[0]
					offset := part.Start
					if ev.Key == ui.KeyRight {
						part = parts[len(parts)-1]
						offset = part.End
					}
					point = DiffTextPoint{part.Side, offset}
					d.setTextSelection(point, point)
					d.notifyTextSelection()
					return true
				}
			}
			switch ev.Key {
			case ui.KeyLeft:
				switch {
				case ev.Mods&ui.Cmd != 0:
					point.Offset = snap.LineStart(line)
				case point.Offset == snap.LineStart(line):
					point = d.adjacentTextRow(point, -1)
				case ev.Mods&ui.Alt != 0:
					point.Offset = max(snap.LineStart(line), wordLeft(snap.t, point.Offset, ed.wordChars(), false))
				default:
					point.Offset = graphemeLeft(snap.t, point.Offset)
				}
			case ui.KeyRight:
				switch {
				case ev.Mods&ui.Cmd != 0:
					point.Offset = snap.LineEnd(line)
				case point.Offset == snap.LineEnd(line):
					point = d.adjacentTextRow(point, 1)
				case ev.Mods&ui.Alt != 0:
					point.Offset = min(snap.LineEnd(line), wordRight(snap.t, point.Offset, ed.wordChars(), false))
				default:
					point.Offset = graphemeRight(snap.t, point.Offset)
				}

			case ui.KeyHome:
				point.Offset = snap.LineStart(line)
			case ui.KeyEnd:
				point.Offset = snap.LineEnd(line)
			default:
				delta := 1
				if ev.Key == ui.KeyUp {
					delta = -1
				}
				point = d.verticalTextPoint(point, delta)
			}
			if ev.Key != ui.KeyUp && ev.Key != ui.KeyDown {
				d.textSelection.hasPreferredX = false
			}
			// Never place a UTF-8 caret in the middle of a code point.
			text := d.selectionBuffer(point.Side).Snapshot().String()
			for point.Offset > 0 && point.Offset < len(text) && !utf8.RuneStart(text[point.Offset]) {
				point.Offset--
			}
			if ev.Mods&ui.Shift == 0 {
				d.textSelection.anchor = point
			}
			d.textSelection.head = point
			d.textSelection.computed = false
			d.notifyTextSelection()
			return true
		}
	}
	return false
}

// ScrollY and SetScrollY expose the unified viewer's vertical reading position.
func (d *DiffState) ScrollY() float64     { return d.eds[1].scrollY }
func (d *DiffState) SetScrollY(y float64) { d.eds[1].setScrollShared(y) }
func (d *DiffState) TextSelectionScrollTarget() (float64, bool) {
	selection, ok := d.SelectedText()
	ed := &d.eds[1]
	if !ok || selection.Bounds.H <= 0 || ed.r.H <= 0 {
		return 0, false
	}
	target := ed.scrollY + float64(selection.Bounds.Y-ed.r.Y-ed.r.H*.4)
	return min(max(0, target), ed.scrollMax()), true
}

func (d *DiffState) adjacentTextRow(point DiffTextPoint, direction int) DiffTextPoint {
	rows := d.selectionRows()
	line := d.selectionBuffer(point.Side).Snapshot().LineOf(point.Offset)
	for i, row := range rows {
		if row.side == point.Side && row.line == line {
			next := i + direction
			if next < 0 || next >= len(rows) {
				return point
			}
			row = rows[next]
			snap := d.selectionBuffer(row.side).Snapshot()
			offset := snap.LineStart(row.line)
			if direction < 0 {
				offset = snap.LineEnd(row.line)
			}
			return DiffTextPoint{row.side, offset}
		}
	}
	return point
}
func (d *DiffState) selectAllText() {
	d.ExpandAll()
	rows := d.selectionRows()
	if len(rows) == 0 {
		return
	}
	first, last := rows[0], rows[len(rows)-1]
	d.setTextSelection(DiffTextPoint{first.side, d.selectionBuffer(first.side).Snapshot().LineStart(first.line)}, DiffTextPoint{last.side, d.selectionBuffer(last.side).Len()})
	d.notifyTextSelection()
}

func (d *DiffState) verticalTextPoint(point DiffTextPoint, direction int) DiffTextPoint {
	rows := d.selectionRows()
	line := d.selectionBuffer(point.Side).Snapshot().LineOf(point.Offset)
	viewFor := func(side DiffSide) *view {
		if side == DiffOld {
			return &d.oldView
		}
		return &d.eds[1].view
	}
	for i, sourceRow := range rows {
		if sourceRow.side != point.Side || sourceRow.line != line {
			continue
		}
		v := viewFor(point.Side)
		layout := v.line(line)
		disp := layout.toDisp(point.Offset-layout.start, true)
		row := layout.rowOf(disp, false)
		if !d.textSelection.hasPreferredX {
			d.textSelection.preferredX = v.xOf(layout, &layout.rows[row], disp)
			d.textSelection.hasPreferredX = true
		}
		row += direction
		if row < 0 || row >= len(layout.rows) {
			next := i + direction
			if next < 0 || next >= len(rows) {
				return point
			}
			sourceRow = rows[next]
			v = viewFor(sourceRow.side)
			layout = v.line(sourceRow.line)
			row = 0
			if direction < 0 {
				row = len(layout.rows) - 1
			}
		}
		offset := layout.start + layout.fromDisp(v.dispAt(layout, &layout.rows[row], d.textSelection.preferredX))
		return DiffTextPoint{sourceRow.side, offset}
	}
	return point
}
