package ui

import (
	"reflect"
	"shioji/internal/app"
	"time"

	n "github.com/egoist/mygo/ui"
)

type navigationDragPhase uint8

const (
	navigationArmed navigationDragPhase = iota
	navigationMoving
	navigationCanceled
)

type navigationDragValue struct {
	kind navigationRowKind
	ref  app.SessionLocation
}
type navigationDrop struct {
	valid, pinned, restore bool
	before                 app.SessionLocation
}
type navigationDragState struct {
	source          navigationDragValue
	phase           navigationDragPhase
	basis, preview  app.NavigationPreferences
	pressed, active bool
	target          navigationDrop
	action          string
	x, y            float32
}

func (v *View) navigationPreview() *app.Model {
	m := v.Model
	m.ObserveNavigation()
	if d := v.navigationDrag; d != nil {
		d.pressed, d.active = false, false
		if !reflect.DeepEqual(m.Navigation, d.basis) {
			d.phase = navigationCanceled
		}
		if d.phase == navigationMoving {
			copy := *m
			copy.Navigation = d.preview
			return &copy
		}
	}
	return m
}
func (v *View) navigationDragSource(c *n.Context, row n.Element, r navigationRow) bool {
	if r.session.Archived || r.workspace.Archived {
		return false
	}
	payload := navigationDragValue{r.kind, r.ref}
	row.Drag(payload).Cursor(n.CursorGrab)
	pressed, moving := row.Pressed(), row.Dragging()
	if v.navigationDrag == nil && (pressed || moving) {
		v.navigationDrag = &navigationDragState{source: payload, basis: v.Model.Navigation.Clone(), preview: v.Model.Navigation.Clone()}
	}
	if d := v.navigationDrag; d != nil && d.source == payload {
		d.pressed = d.pressed || pressed
		d.active = d.active || moving
		if moving && d.phase == navigationArmed {
			d.phase = navigationMoving
			c.AnimationFrame()
		}
	}
	if moving {
		row.Opacity(0).Cursor(n.CursorGrabbing).Background(c.Theme().SurfaceHover).Shadow(0, 2, 6, 0, n.RGBA(0, 0, 0, .08))
	}
	return moving
}

// Use committed row boxes, not the virtual list's estimated content offset.
// Unmeasured offscreen rows may have different estimates after a long scroll.
func (v *View) navigationDropAt(rows []navigationRow, d *navigationDragState, y float32) navigationDrop {
	at := -1
	var box n.Rect
	for i, r := range rows {
		b := v.navigationGeometry.bounds[r.key()]
		if b.H > 0 && y >= b.Y && y < b.Y+navigationRowHeight(r) {
			at, box = i, b
			break
		}
	}
	if at < 0 {
		return navigationDrop{}
	}
	r := rows[at]
	if d.source.kind == navigationWorkspace {
		if r.kind != navigationWorkspace && (r.kind != navigationSession || r.pinned) {
			return navigationDrop{}
		}
		owner := app.SessionLocation{DeviceID: r.workspace.DeviceID, WorkspaceID: r.workspace.ID}
		if owner == d.source.ref && d.target.valid {
			return d.target
		}
		before := owner
		if r.kind == navigationSession || y > box.Y+15 {
			before = app.SessionLocation{}
			for _, next := range rows[at+1:] {
				if next.kind == navigationWorkspace {
					before = next.ref
					break
				}
			}
		}
		return navigationDrop{valid: true, before: before}
	}
	if r.kind == navigationSession && r.ref == d.source.ref && d.target.valid {
		return d.target
	}
	pinned := r.kind == navigationPinnedHeading || r.kind == navigationSession && r.pinned
	if r.kind == navigationWorkspaceHeading {
		if len(v.Model.NavigationPins()) > 0 || y >= box.Y+28 {
			return navigationDrop{}
		}
		pinned = true
	}
	if !pinned {
		if r.workspace.DeviceID != d.source.ref.DeviceID || r.workspace.ID != d.source.ref.WorkspaceID {
			return navigationDrop{}
		}
		if v.collapsedWorkspaces[r.workspace.ID] {
			return navigationDrop{valid: true, restore: true}
		}
	}
	before := app.SessionLocation{}
	if !pinned {
		before = app.SessionLocation{DeviceID: r.workspace.DeviceID, WorkspaceID: r.workspace.ID}
	}
	start := at
	if r.kind != navigationSession || y >= box.Y+15 {
		start++
	}
	for _, next := range rows[start:] {
		if next.kind == navigationWorkspaceHeading || next.kind == navigationWorkspace {
			break
		}
		if next.kind == navigationSession && next.pinned == pinned && next.ref != d.source.ref {
			before = next.ref
			break
		}
	}
	return navigationDrop{valid: true, pinned: pinned, before: before}
}
func (d navigationDrop) apply(m *app.Model, source navigationDragValue) error {
	if source.kind == navigationWorkspace {
		return m.MoveNavigationWorkspace(source.ref.DeviceID, source.ref.WorkspaceID, d.before.DeviceID, d.before.WorkspaceID)
	}
	if d.restore {
		return m.SetSessionPinned(source.ref, false)
	}
	return m.MoveNavigationSession(source.ref, d.before, d.pinned)
}
func (v *View) navigationListGestures(c *n.Context, list n.Element, rows []navigationRow) {
	dropped, released := n.Drop[navigationDragValue](list)
	x, y, over := list.PointerPosition()
	d := v.navigationDrag
	if d == nil {
		return
	}
	box := list.Bounds()
	d.x, d.y = box.X+x, box.Y+y
	if d.phase == navigationMoving {
		target := navigationDrop{}
		if over {
			target = v.navigationDropAt(rows, d, box.Y+y)
		}
		d.target = target
		d.action = ""
		if !d.target.valid {
			d.action = "保留在原工作区"
		}
		if d.target.valid {
			candidate := *v.Model
			candidate.Navigation = d.basis.Clone()
			if err := d.target.apply(&candidate, d.source); err != nil {
				d.target.valid = false
				d.action = "排列已变化"
			} else {
				if !reflect.DeepEqual(candidate.Navigation, d.preview) {
					d.preview = candidate.Navigation
					c.AnimationFrame()
				}
				if d.source.kind == navigationSession && d.target.pinned != v.Model.SessionPinned(d.source.ref) {
					if d.target.pinned {
						d.action = "置顶"
					} else {
						d.action = "取消置顶"
					}
				}
			}
		}
	}
	if released && dropped == d.source {
		if d.phase == navigationMoving && d.target.valid {
			target, source := d.target, d.source
			v.navigationChange(func(m *app.Model) error { return target.apply(m, source) })
		}
		v.navigationDrag = nil
		c.AnimationFrame()
		return
	}
	if d.phase == navigationMoving && !d.active {
		d.phase = navigationCanceled
		c.AnimationFrame()
	}
	if !d.pressed && !d.active {
		v.navigationDrag = nil
		c.AnimationFrame()
		return
	}
	if d.phase != navigationMoving {
		return
	}
	if d.source.kind == navigationSession && len(v.Model.NavigationPins()) == 0 && v.navigationScroll.Y < .5 {
		hasPreviewPin := false
		for _, r := range rows {
			hasPreviewPin = hasPreviewPin || r.kind == navigationSession && r.pinned
		}
		if !hasPreviewPin {
			n.Overlay(c, func() {
				n.Row(c.Key("navigation-empty-pin-target")).Absolute().Left(box.X).Top(box.Y).Width(box.W).Height(28).PaddingX(8).Gap(6).AlignItems(n.Baseline).Radius(6).Background(c.Theme().Surface).Border(1, c.Theme().Border).PassThrough().Label("拖到这里置顶").Transition(fadeMotion(c, 120*time.Millisecond)).Children(func() {
					inlineIconText(c, navigationPinIcon, "拖到这里置顶", n.Font{Family: c.Theme().Font, Size: 11}, 12, 6, c.Theme().TextMuted)
				})
			})
		}
	}
	if d.action != "" {
		label := d.action
		_, height := c.Size()
		n.Overlay(c, func() {
			n.Column(c.Key("navigation-drag-action")).Absolute().Left(min(d.x+12, v.viewportWidth-140)).Top(min(d.y+18, height-32)).Padding(3, 7).Radius(5).Background(c.Theme().Surface).Border(1, c.Theme().Border).PassThrough().Transition(fadeMotion(c, 120*time.Millisecond)).Children(func() { n.Text(c, label).FontSize(11).FixedLineHeight(18).TextColor(c.Theme().TextMuted) })
		})
	}
}
