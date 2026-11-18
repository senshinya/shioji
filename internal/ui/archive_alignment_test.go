package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestArchiveBackArrowAlignsToTitleInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{820, 1240} {
			for _, title := range []string{"已归档", "HHH", "国 H 国"} {
				t.Run(fmt.Sprintf("dark=%t/width=%d/%s", dark, width, title), func(t *testing.T) {
					v, _ := archiveViewFixture()
					label := "返回会话"
					if title != "已归档" {
						w := &v.Model.Snapshot.Workspaces[0]
						w.Archived, w.Name = true, title
						v.archive.scope = w.ID
						label = "返回已归档工作区"
					}
					ui := n.NewTester(v.View, width, 720)
					ui.SetDark(dark)
					ui.SetScale(2)
					ui.Advance(time.Second)
					button, buttonOK := ui.Find(label)
					heading, headingOK := ui.Find(title)
					if !buttonOK || !headingOK || button.W != 28 || button.H != 28 {
						t.Fatal("missing heading or compact return target", heading, button)
					}
					saveNativeSkillImage(t, ui, fmt.Sprintf("archive-title-dark-%t-width-%d-%s.png", dark, width, title))
					lo, hi := nativeQuickInkY(t, ui, button, 2)
					a, b := nativeQuickInkY(t, ui, heading, 2)
					delta := float64(lo+hi-a-b) / 4
					t.Logf("arrow minus title ink center: %+.2f DIP", delta)
					if math.Abs(delta) > 1 {
						t.Errorf("visible arrow misses title ink center by %.2f DIP", delta)
					}
				})
			}
		}
	}
}
