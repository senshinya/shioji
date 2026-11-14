package ui

import (
	"math"
	"strconv"
	"testing"
)

func TestListTrailingRevealKeepsThePartialRowsBottomContent(t *testing.T) {
	s := ListState{}
	tester := NewTester(func(c *Context) {
		List(c, &s, 2, func(i int) {
			Column(c).Height(60).Children(func() {
				Text(c, "head"+strconv.Itoa(i)).Height(20)
				Box(c).Height(20)
				Text(c, "tail"+strconv.Itoa(i)).Height(20)
			})
		}).Fill()
	}, 300, 220)
	full, ok := tester.Find("tail1")
	if !ok {
		t.Fatal("missing native content before reveal")
	}
	s.Reveals = []ListReveal{{First: 0, End: 2, Progress: .5, MaxHeight: 100, FromEnd: true}}
	for i := 0; i < 5; i++ {
		tester.Frame()
		clipped, ok := tester.Find("tail1")
		if !ok || math.Abs(float64(clipped.Y-(full.Y-70))) > .5 {
			t.Fatalf("leading clipping moves the wrong part of the row or accumulates on relayout: frame=%d full=%v clipped=%v natural=%v/%v visible=%v/%v offset=%v top=%v anchor=%d inset=%v", i, full, clipped, s.heights.naturalHeight(0), s.heights.naturalHeight(1), s.heights.height(0), s.heights.height(1), s.heights.revealOffset(1), s.heights.top(2, 0), s.anchor, s.inset)
		}
	}
}

func TestListRevealCanCollapseFromItsTrailingControl(t *testing.T) {
	hs := listHeights{n: 102, def: 20, gap: 4}
	hs.reveals = []ListReveal{{First: 1, End: 101, Progress: .5, MaxHeight: 200, FromEnd: true}}
	if got := hs.top(101, 4); got != 124 {
		t.Fatal("trailing reveal has the wrong total height", got)
	}
	// The 100-DIP suffix contains four complete 24-DIP steps and four
	// extra DIPs. Those four are the preceding gap, so row 96 is hidden.
	if hs.height(96) != 0 || hs.height(97) != 20 || hs.top(97, 4) != 28 || hs.top(100, 4) != 100 {
		t.Fatal("trailing reveal exposes the start instead of the latest rows", hs.height(96), hs.top(97, 4), hs.top(100, 4))
	}
	if hs.nextVisible(50, 1) != 97 || hs.nextVisible(50, -1) != 0 {
		t.Fatal("hidden prefix is not skipped in both directions")
	}
	hs.reveals[0].Progress = .625 // 125 DIP, including 1 DIP of row 95.
	if hs.height(95) != 1 || hs.revealOffset(95) != 19 {
		t.Fatal("partial leading row loses its clipped content offset", hs.height(95), hs.revealOffset(95))
	}
}

func TestListRevealCapsOnlyTheAnimatedPrefix(t *testing.T) {
	hs := listHeights{n: 102, def: 20, gap: 4}
	hs.reveals = []ListReveal{{First: 1, End: 101, Progress: .5, MaxHeight: 200}}
	if got := hs.top(101, 4); got != 124 {
		t.Fatal("following rows do not use the capped animated height", got)
	}
	if got := hs.height(5); got != 4 {
		t.Fatal("last exposed row does not clip at the same prefix", got)
	}
	if hs.height(6) != 0 || hs.nextVisible(6, 1) != 101 || hs.nextVisible(90, -1) != 5 {
		t.Fatal("hidden suffix is not skipped in both directions")
	}
	for _, test := range []struct {
		progress, limit float32
		want            float64
	}{
		{0, 200, 24},
		{1, 200, 2424}, // Settled open rows retain their full scrollable extent.
		{.5, 0, 1224},  // Existing callers retain natural-height interpolation.
		{.5, 4000, 1224},
	} {
		hs.reveals[0].Progress, hs.reveals[0].MaxHeight = test.progress, test.limit
		if got := hs.top(101, 4); got != test.want {
			t.Fatalf("progress=%v limit=%v: got %v, want %v", test.progress, test.limit, got, test.want)
		}
	}
}
