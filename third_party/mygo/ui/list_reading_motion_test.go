package ui

import (
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
	tt.Frame()
	if math.Abs(s.wroteY-before) > 1 {
		t.Fatal("animated end scroll jumps before its first frame", before, s.wroteY)
	}
	tt.Advance(100 * time.Millisecond)
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
