package ui

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestListContentAnchorKeepsWheelInertiaAcrossAPrepend(t *testing.T) {
	first := 100
	count := 41
	s := ListState{}
	s.Key = func(i int) any {
		if i == 0 {
			return "edge"
		}
		return first + i - 1
	}
	s.SkipAnchor = func(i int) bool { return i == 0 }
	tt := NewTester(func(c *Context) {
		List(c, &s, count, func(i int) {
			if i == 0 {
				Column(c).Height(80)
				return
			}
			Text(c, fmt.Sprint("row-", first+i-1)).Height(60)
		}).Fill()
	}, 400, 400)
	before, ok := tt.Find("row-100")
	if !ok {
		t.Fatal("missing boundary content")
	}
	first = 80
	count += 20
	tt.Frame()
	after, ok := tt.Find("row-100")
	if !ok || math.Abs(float64(after.Y-before.Y)) > 1 {
		t.Fatal("edge spacer anchored the prepend", before, after)
	}
	tt.Wheel(200, 200, 0, -100)
	tt.Advance(30 * time.Millisecond)
	tt.Frame() // Sample at the same clock instant as the arriving page.
	moving, _ := tt.Find("row-100")
	first = 60
	count += 20
	tt.Frame()
	loaded, ok := tt.Find("row-100")
	if !ok || math.Abs(float64(loaded.Y-moving.Y)) > 1 {
		t.Fatal("page arrival moved the current reading row", moving, loaded)
	}
	tt.Advance(30 * time.Millisecond)
	continued, _ := tt.Find("row-100")
	if continued.Y <= loaded.Y+1 {
		t.Fatal("page arrival stopped wheel inertia", loaded, continued)
	}
}

func TestListScrollEdgesReadMeasuredOffsetsWithoutScrolling(t *testing.T) {
	s := ListState{}
	tt := NewTester(func(c *Context) { List(c, &s, 100, func(i int) { Text(c, fmt.Sprint(i)).Height(40) }).Fill() }, 400, 400)
	before, after := s.ScrollEdges()
	if before != 0 || after <= 0 {
		t.Fatal("incorrect initial edge distances", before, after)
	}
	s.ScrollTo(10, Start)
	tt.Frame()
	before, after = s.ScrollEdges()
	if math.Abs(float64(before-400)) > 1 || after <= 0 {
		t.Fatal("edge distances do not follow the laid-out content", before, after)
	}
	tt.Wheel(200, 200, 0, 120)
	tt.Advance(30 * time.Millisecond)
	moving, _ := s.ScrollEdges()
	tt.Frame()
	tt.Advance(30 * time.Millisecond)
	continued, _ := s.ScrollEdges()
	if continued <= moving+1 {
		t.Fatal("reading edge distances interrupted wheel inertia", moving, continued)
	}
}
