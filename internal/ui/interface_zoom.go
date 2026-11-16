package ui

import (
	"fmt"
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
)

func (v *View) syncInterfaceZoom(c *n.Context) {
	if c.Shortcut(n.Cmd, n.Key0) {
		v.Model.Preferences.InterfaceZoom = 1
		v.interfaceZoomToast = true
	}
	zoom := v.Model.Preferences.InterfaceZoom
	c.SetZoom(zoom)
	c.OnScroll(func(ev n.InputEvent) bool {
		if ev.Precise {
			// Events use the last drawn frame's coordinates, even when
			// earlier callbacks have already changed the requested zoom.
			ev.DY *= zoom
		}
		return v.interfaceScroll(ev)
	})
	if v.interfaceZoomToast {
		c.AddToast(n.Toast{ID: "interface-zoom", Title: fmt.Sprintf("缩放 %.0f%%", v.Model.Preferences.InterfaceZoom*100), Timeout: 1500 * time.Millisecond})
		v.interfaceZoomToast = false
	}
}

func (v *View) interfaceScroll(ev n.InputEvent) bool {
	if ev.Mods != n.Cmd || ev.DY == 0 {
		return false
	}
	// Consume inertia without letting it zoom after the fingers have lifted.
	if !ev.Momentum {
		p := &v.Model.Preferences
		zoom := min(float32(1.5), max(.75, p.InterfaceZoom*float32(math.Exp(float64(-ev.DY)*.0025))))
		if zoom != p.InterfaceZoom {
			p.InterfaceZoom = zoom
			v.interfaceZoomToast = true
		}
	}
	return true
}
