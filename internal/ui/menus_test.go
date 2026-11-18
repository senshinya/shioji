package ui

import (
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestWorkspaceMenuActionsShareTheSameVisibleTextColumn(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			m := layoutModel()
			m.Snapshot.Workspaces[0].Folders = []string{"/project/shioji"}
			ui := n.NewTester(New(m, nil, "").View, 850, 600)
			ui.SetDark(dark)
			if err := ui.Click("工作区目录"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			im := ui.Image()
			inkStart := func(label string, start, end int) int {
				t.Helper()
				r, ok := ui.Find(label)
				if !ok {
					t.Fatalf("missing workspace action %q", label)
				}
				y0, y1 := int(r.Y)+4, int(r.Y+r.H)-4
				background := im.RGBAAt(int(r.X+r.W)-10, y0)
				for x := int(r.X) + start; x < int(r.X)+end; x++ {
					for y := y0; y < y1; y++ {
						if p := im.RGBAAt(x, y); abs(int(p.R)-int(background.R)) > 28 {
							return x
						}
					}
				}
				t.Fatalf("no visible ink for %q at row offsets %d–%d in %v", label, start, end, r)
				return 0
			}
			inkStart("工作区设置", 6, 25)
			settings := inkStart("工作区设置", 26, 65)
			importSession := inkStart("导入会话…", 26, 65)
			if abs(settings-importSession) > 1 {
				t.Fatalf("workspace action text columns differ: settings x=%d, import x=%d", settings, importSession)
			}
		})
	}
}
