package ui

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestReadingScrollStartsAtTheCurrentOffsetAndKeepsRowsVirtual(t *testing.T) {
	s := ListState{FollowEnd: true}
	built := 0
	tt := NewTester(func(c *Context) {
		built = 0
		List(c, &s, 100, func(i int) {
			built++
			Column(c).Height(40)
		}).Fill()
	}, 400, 400)
	before := s.wroteY
	if before < 3000 {
		t.Fatal("fixture did not start at the end", before)
	}
	s.ScrollToRow(10, 80, Animation{Duration: 300 * time.Millisecond, Ease: Linear})
	tt.Frame()
	if math.Abs(s.wroteY-before) > 1 {
		t.Fatal("reading scroll jumped before the animation", before, s.wroteY)
	}
	tt.Advance(100 * time.Millisecond)
	if s.wroteY >= before || s.wroteY <= 320 || built > 30 {
		t.Fatal("reading scroll did not interpolate virtual rows", before, s.wroteY, built)
	}
	middle := s.wroteY
	s.ScrollToRow(95, 80, Animation{Duration: 300 * time.Millisecond, Ease: Linear})
	tt.Frame()
	if math.Abs(s.wroteY-middle) > 1 {
		t.Fatal("retargeting a reading scroll jumped", middle, s.wroteY)
	}
	tt.Advance(400 * time.Millisecond)
	if math.Abs(s.wroteY-3720) > 1 || s.FollowingEnd() || built > 30 {
		t.Fatal("reading scroll did not finish at its stable target", s.wroteY, built)
	}
}

func TestAnimatedScrollToEndKeepsItsPlaceAndFollowsNewRows(t *testing.T) {
	s := ListState{FollowEnd: true}
	count, built := 1000, 0
	tt := NewTester(func(c *Context) {
		built = 0
		List(c, &s, count, func(i int) {
			built++
			Column(c).Height(40)
		}).Fill()
	}, 400, 400)
	s.ScrollTo(10, Start)
	tt.Frame()
	before := s.wroteY
	s.ScrollToEnd(Animation{Duration: 300 * time.Millisecond, Ease: Linear})
	if !s.FollowingEnd() {
		t.Fatal("pending end scroll is incorrectly reported as history reading")
	}
	tt.Frame()
	if math.Abs(s.wroteY-before) > 1 {
		t.Fatal("animated end scroll jumps before its first frame", before, s.wroteY)
	}
	tt.Advance(100 * time.Millisecond)
	if !s.FollowingEnd() {
		t.Fatal("animated end scroll is incorrectly reported as history reading")
	}
	if s.wroteY <= before || s.AtEnd() || built > 35 {
		t.Fatal("end scroll does not interpolate virtual rows", s.wroteY, built)
	}
	middle := s.wroteY
	count += 3
	tt.Frame()
	if math.Abs(s.wroteY-middle) > 1 {
		t.Fatal("new rows jump an end scroll already on its way", middle, s.wroteY)
	}
	tt.Advance(400 * time.Millisecond)
	if !s.AtEnd() || !s.FollowingEnd() || built > 35 {
		t.Fatal("end scroll did not hand back to following", s.wroteY, built)
	}
	count++
	tt.Frame()
	if !s.AtEnd() || !s.FollowingEnd() {
		t.Fatal("settled end scroll does not follow subsequent output")
	}
}

func TestReaderInterruptsAnimatedEndFollowing(t *testing.T) {
	s := ListState{FollowEnd: true}
	tt := NewTester(func(c *Context) {
		List(c, &s, 100, func(i int) { Column(c).Height(40) }).Fill()
	}, 400, 400)
	s.ScrollTo(10, Start)
	tt.Frame()
	s.ScrollToEnd(Animation{Duration: 300 * time.Millisecond, Ease: Linear})
	tt.Advance(100 * time.Millisecond)
	if !s.FollowingEnd() {
		t.Fatal("animated end scroll did not resume following")
	}
	tt.Scroll(200, 200, 0, -120)
	tt.Frame()
	y := s.wroteY
	tt.Advance(time.Second)
	if s.FollowingEnd() || math.Abs(s.wroteY-y) > 1 {
		t.Fatal("reader scroll did not interrupt animated end following", y, s.wroteY)
	}
}

func TestAnimatedEndFollowsNewRowsAfterListRemount(t *testing.T) {
	count, mount := 60, "first"
	s := ListState{FollowEnd: true, Key: func(i int) any { return i }}
	tt := NewTester(func(c *Context) {
		List(c.Key(mount), &s, count, func(i int) {
			Text(c, fmt.Sprint("message ", i)).Height(40)
		}).Fill()
	}, 400, 400)
	before, _ := s.ScrollEdges()
	if before == 0 || !s.AtEnd() {
		t.Fatal("fixture did not start at the bottom of a long reply")
	}

	mount = "conversation"
	count++
	s.ScrollToEnd(Animation{Duration: 300 * time.Millisecond, Ease: Linear})
	tt.Frame()
	tt.Advance(time.Second)
	followUp, visible := tt.Find("message 60")
	if !visible || followUp.Y+followUp.H > 400.5 || !s.AtEnd() || !s.FollowingEnd() {
		t.Fatalf("follow-up after remount: bounds=%+v visible=%t atEnd=%t following=%t",
			followUp, visible, s.AtEnd(), s.FollowingEnd())
	}
}

func TestAnimatedEndScrollKeepsVisibleRowsWhenPrefixEstimatesChange(t *testing.T) {
	for _, atEnd := range []bool{false, true} {
		t.Run(fmt.Sprint(atEnd), func(t *testing.T) {
			count := 1000
			s := ListState{FollowEnd: true, Key: func(i int) any { return i }}
			tt := NewTester(func(c *Context) {
				List(c, &s, count, func(i int) {
					height := float32(24 + i%5*7)
					if i == 1000 {
						height = 180
					}
					Text(c, fmt.Sprint("row-", i)).Height(height)
				}).Fill()
			}, 400, 400)
			marker := "row-999"
			if !atEnd {
				s.ScrollTo(15, Start)
				tt.Frame()
				marker = "row-15"
			}
			tt.Advance(time.Second)
			before, ok := tt.Find(marker)
			if !ok {
				t.Fatal("missing initial marker")
			}
			count++
			s.ScrollToEnd(Animation{Duration: 300 * time.Millisecond, Ease: Linear})
			tt.Frame()
			first, ok := tt.Find(marker)
			if !ok || math.Abs(float64(first.Y-before.Y)) > 1 {
				t.Fatal("new estimates moved the viewport before scrolling began", before, first, ok)
			}
			tt.Advance(400 * time.Millisecond)
			if !s.AtEnd() || !s.FollowingEnd() {
				t.Fatal("end glide did not settle")
			}
		})
	}
}

func TestAnimatedScrollToEndSettlesOnDeadlineDuringContinuousFastGrowth(t *testing.T) {
	s := ListState{FollowEnd: true, Key: func(i int) any { return i }}
	tailH := float32(40)
	tt := NewTester(func(c *Context) {
		List(c, &s, 40, func(i int) {
			h := float32(40)
			if i == 38 {
				h = tailH
			}
			Text(c, fmt.Sprint("row-", i)).Height(h)
		}).Fill()
	}, 400, 400)
	s.ScrollTo(10, Start)
	tt.Frame()
	s.ScrollToEnd(Animation{Duration: 320 * time.Millisecond, Ease: CubicBezier(.4, 0, .2, 1)})
	tt.Frame()
	// Continuously grow the tail every 16ms for 600ms (well past the 320ms deadline).
	for step := 0; step < 38; step++ {
		tailH += 60
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
	}
	_, after := s.ScrollEdges()
	if !s.AtEnd() || !s.FollowingEnd() || after > 1 {
		t.Fatalf("continuous fast growth trapped ScrollToEnd behind the bottom: atEnd=%t following=%t after=%.1f", s.AtEnd(), s.FollowingEnd(), after)
	}
}

func TestDownwardScrollDuringAnimatedEndGlideKeepsFollowing(t *testing.T) {
	s := ListState{FollowEnd: true}
	tt := NewTester(func(c *Context) {
		List(c, &s, 100, func(i int) { Column(c).Height(40) }).Fill()
	}, 400, 400)
	s.ScrollTo(10, Start)
	tt.Frame()
	s.ScrollToEnd(Animation{Duration: 320 * time.Millisecond, Ease: Linear})
	tt.Advance(80 * time.Millisecond)
	before := s.wroteY
	tt.Scroll(200, 200, 0, 120)
	tt.Frame()
	if !s.FollowingEnd() || s.wroteY < before {
		t.Fatalf("downward scroll toward the end cancelled following or rewound: following=%t before=%.1f after=%.1f", s.FollowingEnd(), before, s.wroteY)
	}
	tt.Advance(400 * time.Millisecond)
	if !s.AtEnd() || !s.FollowingEnd() {
		t.Fatalf("animated end glide did not settle after downward scroll: atEnd=%t following=%t", s.AtEnd(), s.FollowingEnd())
	}
}
