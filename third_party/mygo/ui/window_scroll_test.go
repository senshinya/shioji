package ui

import (
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

func TestWindowScrollCapturePrecedesChildHandlersAndExpires(t *testing.T) {
	capture := true
	children := 0
	tt := NewTester(func(c *Context) {
		if capture {
			c.OnScroll(func(ev InputEvent) bool { return ev.Mods == Cmd })
		}
		Box(c).Fill().HandleInput(func(ev InputEvent) bool {
			if ev.Kind == InputScroll {
				children++
				return true
			}
			return false
		})
	}, 400, 300)
	scroll := func(mods Modifiers) {
		tt.send(platform.SurfaceEvent{Kind: platform.PointerScroll, X: 100, Y: 100, DY: 40, Mods: platform.Modifiers(mods)})
	}
	scroll(Cmd)
	if children != 0 {
		t.Fatal("captured zoom gesture reached the child scroller")
	}
	scroll(0)
	if children != 1 {
		t.Fatal("ordinary scrolling was swallowed")
	}
	capture = false
	tt.Frame()
	scroll(Cmd)
	if children != 2 {
		t.Fatal("removed window handler still intercepts events")
	}
}
