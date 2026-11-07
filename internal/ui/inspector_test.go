package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"math"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestFileDiffReplacesListInsideSingleInspector(t *testing.T) {
	m := layoutModel()
	m.Inspectors["s"] = true
	v := New(m, app.NewController(""), "")
	state := v.changesFor("s")
	state.load = changesLoaded
	state.Updated = time.Now()
	state.Files = []domain.ChangedFile{{Path: "main.go"}}
	ui := n.NewTester(v.View, 1440, 900)
	before, _ := ui.Find("更改侧栏")
	history, _ := ui.Find("对话记录")
	ui.Click("查看文件:main.go")
	var file app.Job
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "git.file" {
			file = job
		}
	}
	if file.Apply == nil {
		t.Fatal("file selection did not request a diff")
	}
	body, _ := json.Marshal(domain.FileVersions{Path: "main.go", Old: "old\n", New: "new\n"})
	file.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	panel, _ := ui.Find("更改侧栏")
	diff, ok := ui.Find("原生文件差异")
	afterHistory, _ := ui.Find("对话记录")
	if !ok || panel != before || afterHistory != history || diff.X < panel.X || diff.X+diff.W > panel.X+panel.W {
		t.Fatal("diff does not replace list within the same panel", before, panel, history, afterHistory, diff)
	}
	if _, extra := ui.Find("文件差异阅读器"); extra {
		t.Fatal("opening a file creates a second sidebar")
	}
	if list, ok := ui.Find("查看文件:main.go"); !ok || list.X < panel.X || list.X+list.W > panel.X+panel.W {
		t.Fatal("changes list is not retained underneath the file view", panel, list)
	}
	if err := ui.Click("返回更改列表"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if _, ok := ui.Find("查看文件:main.go"); !ok {
		t.Fatal("Back did not restore the global changes list")
	}
	if got, _ := ui.Find("更改侧栏"); got != before {
		t.Fatal("Back changed inspector width", before, got)
	}
}

func TestFileDiffSlidesFromRightWithoutMovingInspector(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		m := layoutModel()
		m.Inspectors["s"] = true
		m.InspectorWidth = 500
		v := New(m, nil, "")
		state := v.changesFor("s")
		state.load = changesLoaded
		state.Files = []domain.ChangedFile{{Path: "main.go"}, {Path: "README.md"}}
		state.Versions = domain.FileVersions{Path: "file.go", Old: "old\n", New: "new\n"}
		state.Diff = editor.DiffState{Old: editor.NewBuffer("old\n"), New: editor.NewBuffer("new\n"), Layout: editor.DiffUnified}
		ui := n.NewTester(v.View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		panel, _ := ui.Find("更改侧栏")
		refresh, _ := ui.Find("刷新更改")
		close, _ := ui.Find("关闭更改侧栏")
		list, _ := ui.Find("查看文件:main.go")
		state.selectFile("file.go")
		ui.Frame()
		ui.Advance(80 * time.Millisecond)
		diff, ok := ui.Find("原生文件差异")
		if !ok || !reduced && (diff.X <= panel.X || diff.W >= panel.W) || reduced && diff.X != panel.X {
			t.Fatal("file diff did not slide in from right / honor Reduce Motion", reduced, panel, diff)
		}
		back, _ := ui.Find("返回更改列表")
		if got, _ := ui.Find("刷新更改"); got != refresh {
			t.Fatal("refresh moves with file content", refresh, got)
		}
		if got, _ := ui.Find("关闭更改侧栏"); got != close {
			t.Fatal("close moves with file content", close, got)
		}
		if got, ok := ui.Find("查看文件:main.go"); !ok || got != list {
			t.Fatal("original changes list disappears on file selection", list, got)
		}
		if math.Abs(float64(back.X-diff.X-12)) > 1 {
			t.Fatal("file header does not move with diff body", back, diff)
		}
		ui.Advance(300 * time.Millisecond)
		diff, _ = ui.Find("原生文件差异")
		if diff.X != panel.X || diff.W != panel.W {
			t.Fatal("file diff did not fill inspector", panel, diff)
		}
		x, y := int(panel.X+panel.W-10), int(diff.Y+10)
		filePixel := ui.Image().RGBAAt(x, y)
		ui.Click("返回更改列表")
		ui.Advance(80 * time.Millisecond)
		if got, _ := ui.Find("更改侧栏"); got != panel {
			t.Fatal("Back moved inspector", got, panel)
		}
		movingPixel := ui.Image().RGBAAt(x, y)
		if !reduced {
			for px := int(panel.X + panel.W - 45); px < int(panel.X+panel.W-10); px++ {
				for py := int(diff.Y + 5); py < int(diff.Y+18); py++ {
					if got := ui.Image().RGBAAt(px, py); got != filePixel {
						t.Fatal("restored list paints over departing diff", px, py, got, filePixel)
					}
				}
			}
		}
		ui.Advance(300 * time.Millisecond)
		listPixel := ui.Image().RGBAAt(x, y)
		if !reduced && (movingPixel != filePixel || movingPixel == listPixel) {
			t.Fatal("file diff disappears or fades instead of sliding out to right", filePixel, movingPixel, listPixel)
		}
	}
}

func TestInspectorDividerTracksDragAndPersistsWidth(t *testing.T) {
	m := layoutModel()
	m.Inspectors["s"] = true
	m.Drafts["session:s"] = "keep draft"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	before, _ := ui.Find("更改侧栏")
	grip, ok := ui.Find("调整右侧栏宽度")
	if !ok {
		t.Fatal("inspector has no resize handle")
	}
	x, y := grip.X+grip.W/2, grip.Y+100
	ui.Press(x, y)
	ui.Move(x-150, y)
	ui.Frame()
	during, _ := ui.Find("更改侧栏")
	if math.Abs(float64(during.W-before.W-150)) > 1 {
		t.Fatal("inspector does not follow the divider while dragging", before, during)
	}
	ui.Release(x-150, y)
	ui.Advance(250 * time.Millisecond)
	history, _ := ui.Find("对话记录")
	if history.X+history.W > during.X+.5 {
		t.Fatal("resized inspector overlaps conversation", history, during)
	}
	if m.Drafts["session:s"] != "keep draft" {
		t.Fatal("resizing changes composer draft")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := layoutModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	restored.SessionID = "s"
	reopened := n.NewTester(New(restored, nil, "").View, 1440, 900)
	panel, _ := reopened.Find("更改侧栏")
	if math.Abs(float64(panel.W-during.W)) > .5 {
		t.Fatal("inspector width does not survive reopening", during, panel)
	}
}

func TestInspectorResizeBoundsInListAndFileViews(t *testing.T) {
	for _, width := range []int{900, 1440} {
		for _, file := range []bool{false, true} {
			m := layoutModel()
			m.Inspectors["s"] = true
			v := New(m, nil, "")
			if file {
				v.changesFor("s").selectFile("main.go")
			}
			ui := n.NewTester(v.View, width, 900)
			grip, _ := ui.Find("调整右侧栏宽度")
			x, y := grip.X+3, grip.Y+100
			ui.Press(x, y)
			ui.Move(x-2000, y)
			ui.Release(x-2000, y)
			ui.Advance(250 * time.Millisecond)
			panel, _ := ui.Find("更改侧栏")
			limit := float32(width - 256 - 360)
			if width <= 1160 {
				limit = float32(width - 224 - 16)
			}
			if panel.W != min(limit, 960) {
				t.Fatal("resize exceeds available width", width, file, panel)
			}
			grip, _ = ui.Find("调整右侧栏宽度")
			x = grip.X + 3
			ui.Press(x, y)
			ui.Move(x+2000, y)
			ui.Release(x+2000, y)
			ui.Advance(250 * time.Millisecond)
			panel, _ = ui.Find("更改侧栏")
			if panel.W != 280 {
				t.Fatal("resize lost minimum readable width", width, file, panel)
			}
		}
	}
}
