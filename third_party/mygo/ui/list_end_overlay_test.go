package ui

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestListEndOverlayUsesCurrentLayoutBeforePainting(t *testing.T) {
	s := ListState{FollowEnd: true}
	height := float32(130)
	painted, built := 0, 0
	tt := NewTester(func(c *Context) {
		built = 0
		List(c, &s, 1000, func(i int) {
			built++
			Text(c, fmt.Sprint(i)).Height(20).DrawOver(func(_ *Painter, r Rect) {
				if i == 999 {
					painted++
					if math.Abs(float64(r.Y+r.H+height-300)) > .5 {
						t.Error("painted the end with another layout's overlay height", r, height)
					}
				}
			})
		}).Fill()
		s.EndOverlay = Column(c).Absolute().Left(0).Right(0).Bottom(0).Height(height)
	}, 400, 300)
	for _, next := range []float32{30, 170, 60} {
		height = next
		tt.Frame()
		if !s.AtEnd() || built > 30 {
			t.Fatal("overlay measurement lost the end or virtualized rows", s.AtEnd(), built)
		}
	}
	// Removing the overlay must not leave an animation inset on the state.
	height = 0
	s.FollowAnimation = &Animation{Duration: 200 * time.Millisecond, Ease: Linear}
	tt.Frame()
	tt.Advance(time.Second)
	if painted < 4 {
		t.Fatal("did not inspect the painted frames", painted)
	}
}

func TestListEndOverlayRemovalAndEmptyContent(t *testing.T) {
	s := ListState{FollowEnd: true, FollowAnimation: &Animation{Duration: 200 * time.Millisecond, Ease: Linear}}
	show, count := true, 40
	tt := NewTester(func(c *Context) {
		List(c, &s, count, func(i int) { Text(c, fmt.Sprint(i)).Height(20) }).Fill()
		s.EndOverlay = Element{}
		if show {
			s.EndOverlay = Column(c).Absolute().Left(0).Right(0).Bottom(0).Height(100)
		}
	}, 400, 300)
	tt.Advance(time.Second)
	show = false
	tt.Frame()
	last, _ := tt.Find("39")
	if math.Abs(float64(last.Y+last.H)-300) > .5 {
		t.Fatal("removing overlay retained its inset or animated the released space", last)
	}
	show, count = true, 0
	tt.SetSize(400, 60)
	if s.frame.e.contentH != 100 {
		t.Fatal("empty list omitted the measured overlay reserve", s.frame.e.contentH)
	}
}

func TestListEndOverlayAndContentRetainIndependentMotion(t *testing.T) {
	s := ListState{FollowEnd: true, FollowAnimation: &Animation{Duration: 200 * time.Millisecond, Ease: Linear}}
	inset, height := float32(100), float32(20)
	tt := NewTester(func(c *Context) {
		List(c, &s, 32, func(i int) {
			h := float32(20)
			if i == 31 {
				h = height
			}
			Text(c, fmt.Sprint("row ", i)).Height(h)
		}).Fill()
		s.EndOverlay = Column(c).Absolute().Left(0).Right(0).Bottom(0).Height(inset)
	}, 400, 300)
	tt.Advance(time.Second)
	before, _ := tt.Find("row 30")
	inset, height = 130, 60
	tt.Frame()
	first, _ := tt.Find("row 30")
	tt.Advance(100 * time.Millisecond)
	middle, _ := tt.Find("row 30")
	tt.Advance(time.Second)
	end, _ := tt.Find("row 30")
	if math.Abs(float64(before.Y-first.Y)-30) > .5 || middle.Y >= first.Y || middle.Y <= end.Y || math.Abs(float64(before.Y-end.Y)-70) > .5 {
		t.Fatal("overlay growth and live content did not keep independent motion", before, first, middle, end)
	}
	tt.Scroll(200, 100, 0, -140)
	tt.Frame()
	reading, ok := tt.Find("row 25")
	if !ok || s.FollowingEnd() {
		t.Fatal("fixture did not enter reading mode")
	}
	inset = 40
	tt.Frame()
	tt.Advance(time.Second)
	after, _ := tt.Find("row 25")
	if math.Abs(float64(after.Y-reading.Y)) > .5 || s.FollowingEnd() {
		t.Fatal("overlay resizing moved the history reader", reading, after)
	}
}
