package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"time"
)

type inspectorTabDrag struct{ session, kind string }

func otherInspectorView(kind string) string {
	if kind == "tasks" {
		return "changes"
	}
	return "tasks"
}

func (v *View) inspectorTabs(c *n.Context, t *n.Theme, kinds []string, kind string, headerHeight float32, drop **app.InspectorLayout) {
	selected := 0
	for i, k := range kinds {
		if k == kind {
			selected = i
		}
	}
	tabs := n.TabsBase(c.Key("inspector-tabs"), &selected, len(kinds))
	top := (headerHeight - 28) / 2
	tabs.List.Absolute().Left(12).Top(top).Width(float32(len(kinds)*30 + 28)).Height(28).Gap(2).Children(func() {
		for i, k := range kinds {
			label, icon := "文件更改", changesIcon
			if k == "tasks" {
				label, icon = "任务", tasksIcon
			}
			button := controlFeedback(c, tabs.Tab(i), 6).Size(28, 28).AlignItems(n.Center).Justify(n.Center).Radius(6).Label(label).Tooltip(label).TextColor(t.TextMuted).Cursor(n.CursorDefault).Drag(inspectorTabDrag{v.Model.SessionID, k})
			button.Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 120*time.Millisecond).Duration})
			if len(kinds) > 1 && selected == i {
				button.Background(t.SurfacePressed).TextColor(t.Text)
			}
			if button.Dragging() {
				button.Opacity(0).Cursor(n.CursorGrabbing)
			}
			button.Children(func() { n.Icon(c, icon).Size(16, 16) })
		}
	})
	if len(kinds) > 1 && kinds[selected] != kind {
		v.Model.InspectorLayout.Selected = kinds[selected]
		c.AnimationFrame()
	}
	dragged, over := n.DragOver[inspectorTabDrag](tabs.List)
	dropped, released := n.Drop[inspectorTabDrag](tabs.List)
	valid := over && len(kinds) == 1 && dragged.session == v.Model.SessionID && dragged.kind != kind
	if valid {
		n.Column(c.Key("inspector-merge-preview")).Absolute().Left(10).Top(top-2).Width(62).Height(32).Radius(7).Background(t.Text.Alpha(.07)).Border(1, t.TextMuted.Alpha(.4)).PassThrough().Label("合并标签预览").Transition(n.ElementTransition{Colors: true, Duration: motionAnimation(c, 100*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
	}
	if released && len(kinds) == 1 && dropped.session == v.Model.SessionID && dropped.kind != kind {
		next := app.InspectorLayout{Joined: true, First: kind, Selected: dropped.kind}
		*drop = &next
	}
}
