package ui

import (
	"testing"
	"time"
)

func TestScrollContainerBarsFollowMovementRatherThanHover(t *testing.T) {
	s := ScrollState{}
	tt := NewTester(func(c *Context) {
		Column(c).Fill().Background(Hex("#ffffff")).Children(func() {
			Scroll(c).Fill().TrackScroll(&s).Children(func() { Box(c).Height(1000).FillWidth() })
		})
	}, 240, 200)
	barInk := func() int {
		ink := 0
		for y := 5; y < 195; y++ {
			for x := 228; x < 239; x++ {
				if tt.Image().RGBAAt(x, y).R < 240 {
					ink++
				}
			}
		}
		return ink
	}
	tt.Move(100, 100)
	tt.Advance(time.Second)
	if ink := barInk(); ink != 0 {
		t.Fatal("hover alone reveals the scrollbar", ink)
	}
	tt.Scroll(100, 100, 0, 80)
	tt.Frame()
	if ink := barInk(); ink == 0 || s.Y < 1 {
		t.Fatal("actual scrolling does not reveal its thumb", ink, s.Y)
	}
	// The thumb fades even if the pointer stays inside the scrollport.
	tt.Advance(2 * time.Second)
	if ink := barInk(); ink != 0 {
		t.Fatal("idle scrollbar remains visible under the pointer", ink)
	}
	tt.Move(260, 100)
	tt.Move(100, 100)
	tt.Frame()
	if ink := barInk(); ink != 0 {
		t.Fatal("re-entering replays a stale scrollbar", ink)
	}
}
