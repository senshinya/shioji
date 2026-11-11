package ui

import (
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

func dockingUI(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	m := layoutModel()
	m.Inspectors["s"] = true
	m.InspectorWidth = 360
	m.Drafts["session:s"] = "keep draft"
	d := m.Details["s"]
	d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "接入任务事件", Status: "running"}}}
	m.Details["s"] = d
	v := New(m, nil, "")
	s := v.changesFor("s")
	s.load = changesLoaded
	s.selectFile("main.go")
	s.Files = []domain.ChangedFile{{Path: "main.go"}}
	s.receiveFile(domain.FileVersions{Path: "main.go", Old: "old\n", New: "new\n"})
	s.Diff = &editor.DiffState{Old: editor.NewBuffer("old\n"), New: editor.NewBuffer("new\n"), Layout: editor.DiffUnified}
	return v, n.NewTester(v.View, 1440, 900)
}

func mergeTaskTab(t *testing.T, ui *n.Tester) {
	t.Helper()
	task, ok := ui.Find("任务")
	if !ok {
		t.Fatal("task icon missing")
	}
	// The fixed top-left icon stays available while a file diff is open.
	pane, _ := ui.Find("更改侧栏")
	ui.Press(task.X+task.W/2, task.Y+task.H/2)
	ui.Move(pane.X+26, 24)
	ui.Release(pane.X+26, 24)
	ui.Advance(300 * time.Millisecond)
}

func TestInspectorDragMergesWithoutLosingFileOrComposer(t *testing.T) {
	v, ui := dockingUI(t)
	mergeTaskTab(t, ui)
	tasks, ok := ui.Find("任务面板")
	if !ok || tasks.H != 900 {
		t.Fatal("dropping task icon next to Diff did not merge", tasks)
	}
	if _, ok := ui.Find("调整侧栏分区高度"); ok {
		t.Fatal("merged group still has horizontal splitter")
	}
	if err := ui.Click("文件更改"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if _, ok := ui.Find("返回更改列表"); !ok {
		t.Fatal("merged tab switch lost selected diff")
	}
	if v.changesFor("s").Path != "main.go" || v.Model.Drafts["session:s"] != "keep draft" {
		t.Fatal("docking changed file or composer")
	}
}

func TestInspectorDropPreviewCommitsOnlyOnRelease(t *testing.T) {
	_, ui := dockingUI(t)
	mergeTaskTab(t, ui)
	tab, ok := ui.Find("文件更改")
	if !ok {
		t.Fatal("merged Diff icon missing")
	}
	pane, _ := ui.Find("更改侧栏")
	x, y := pane.X+pane.W/2, float32(850)
	ui.Press(tab.X+tab.W/2, tab.Y+tab.H/2)
	ui.Move(x, y)
	ui.Advance(100 * time.Millisecond)
	preview, ok := ui.Find("下方分屏预览")
	if !ok || preview.Y < 450 || preview.X < pane.X {
		t.Fatal("bottom docking preview missing", preview)
	}
	if tasks, _ := ui.Find("任务面板"); tasks.H != 900 {
		t.Fatal("preview mutates layout before release", tasks)
	}
	ui.Release(x, y)
	ui.Advance(300 * time.Millisecond)
	tasks, _ := ui.Find("任务面板")
	diff, _ := ui.Find("文件更改区域")
	if tasks.Y != 0 || tasks.H != 450 || diff.Y != 450 || diff.H != 450 {
		t.Fatal("dragged Diff did not move to lower split", tasks, diff)
	}
	if _, ok := ui.Find("下方分屏预览"); ok {
		t.Fatal("preview remains after drop")
	}
}

func TestInspectorDragEscapeAndOutsideReleaseKeepLayout(t *testing.T) {
	for _, escape := range []bool{true, false} {
		_, ui := dockingUI(t)
		mergeTaskTab(t, ui)
		tab, ok := ui.Find("文件更改")
		if !ok {
			t.Fatal("merged Diff icon missing")
		}
		pane, _ := ui.Find("更改侧栏")
		ui.Press(tab.X+tab.W/2, tab.Y+tab.H/2)
		ui.Move(pane.X+100, 850)
		ui.Advance(100 * time.Millisecond)
		if escape {
			ui.Key(0, n.KeyEscape)
		} else {
			ui.Move(500, 500)
		}
		ui.Release(500, 500)
		ui.Advance(300 * time.Millisecond)
		if tasks, _ := ui.Find("任务面板"); tasks.H != 900 {
			t.Fatal("cancel/outside drop changed layout", escape, tasks)
		}
		if _, ok := ui.Find("下方分屏预览"); ok {
			t.Fatal("cancel leaves preview", escape)
		}
	}
}

func TestInspectorMergedTabSelectsTasks(t *testing.T) {
	_, ui := dockingUI(t)
	mergeTaskTab(t, ui)
	ui.Click("文件更改")
	ui.Advance(300 * time.Millisecond)
	ui.Click("任务")
	ui.Advance(300 * time.Millisecond)
	if tasks, ok := ui.Find("任务面板"); !ok || tasks.H != 900 {
		t.Fatal("task entry leaves merged Diff selected", tasks)
	}
}

func TestInspectorDockLayoutRestoresAfterRestart(t *testing.T) {
	for _, joined := range []bool{false, true} {
		v, ui := dockingUI(t)
		mergeTaskTab(t, ui)
		if !joined {
			tab, _ := ui.Find("文件更改")
			panel, _ := ui.Find("更改侧栏")
			ui.Press(tab.X+14, tab.Y+14)
			ui.Move(panel.X+100, 850)
			ui.Release(panel.X+100, 850)
			ui.Advance(300 * time.Millisecond)
		}
		path := filepath.Join(t.TempDir(), "client.json")
		if err := v.Model.Save(path); err != nil {
			t.Fatal(err)
		}
		restored, _ := dockingUI(t)
		if err := restored.Model.Load(path); err != nil {
			t.Fatal(err)
		}
		reopened := n.NewTester(restored.View, 1440, 900)
		tasks, ok := reopened.Find("任务面板")
		want := float32(450)
		if joined {
			want = 900
		}
		if !ok || tasks.H != want {
			t.Fatal("restart lost group/split layout", joined, tasks)
		}
		if !joined {
			if diff, _ := reopened.Find("文件更改区域"); diff.Y != 450 {
				t.Fatal("restart lost lower Diff", diff)
			}
		}
	}
}

func TestInspectorWithoutPlanKeepsDiffAndRestoresDockWhenPlanReturns(t *testing.T) {
	v, ui := dockingUI(t)
	mergeTaskTab(t, ui)
	d := v.Model.Details["s"]
	saved := d.Plan
	d.Plan = nil
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if diff, ok := ui.Find("文件更改区域"); !ok || diff.H != 900 {
		t.Fatal("absent plan hides Diff", diff)
	}
	d.Plan = saved
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if tasks, ok := ui.Find("任务面板"); !ok || tasks.H != 900 {
		t.Fatal("plan arrival loses merged selection", tasks)
	}
}

func TestInspectorMergedTabsKeepKeyboardSelection(t *testing.T) {
	_, ui := dockingUI(t)
	mergeTaskTab(t, ui)
	ui.Click("任务")
	ui.Key(0, n.KeyLeft)
	ui.Advance(300 * time.Millisecond)
	if _, ok := ui.Find("返回更改列表"); !ok {
		t.Fatal("keyboard arrow cannot select merged Diff")
	}
	ui.Key(0, n.KeyRight)
	ui.Advance(300 * time.Millisecond)
	if tasks, ok := ui.Find("任务面板"); !ok || tasks.H != 900 {
		t.Fatal("keyboard arrow cannot select merged task", tasks)
	}
}

func TestInspectorDockRespectsReducedMotion(t *testing.T) {
	_, ui := dockingUI(t)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	tab, _ := ui.Find("任务")
	pane, _ := ui.Find("更改侧栏")
	ui.Press(tab.X+14, tab.Y+14)
	ui.Move(pane.X+26, 24)
	ui.Release(pane.X+26, 24)
	ui.Frame()
	if tasks, ok := ui.Find("任务面板"); !ok || tasks.H != 900 {
		t.Fatal("reduced-motion merge animates geometry", tasks)
	}
	tab, _ = ui.Find("文件更改")
	ui.Press(tab.X+14, tab.Y+14)
	ui.Move(pane.X+100, 850)
	ui.Release(pane.X+100, 850)
	ui.Frame()
	if diff, ok := ui.Find("文件更改区域"); !ok || diff.Y != 450 || diff.H != 450 {
		t.Fatal("reduced-motion split animates geometry", diff)
	}
}

func TestInspectorNewSplitUsesHalfHeightForPreviewAndDrop(t *testing.T) {
	v, ui := dockingUI(t)
	// A previous custom size must not make the next newly created split tiny.
	v.Model.InspectorSplitHeight = 200
	ui.Frame()
	mergeTaskTab(t, ui)
	tab, _ := ui.Find("文件更改")
	pane, _ := ui.Find("更改侧栏")
	ui.Press(tab.X+14, tab.Y+14)
	ui.Move(pane.X+100, 850)
	ui.Advance(120 * time.Millisecond)
	preview, ok := ui.Find("下方分屏预览")
	if !ok || preview.Y != 456 || preview.H != 438 {
		t.Fatal("new split preview is not half-height", preview)
	}
	ui.Release(pane.X+100, 850)
	ui.Advance(300 * time.Millisecond)
	tasks, _ := ui.Find("任务面板")
	diff, _ := ui.Find("文件更改区域")
	if tasks.H != 450 || diff.Y != 450 || diff.H != 450 {
		t.Fatal("new split is not half-height", tasks, diff)
	}
}
