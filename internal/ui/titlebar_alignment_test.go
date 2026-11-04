package ui

import (
	"math"
	"testing"

	n "github.com/egoist/mygo/ui"
)

func TestSearchSharesNativeTrafficLightCenter(t *testing.T) {
	for _, width := range []int{1440, 950} {
		ui := n.NewTester(New(layoutModel(), nil, "").View, width, 900)
		r, ok := ui.Find("搜索")
		// The native red circle's visible top is 23 DIP and diameter is 12.
		if !ok || math.Abs(float64(r.Y+r.H/2-29)) > .5 {
			t.Fatalf("width %d: search center differs from titlebar controls: %+v", width, r)
		}
	}
}
