package ui

import (
	"fmt"
	"shioji/internal/domain"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestNavigationScrollbarDragsOutsideRowTargets(t *testing.T) {
	for _, width := range []int{950, 1280} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/dark=%v", width, dark), func(t *testing.T) {
				m := navigationFixture()
				m.Snapshot.Workspaces[0].Name = "shioji"
				for i := 0; i < 40; i++ {
					m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: fmt.Sprint("history-", i), WorkspaceID: "w", Title: fmt.Sprintf("检查工作区 Worker 运行状态 %02d", i), Status: "completed"})
				}
				v := New(m, nil, "")
				ui := n.NewTester(v.View, width, 800)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				list, _ := ui.Find("工作区与会话")
				footer, _ := ui.Find("全局快捷栏")
				ui.Scroll(list.X+70, list.Y+100, 0, 30)
				ui.Scroll(list.X+70, list.Y+100, 0, -10000)
				ui.Frame()
				prefix := fmt.Sprintf("navigation-%d-dark-%v", width, dark)
				saveNativeSkillImage(t, ui, prefix+"-scrolling.png")
				// Grab from the sidebar edge, beyond every row's content and
				// action targets, so dragging cannot select or fold a row.
				x, y := footer.X+footer.W-6, list.Y+60
				ui.Press(x, y)
				ui.Move(x, y+160)
				ui.Frame()
				saveNativeSkillImage(t, ui, prefix+"-dragging.png")
				ui.Release(x, y+160)
				if v.navigationScroll.Y < 100 {
					t.Fatal("sidebar edge cannot drag the scrollbar", v.navigationScroll.Y)
				}
				if m.SessionID != "s" || v.collapsedWorkspaces["w"] || m.Drafts["session:s"] != "ordinary draft" {
					t.Fatal("scrollbar drag activated a navigation row or changed its draft")
				}
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, prefix+"-idle.png")
			})
		}
	}
}
