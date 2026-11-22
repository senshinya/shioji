package mobileclient

import "testing"

func TestQueueViewportShowsPartialThirdRow(t *testing.T) {
	for _, tc := range []struct {
		count int
		want  float32
	}{{0, 0}, {1, 50}, {2, 100}, {3, 125}, {24, 125}} {
		if got := QueueViewport(tc.count); got != tc.want {
			t.Errorf("QueueViewport(%d) = %g, want %g", tc.count, got, tc.want)
		}
	}
}

func TestPhoneNeverUsesSidebarAndTabletFoldsAtCompactWidth(t *testing.T) {
	if UsesSidebar(false, 950) || UsesSidebar(true, 680) || !UsesSidebar(true, 1000) {
		t.Fatal("split should be reserved for wide iPad windows")
	}
}

func TestComposerGeometryMovesOneSetOfControls(t *testing.T) {
	compact := ComposerGeometry(0, false, 18.6, 44)
	open := ComposerGeometry(1, false, 18.6, 44)
	half := ComposerGeometry(0.5, false, 18.6, 44)
	if compact.Height != 56 || compact.ControlsY != 6 || open.Height != 130 || open.ControlsY != 80 {
		t.Fatalf("unexpected endpoint geometry: compact = %+v open = %+v", compact, open)
	}
	if !(half.ControlsY > compact.ControlsY && half.ControlsY < open.ControlsY && half.TextInset < compact.TextInset && half.TextInset > open.TextInset) {
		t.Fatalf("controls or editor jumped at the midpoint: %+v", half)
	}
}

func TestDraftComposerKeepsControlsExpanded(t *testing.T) {
	idle, focused := ComposerGeometry(-1, true, 18.6, 44), ComposerGeometry(2, true, 18.6, 44)
	if idle.Height != 92 || idle.ControlsY != 43 || focused.Height != 130 || focused.ControlsY != 80 {
		t.Fatalf("unexpected draft geometry: idle = %+v focused = %+v", idle, focused)
	}
	for _, metrics := range []ComposerMetrics{idle, focused} {
		if metrics.ControlsProgress != 1 || metrics.TextInset != 12 || metrics.TextTop != 4 || metrics.TextRight != 0 {
			t.Fatalf("draft lost its expanded controls: %+v", metrics)
		}
	}
}

func TestComposerReservesAnEnlargedLineAboveTheControls(t *testing.T) {
	const lineHeight float32 = 45
	for _, draft := range []bool{false, true} {
		for _, progress := range []float32{0, .5, 1} {
			metrics := ComposerGeometry(progress, draft, lineHeight, 44)
			editorBottom := metrics.TextTop + metrics.EditorHeight - ComposerEditorPadding
			if metrics.EditorHeight-2*ComposerEditorPadding < lineHeight || editorBottom > metrics.ControlsY {
				t.Fatalf("draft=%v progress=%g: %v", draft, progress, metrics)
			}
			if metrics.ControlsProgress != 1 || metrics.TextRight != 0 {
				t.Fatalf("the enlarged editor competes with inline controls: %v", metrics)
			}
		}
	}
}

func TestZoomedOutComposerReservesRoomForTouchTargets(t *testing.T) {
	const touchSize = 44 / .75
	for _, draft := range []bool{false, true} {
		for _, progress := range []float32{0, .5, 1} {
			metrics := ComposerGeometry(progress, draft, 18.6, touchSize)
			if metrics.Height-metrics.ControlsY < touchSize {
				t.Fatalf("draft=%v progress=%g: controls escape their surface: %+v", draft, progress, metrics)
			}
		}
	}
}
