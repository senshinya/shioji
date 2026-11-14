package ui

import (
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestInspectorTabCursorOnlyGrabsDuringDrag(t *testing.T) {
	for _, work := range []bool{false, true} {
		for _, joined := range []bool{false, true} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("work=%v/joined=%v/dark=%v", work, joined, dark), func(t *testing.T) {
					fixture := dockingUI
					labels := []string{"文件更改", "任务"}
					if work {
						fixture = sessionWorkFixture
						labels = []string{"更改", "子 Agent", "任务", "后台任务"}
					}
					v, ui := fixture(t)
					if joined {
						if work {
							v.Model.InspectorLayout.Groups = []app.InspectorGroup{{Views: []string{"changes", "agents", "tasks", "background"}, Selected: "agents"}}
						} else {
							v.Model.InspectorLayout = app.InspectorLayout{Joined: true, First: "changes", Selected: "changes"}
						}
					}
					ui.SetDark(dark)
					ui.Frame()
					ui.Advance(time.Second)
					for _, label := range labels {
						tab, ok := ui.Find(label)
						if !ok {
							t.Fatal("missing inspector tab", label)
						}
						x, y := tab.X+tab.W/2, tab.Y+tab.H/2
						check := func(phase string, want n.Cursor) {
							t.Helper()
							if got := ui.Cursor(); got != want {
								t.Errorf("%s/%s cursor=%v, want=%v", label, phase, got, want)
							}
						}
						ui.Move(x, y)
						check("hover", n.CursorDefault)
						ui.Press(x, y)
						ui.Move(x+2, y)
						check("below drag threshold", n.CursorDefault)
						ui.Move(x+40, y+40)
						check("dragging", n.CursorGrabbing)
						ui.Move(500, 500)
						check("drag outside tab", n.CursorGrabbing)
						ui.Release(500, 500)
						ui.Move(x, y)
						check("released", n.CursorDefault)
						ui.Press(x, y)
						ui.Move(x+40, y+40)
						ui.Key(0, n.KeyEscape)
						ui.Release(x+40, y+40)
						ui.Move(x, y)
						check("canceled", n.CursorDefault)
					}
				})
			}
		}
	}
}
