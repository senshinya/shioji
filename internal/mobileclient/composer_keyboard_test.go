package mobileclient

import "testing"

func TestComposerKeyboardWaitsForFullKeyboardTarget(t *testing.T) {
	var transition ComposerKeyboardTransition
	for _, sample := range []struct {
		cover, peak, want float32
	}{
		{34, 75, 0},
		{75, 75, 0},
		{75, 355, 0},
		{215, 355, .5},
		{355, 355, 1},
	} {
		if got := transition.Progress(sample.cover, 34, sample.peak); got != sample.want {
			t.Errorf("coverage %g, peak %g: got progress %g, want %g", sample.cover, sample.peak, got, sample.want)
		}
	}
}

func TestComposerKeyboardRetargetsFromDisplayedProgress(t *testing.T) {
	var transition ComposerKeyboardTransition
	for _, sample := range []struct {
		cover, peak, want float32
	}{
		{160, 280, .5},
		{160, 360, .5},
		{260, 360, .75},
		{160, 360, .5},
		{100, 360, .25},
		{160, 360, .5},
		{360, 360, 1},
		{40, 360, 0},
	} {
		if got := transition.Progress(sample.cover, 40, sample.peak); got != sample.want {
			t.Errorf("coverage %g, peak %g: got progress %g, want %g", sample.cover, sample.peak, got, sample.want)
		}
	}
}
