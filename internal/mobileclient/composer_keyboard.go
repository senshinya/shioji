package mobileclient

// ComposerKeyboardVisible distinguishes the docked keyboard from the system
// bar and hardware keyboard toolbar when deciding whether to expand the editor.
func ComposerKeyboardVisible(coverage float32) bool {
	return coverage > 80
}

// ComposerKeyboardTransition maps the current keyboard coverage to the empty
// session editor's expansion while UIKit is moving the keyboard.
type ComposerKeyboardTransition struct {
	bottom, peak                   float32
	anchorCoverage, anchorProgress float32
	coverage, progress             float32
}

func (t *ComposerKeyboardTransition) Progress(coverage, bottom, peak float32) float32 {
	if t.peak != peak || t.bottom != bottom {
		t.anchorCoverage = bottom
		t.anchorProgress = 0
		// UIKit can announce another destination before the previous
		// animation ends. Keep the point already displayed on that path.
		if t.peak > t.bottom && t.coverage > bottom && t.coverage < peak {
			t.anchorCoverage = t.coverage
			t.anchorProgress = t.progress
		}
		t.bottom, t.peak = bottom, peak
	}

	switch {
	case !ComposerKeyboardVisible(peak) || coverage <= bottom:
		t.progress = 0
	case coverage >= peak:
		t.progress = 1
	case coverage < t.anchorCoverage:
		t.progress = t.anchorProgress * (coverage - bottom) / (t.anchorCoverage - bottom)
	default:
		fraction := (coverage - t.anchorCoverage) / (peak - t.anchorCoverage)
		t.progress = t.anchorProgress + (1-t.anchorProgress)*fraction
	}

	t.coverage = coverage
	return t.progress
}
