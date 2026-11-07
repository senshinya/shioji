package ui

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestFollowAnimationMeasuresGrowthAboveFixedTrailingRows(t *testing.T) {
	s := ListState{FollowEnd: true, FollowAnimation: &Animation{Duration: 200 * time.Millisecond, Ease: Linear}}
	height := float32(20)
	tt := NewTester(func(c *Context) {
		s.Key = func(i int) any { return i }
		List(c, &s, 32, func(i int) {
			switch i {
			case 30:
				Box(c).Height(24)
			case 31:
				Box(c).Height(100)
			default:
				h := float32(20)
				if i == 29 {
					h = height
				}
				Text(c, fmt.Sprint("row ", i)).Height(h)
			}
		}).Fill()
	}, 400, 300)
	tt.Advance(time.Second)
	before, _ := tt.Find("row 28")
	height += 40
	tt.Frame()
	first, _ := tt.Find("row 28")
	tt.Advance(100 * time.Millisecond)
	middle, _ := tt.Find("row 28")
	tt.Advance(400 * time.Millisecond)
	end, _ := tt.Find("row 28")
	if math.Abs(float64(first.Y-before.Y)) > .5 || middle.Y >= first.Y || middle.Y <= end.Y || math.Abs(float64(before.Y-end.Y)-40) > .5 {
		t.Fatal("a static footer hides the growing content from smooth following", before, first, middle, end)
	}
}

func TestFollowAnimationDoesNotAnimateHistoryInsertedAboveTheViewport(t *testing.T) {
	s := ListState{FollowEnd: true, FollowAnimation: &Animation{Duration: 200 * time.Millisecond, Ease: Linear}}
	first, count := 0, 60
	tt := NewTester(func(c *Context) {
		s.Key = func(i int) any { return first + i }
		List(c, &s, count, func(i int) { Text(c, fmt.Sprint("row ", first+i)).Height(20) }).Fill()
	}, 400, 300)
	tt.Advance(time.Second)
	before, _ := tt.Find("row 59")
	first, count = -10, 70
	tt.Frame()
	after, _ := tt.Find("row 59")
	tt.Advance(100 * time.Millisecond)
	later, _ := tt.Find("row 59")
	if math.Abs(float64(after.Y-before.Y)) > .5 || math.Abs(float64(later.Y-before.Y)) > .5 {
		t.Fatal("older history animated an unchanged live tail", before, after, later)
	}
}

func TestFollowAnimationKeepsAnimatedDockInsetSynchronous(t *testing.T) {
	s := ListState{FollowEnd: true, FollowAnimation: &Animation{Duration: 200 * time.Millisecond, Ease: Linear}}
	inset := float32(100)
	tt := NewTester(func(c *Context) {
		s.Key = func(i int) any { return i }
		s.FollowInset = inset
		List(c, &s, 32, func(i int) {
			if i == 31 {
				Box(c).Height(inset)
			} else {
				Text(c, fmt.Sprint("row ", i)).Height(20)
			}
		}).Fill()
	}, 400, 300)
	tt.Advance(time.Second)
	before, _ := tt.Find("row 30")
	for i := 0; i < 8; i++ {
		inset += 10
		tt.Frame()
		after, ok := tt.Find("row 30")
		if !ok || math.Abs(float64(after.Y-before.Y)+float64((i+1)*10)) > .5 {
			t.Fatal("dock inset was given a second animation", i, before, after)
		}
		tt.Advance(time.Second / 60)
	}
}

func TestFollowAnimationGlidesContentWhileDockInsetMovesDirectly(t *testing.T) {
	s := ListState{FollowEnd: true, FollowAnimation: &Animation{Duration: 200 * time.Millisecond, Ease: Linear}}
	inset, height := float32(100), float32(20)
	tt := NewTester(func(c *Context) {
		s.Key = func(i int) any { return i }
		s.FollowInset = inset
		List(c, &s, 32, func(i int) {
			if i == 31 {
				Box(c).Height(inset)
			} else if i == 30 {
				Text(c, "growing row").Height(height)
			} else {
				Text(c, fmt.Sprint("row ", i)).Height(20)
			}
		}).Fill()
	}, 400, 300)
	tt.Advance(time.Second)
	before, _ := tt.Find("row 29")
	inset, height = 130, 60
	tt.Frame()
	first, _ := tt.Find("row 29")
	tt.Advance(100 * time.Millisecond)
	middle, _ := tt.Find("row 29")
	tt.Advance(400 * time.Millisecond)
	end, _ := tt.Find("row 29")
	if math.Abs(float64(before.Y-first.Y)-30) > .5 || middle.Y >= first.Y || middle.Y <= end.Y || math.Abs(float64(before.Y-end.Y)-70) > .5 {
		t.Fatal("dock and content growth did not retain independent motion", before, first, middle, end)
	}
}
