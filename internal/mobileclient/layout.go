// Package mobileclient contains the iOS and iPadOS client's platform-neutral code.
package mobileclient

const QueueRowHeight float32 = 44
const SidebarMinWidth = 840
const (
	ComposerEditorPadding = 10
	ComposerControlInset  = 5
	ComposerControlGap    = 2
	ComposerModelSendGap  = 8
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
	ControlsOpacity                float32
}

func ComposerGeometry(progress float32, draft bool, lineHeight, touchSize float32) ComposerMetrics {
	progress = max(0, min(progress, 1))
	// The compact editor shares the row with the attachment and send targets.
	inlineControls := touchSize + ComposerControlInset + ComposerControlGap
	metrics := ComposerMetrics{
		Height: 56 + 52*progress, ControlsY: 6 + 52*progress,
		TextInset: 12 + (inlineControls-12)*(1-progress), TextTop: 8 - 4*progress,
		TextRight:    inlineControls * (1 - progress),
		EditorHeight: 44 + 11*progress, ControlsProgress: progress,
	}
	// When an enlarged line no longer fits in the compact editor, put the
	// controls on their own row and reserve room for the actual font metrics.
	extra := max(0, lineHeight+2*ComposerEditorPadding-44) * (1 + progress)
	if draft || extra > 0 {
		metrics.Height, metrics.ControlsY = 92+16*progress, 43+15*progress
		metrics.TextInset, metrics.TextTop, metrics.TextRight, metrics.ControlsProgress = 12, 4, 0, 1
	}
	metrics.Height += extra
	metrics.ControlsY += extra
	metrics.EditorHeight += extra
	startSpace := max(0, touchSize-44)
	metrics.Height += startSpace
	inlineSpace := startSpace * (1 - metrics.ControlsProgress)
	metrics.EditorHeight += inlineSpace
	// Reveal the options after the editor starts clearing their row.
	metrics.ControlsOpacity = max(0, 2*metrics.ControlsProgress-1)
	return metrics
}
