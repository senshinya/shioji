// Package mobileclient contains the iOS and iPadOS client's platform-neutral code.
package mobileclient

const QueueRowHeight float32 = 50
const SidebarMinWidth = 840
const ComposerEditorPadding = 10

// QueueViewport leaves the third row partially visible to indicate overflow.
func QueueViewport(count int) float32 {
	return min(float32(count), 2.5) * QueueRowHeight
}

func UsesSidebar(tablet bool, width float32) bool {
	return tablet && width >= SidebarMinWidth
}

type ComposerMetrics struct {
	Height, ControlsY              float32
	TextInset, TextTop, TextRight  float32
	EditorHeight, ControlsProgress float32
}

func ComposerGeometry(progress float32, draft bool, lineHeight, touchSize float32) ComposerMetrics {
	progress = max(0, min(progress, 1))
	metrics := ComposerMetrics{
		Height: 56 + 74*progress, ControlsY: 6 + 74*progress,
		TextInset: 50 - 38*progress, TextTop: 8 - 4*progress, TextRight: 136 * (1 - progress),
		EditorHeight: 44 + 33*progress, ControlsProgress: progress,
	}
	// When an enlarged line no longer fits in the compact editor, put the
	// controls on their own row and reserve room for the actual font metrics.
	extra := max(0, lineHeight+2*ComposerEditorPadding-44) * (1 + progress)
	if draft || extra > 0 {
		metrics.Height, metrics.ControlsY = 92+38*progress, 43+37*progress
		metrics.TextInset, metrics.TextTop, metrics.TextRight, metrics.ControlsProgress = 12, 4, 0, 1
	}
	metrics.Height += extra
	metrics.ControlsY += extra
	metrics.EditorHeight += extra
	startSpace := max(0, touchSize-44)
	metrics.Height += startSpace
	inlineSpace := startSpace * (1 - metrics.ControlsProgress)
	metrics.EditorHeight += inlineSpace
	metrics.TextInset += inlineSpace
	metrics.TextRight += 2 * inlineSpace
	return metrics
}
