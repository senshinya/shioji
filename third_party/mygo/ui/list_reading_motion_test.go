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
