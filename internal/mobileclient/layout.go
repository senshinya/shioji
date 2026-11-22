// Package mobileclient contains the iOS and iPadOS client's platform-neutral code.
package mobileclient

const QueueRowHeight float32 = 50
const SidebarMinWidth = 840
const (
	ComposerEditorPadding = 10
	ComposerControlInset  = 5
	ComposerControlGap    = 2
)

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
	ModelSendSpacing               float32
}

func ComposerGeometry(progress float32, draft bool, lineHeight, touchSize float32) ComposerMetrics {
	progress = max(0, min(progress, 1))
	// The compact editor shares the row with two touch targets at each edge.
	inlineControls := 2*touchSize + ComposerControlInset + ComposerControlGap
	// The filled send button reaches farther than the left-hand glyphs.
	// The compact model icon already has its own inset within its touch target.
	const compactSpacing, expandedSpacing = 14, 22
	modelSendSpacing := compactSpacing + (expandedSpacing-compactSpacing)*progress
	metrics := ComposerMetrics{
		Height: 56 + 74*progress, ControlsY: 6 + 74*progress,
		TextInset: 12 + (inlineControls-12)*(1-progress), TextTop: 8 - 4*progress,
		TextRight:    (inlineControls + modelSendSpacing) * (1 - progress),
		EditorHeight: 44 + 33*progress, ControlsProgress: progress,
		ModelSendSpacing: modelSendSpacing,
	}
	// When an enlarged line no longer fits in the compact editor, put the
	// controls on their own row and reserve room for the actual font metrics.
	extra := max(0, lineHeight+2*ComposerEditorPadding-44) * (1 + progress)
	if draft || extra > 0 {
		metrics.Height, metrics.ControlsY = 92+38*progress, 43+37*progress
		metrics.TextInset, metrics.TextTop, metrics.TextRight, metrics.ControlsProgress = 12, 4, 0, 1
		metrics.ModelSendSpacing = expandedSpacing
	}
	metrics.Height += extra
	metrics.ControlsY += extra
	metrics.EditorHeight += extra
	startSpace := max(0, touchSize-44)
	metrics.Height += startSpace
	inlineSpace := startSpace * (1 - metrics.ControlsProgress)
	metrics.EditorHeight += inlineSpace
	return metrics
}
