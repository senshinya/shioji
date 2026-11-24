package conversationui

import n "github.com/egoist/mygo/ui"

// MessageFlightOrigin centers a full-size bubble at a smaller action control.
// Native retained-source transitions lay out text at the intermediate widths.
func MessageFlightOrigin(anchor n.Rect, width, height float32) n.Rect {
	if anchor.W <= 0 || anchor.H <= 0 {
		return n.Rect{}
	}
	return n.Rect{X: anchor.X + (anchor.W-width)/2, Y: anchor.Y + (anchor.H-height)/2, W: width, H: height}
}
