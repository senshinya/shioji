package ui

import (
	n "github.com/egoist/mygo/ui"
	"time"
)

// Settings switches use a flat neutral capsule, with a generous hit area.
func settingsSwitch(c *n.Context, value *bool) n.Element {
	t := c.Theme()
	e := n.SwitchBase(c, value).Size(40, 28)
	track, thumb := n.Hex("#d1d1d1"), n.Hex("#ffffff")
	if t.Dark {
		track, thumb = n.Hex("#393939"), n.Hex("#b0b0b0")
	}
	target := float32(0)
	if *value {
		target = 14
		track, thumb = n.Hex("#333333"), n.Hex("#ffffff")
		if t.Dark {
			track, thumb = n.Hex("#d0d0d0"), n.Hex("#202020")
		}
	}
	if e.Hovered() || e.FocusVisible() {
		track = track.Mix(t.Text, .08)
	}
	if e.Pressed() {
		track = track.Mix(t.Text, .14)
	}
	position := e.AnimateTo("switch-thumb", target, motionAnimation(c, 160*time.Millisecond))
	fill := e.AnimateColor("switch-track", track, motionAnimation(c, 140*time.Millisecond))
	ink := e.AnimateColor("switch-knob", thumb, motionAnimation(c, 140*time.Millisecond))
	return e.Draw(func(p *n.Painter, r n.Rect) {
		box := n.Rect{X: r.X + r.W - 34, Y: r.Y + (r.H-20)/2, W: 34, H: 20}
		p.Fill(box, fill, 10)
		p.Fill(n.Rect{X: box.X + 3 + position, Y: box.Y + 3, W: 14, H: 14}, ink, 7)
	})
}
