package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
)

func (v *View) ViewMenu() *mygo.MenuItem {
	zoom := func(steps int) func(*mygo.MenuItem, *mygo.Window) {
		return func(_ *mygo.MenuItem, window *mygo.Window) {
			if window != nil && window == v.nativeWindow {
				v.keyboardInterfaceZoom(steps)
				window.Invalidate()
			}
		}
	}
	// AppKit handles the View menu's key equivalents before the window.
	return &mygo.MenuItem{Role: mygo.RoleViewMenu, Submenu: []*mygo.MenuItem{
		{Role: mygo.RoleReload},
		{Role: mygo.RoleForceReload},
		{Role: mygo.RoleToggleDevTools},
		mygo.Separator(),
		{Role: mygo.RoleResetZoom, Click: zoom(0)},
		{Role: mygo.RoleZoomIn, Click: zoom(1)},
		{Role: mygo.RoleZoomOut, Click: zoom(-1)},
		mygo.Separator(),
		{Role: mygo.RoleToggleFullScreen},
	}}
}

func (v *View) syncInterfaceZoom(c *n.Context) {
	zoomIn := c.Shortcut(n.Cmd, n.KeyEqual)
	zoomInShifted := c.Shortcut(n.Cmd|n.Shift, n.KeyEqual)
	zoomOut := c.Shortcut(n.Cmd, n.KeyMinus)
	switch {
	case c.Shortcut(n.Cmd, n.Key0):
		v.keyboardInterfaceZoom(0)
	case zoomIn || zoomInShifted:
		v.keyboardInterfaceZoom(1)
	case zoomOut:
		v.keyboardInterfaceZoom(-1)
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

func (v *View) keyboardInterfaceZoom(steps int) {
	if steps == 0 {
		v.Model.Preferences.InterfaceZoom = 1
		v.interfaceZoomToast = true
		return
	}
	v.setInterfaceZoom(v.Model.Preferences.InterfaceZoom + float32(steps)*.05)
}

func (v *View) interfaceScroll(ev n.InputEvent) bool {
	if ev.Mods != n.Cmd || ev.DY == 0 {
		return false
	}
	// Consume inertia without letting it zoom after the fingers have lifted.
	if !ev.Momentum {
		v.setInterfaceZoom(v.Model.Preferences.InterfaceZoom * float32(math.Exp(float64(-ev.DY)*.0025)))
	}
	return true
}

func (v *View) setInterfaceZoom(zoom float32) {
	zoom = min(1.5, max(.75, zoom))
	if zoom != v.Model.Preferences.InterfaceZoom {
		v.Model.Preferences.InterfaceZoom = zoom
		v.interfaceZoomToast = true
	}
}
