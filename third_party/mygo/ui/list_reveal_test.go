package ui

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func TestListRevealDoesNotTreatEmptyRowsAsClippedContent(t *testing.T) {
	for _, fromEnd := range []bool{false, true} {
		hs := listHeights{n: 5, def: 20}
		for i, height := range []float32{20, 0, 0, 20, 40} {
			hs.set(i, height)
		}
		hs.reveals = []ListReveal{{First: 0, End: 5, Progress: .875, FromEnd: fromEnd}}
		for _, direction := range []int{-1, 1} {
			if got := hs.nextVisible(2, direction); got != 2 {
				t.Errorf("fromEnd=%v direction=%d: empty row moved to %d; clipping must not skip visible neighbours", fromEnd, direction, got)
			}
		}
	}
}

func TestListRevealKeepsEmptyRowsScrollableDuringAnimation(t *testing.T) {
	s := ListState{}
	heights := []float32{20, 0, 0, 20, 40}
	tester := NewTester(func(c *Context) {
		List(c, &s, len(heights), func(i int) {
			Column(c).Height(heights[i]).Children(func() {
				if heights[i] > 0 {
					Text(c, "row"+strconv.Itoa(i))
				}
			})
		}).Fill()
	}, 300, 220)
	// Fail before entering layout on the old implementation, which otherwise
	// loops forever between rows 3 and 4 while covering the viewport upwards.
	s.Reveals = []ListReveal{{First: 0, End: 5, Progress: .875}}
	s.heights.reveals = s.Reveals
	if got := s.heights.nextVisible(2, -1); got > 2 {
		t.Fatalf("backward traversal moves forward to row %d", got)
	}
	for _, fromEnd := range []bool{false, true} {
		s.Reveals[0].FromEnd = fromEnd
		for _, progress := range []float32{.875, .5, 0, .25, 1} {
			s.Reveals[0].Progress = progress
			s.ScrollTo(4, End)
			tester.Advance(20 * time.Millisecond)
			if progress == 1 {
				for _, name := range []string{"row0", "row3", "row4"} {
					if _, ok := tester.Find(name); !ok {
						t.Fatalf("fromEnd=%v: visible neighbour %s was skipped", fromEnd, name)
					}
				}
			}
		}
	}
}

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

func TestListRevealCanReserveSpaceForALiftedRow(t *testing.T) {
	state := ListState{}
	source := Rect{X: 72, Y: 156, W: 100, H: 24}
	ink := RGB(205, 30, 69)
	sending := false
	tester := NewTester(func(c *Context) {
		state.Key = func(index int) any {
			if sending && index == 1 {
				return "sent"
			}
			if index == 0 {
				return "history"
			}
			return "footer"
		}
		rows := 2
		if sending {
			rows++
		}
		List(c, &state, rows, func(index int) {
			switch state.Key(index) {
			case "history":
				Box(c).Height(48)
			case "sent":
				Column(c).Height(60).Children(func() {
					Box(c).Size(100, 24).Background(ink).Transition(ElementTransition{
						From: &source, Position: true, Lift: true,
						Duration: 480 * time.Millisecond, Ease: Linear,
					})
				})
			case "footer":
				Text(c, "Footer").Height(24)
			}
		}).Fill()
	}, 260, 220)
	footer, ok := tester.Find("Footer")
	if !ok {
		t.Fatal("missing the original footer")
	}
	sending = true
	state.Reveals = []ListReveal{{First: 1, End: 2, Progress: 0, LayoutOnly: true}}
	tester.Frame()
	r, g, b, _ := tester.Image().At(80, 164).RGBA()
	if r>>8 != uint32(ink.R) || g>>8 != uint32(ink.G) || b>>8 != uint32(ink.B) {
		t.Fatalf("the first frame did not paint the lifted row at its source: %02x%02x%02x", r>>8, g>>8, b>>8)
	}
	after, ok := tester.Find("Footer")
	if !ok || math.Abs(float64(after.Y-footer.Y)) > .5 {
		t.Fatalf("the lifted row moved the footer before it occupied any height: %v -> %v", footer, after)
	}
}
