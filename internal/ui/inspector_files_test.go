package ui

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func inspectorFilesFixture(modern, split, dark bool, height int) (*View, *n.Tester) {
	m := layoutModel()
	m.Inspectors["s"] = true
	m.InspectorWidth = 340
	if modern {
		m.WorkerMinor = 33
	} else {
		m.WorkerMinor = 32
	}
	if split {
		m.InspectorLayout.Groups = []app.InspectorGroup{{Views: []string{"changes"}, Selected: "changes"}, {Views: []string{"background"}, Selected: "background"}}
	}
	v := New(m, app.NewController(""), "")
	state := v.changesFor("s")
	state.load, state.Updated = changesLoaded, time.Now()
	for i := 0; i < 60; i++ {
		state.Files = append(state.Files, domain.ChangedFile{Path: fmt.Sprintf("internal/ui/file-%02d.go", i)})
	}
	ui := n.NewTester(v.View, 1240, height)
	ui.SetDark(dark)
	ui.Advance(time.Second)
	return v, ui
}

func TestChangesListLastFileIsFullyVisibleAndClickable(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, split := range []bool{false, true} {
			for _, dark := range []bool{false, true} {
				for _, height := range []int{600, 900} {
					t.Run(fmt.Sprintf("modern=%v/split=%v/dark=%v/height=%d", modern, split, dark, height), func(t *testing.T) {
						v, ui := inspectorFilesFixture(modern, split, dark, height)
						pane, _ := ui.Find("文件更改区域")
						ui.Scroll(pane.X+pane.W/2, pane.Y+pane.H/2, 0, 10000)
						ui.Advance(time.Second)
						last, ok := ui.Find("查看文件:internal/ui/file-59.go")
						saveNativeSkillImage(t, ui, fmt.Sprintf("files-modern-%v-split-%v-dark-%v-height-%d.png", modern, split, dark, height))
						if !ok || last.H < 27 || last.Y+last.H > pane.Y+pane.H+.5 {
							t.Fatalf("last file is clipped after scrolling to the end: row=%+v pane=%+v", last, pane)
						}
						ui.ClickAt(last.X+last.W/2, last.Y+last.H/2)
						ui.Frame()
						if v.changesFor("s").Path != "internal/ui/file-59.go" {
							t.Fatal("last file cannot be opened at its visible position")
						}
					})
				}
			}
		}
	}
}

func TestChangesListNamesAddedAndDeletedFilesWithoutGuessingFromLineCounts(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []float32{280, 340} {
			v, ui := inspectorFilesFixture(true, false, dark, 820)
			v.Model.InspectorWidth = width
			state := v.changesFor("s")
			if err := json.Unmarshal([]byte(`[{"path":"added-empty.go","kind":"add","added":0,"removed":0},{"path":"deleted-empty.go","kind":"delete","added":0,"removed":0},{"path":"modified.go","kind":"update","added":3,"removed":0},{"path":"untracked.go","kind":"add"}]`), &state.Files); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("file-kinds-dark-%v-width-%g.png", dark, width))
			if !ui.HasText("新增") || !ui.HasText("删除") || !ui.HasText("+3") {
				t.Fatal("file identity is indistinguishable from changed line counts", ui.Texts())
			}
			for _, text := range ui.Texts() {
				if text == "+—" || text == "−—" {
					t.Fatal("unknown counts look like add/delete evidence", ui.Texts())
				}
			}
		}
	}
}
