package ui

import (
	n "github.com/egoist/mygo/ui"
	"time"
)

type hoverCardState struct {
	Open, Dismissed bool
	LastInside      time.Time
}

func informationPopoverSurface(panel n.Element, t *n.Theme) n.Element {
	return panel.Padding(10).Radius(8).Background(raised(t)).Border(1, t.Border).
		Shadow(0, 5, 18, 0, n.RGBA(0, 0, 0, .14))
}

// hoverCard is interactive information, unlike a pass-through text hint.
// Hover never moves keyboard focus; crossing the gap keeps the card alive.
func hoverCard(c *n.Context, trigger n.Element, build func(n.Element)) n.Element {
	s := n.Local(trigger, "hover-card", func() hoverCardState { return hoverCardState{LastInside: c.Now()} })
	inside := trigger.Hovered() || trigger.FocusVisible()
	if c.MenuOpen() {
		s.Open = false
		s.Dismissed = true
	}
	if inside && !s.Dismissed {
		s.Open = true
		s.LastInside = c.Now()
	}
	panel := n.PopoverBase(c, trigger, &s.Open, func(panel n.Element) { panel.Transition(popupMotion(c)); build(panel) })
	if panel.Valid() {
		if panel.Hovered() || panel.FocusWithin() {
			inside = true
			s.LastInside = c.Now()
		}
		if panel.Dismissed() {
			s.Dismissed = true
		}
	}
	if !inside {
		s.Dismissed = false
		if s.Open {
			wait := 180*time.Millisecond - c.Now().Sub(s.LastInside)
			if wait <= 0 {
				s.Open = false
				c.AnimationFrame()
			} else {
				c.After(wait)
			}
		}
	}
	return panel
}
