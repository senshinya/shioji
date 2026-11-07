package ui

// TextRangeRects returns the window-space rectangles of a Text or RichText
// range, in rune offsets. Read it while drawing, after layout. It does not
// change text selection or focus; wrapped and bidi ranges may have several boxes.
func (e *node) TextRangeRects(start, end int) []Rect {
	if e == nil || e.kind != kindText || e.tl == nil || start >= end {
		return nil
	}
	var result []Rect
	for _, box := range e.tl.SelectionVisual(start, end, false) {
		result = append(result, Rect{X: e.x + e.contentX() + box.X, Y: e.y + e.contentY() + box.Y, W: box.W, H: box.H})
	}
	return result
}
