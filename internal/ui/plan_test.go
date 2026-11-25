package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestSavedACPPlanShowsRunningStep(t *testing.T) {
	m := layoutModel()
	m.Inspectors["s"] = true
	d := m.Details["s"]
	d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "检查旧记录", Status: "in_progress"}}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if _, ok := ui.Find("进行中：检查旧记录"); !ok {
		t.Fatal("saved ACP running step appeared as pending")
	}
}

func TestTaskDockStaysBelowDiffDuringFileNavigation(t *testing.T) {
	m := layoutModel()
	m.Inspectors["s"] = true
	m.InspectorWidth = 500
	d := m.Details["s"]
	d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "接入任务事件", Status: "running"}}}
	m.Details["s"] = d
	v := New(m, nil, "")
	v.changesFor("s").selectFile("main.go")
	ui := n.NewTester(v.View, 1440, 900)
	panel, _ := ui.Find("更改侧栏")
	tasks, ok := ui.Find("任务面板")
	diff, _ := ui.Find("文件更改区域")
	if !ok || tasks.Y+tasks.H != 900 || tasks.X != panel.X || diff.Y+diff.H != tasks.Y || diff.H < 240 {
		t.Fatal("tasks are not docked below diff in one sidebar", panel, diff, tasks)
	}
	if _, ok := ui.Find("返回更改列表"); !ok || !ui.HasText("接入任务事件") {
		t.Fatal("file diff and tasks are not visible together")
	}
	if ui.HasText("1 / 1") {
		t.Fatal("task progress ratio remains")
	}
	ui.Click("返回更改列表")
	ui.Advance(300 * time.Millisecond)
	if after, _ := ui.Find("任务面板"); after != tasks {
		t.Fatal("file navigation moves task dock", tasks, after)
	}
	if after, _ := ui.Find("更改侧栏"); after != panel {
		t.Fatal("file navigation changes sidebar width", panel, after)
	}
}

func TestTaskDockOpensAndClearsWithoutLosingComposer(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		m := layoutModel()
		m.Inspectors["s"] = true
		m.Drafts["session:s"] = "keep draft"
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		if _, ok := ui.Find("任务面板"); ok {
			t.Fatal("task dock exists without structured plan")
		}
		d := m.Details["s"]
		d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "task", Status: "pending"}}}
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		moving, ok := ui.Find("任务面板")
		diff, _ := ui.Find("文件更改区域")
		if !ok || diff.Y+diff.H != moving.Y || moving.Y+moving.H != 900 {
			t.Fatal("diff does not follow task dock's actual height", diff, moving)
		}
		if !reduced && (moving.H <= 0 || moving.H >= 450) {
			t.Fatal("task dock skipped entrance animation", moving)
		}
		ui.Advance(300 * time.Millisecond)
		d.Plan = &domain.Plan{TurnID: "next", Steps: []domain.PlanStep{}}
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		if !reduced {
			diff, _ := ui.Find("文件更改区域")
			if diff.H <= 450 || diff.H >= 900 {
				t.Fatal("task dock removal jumps instead of reclaiming height", diff)
			}
		}
		ui.Advance(300 * time.Millisecond)
		if _, ok := ui.Find("任务面板"); ok {
			t.Fatal("empty plan did not remove task dock")
		}
		if diff, _ := ui.Find("文件更改区域"); diff.H != 900 {
			t.Fatal("cleared task dock leaves unused space", diff)
		}
		if m.Drafts["session:s"] != "keep draft" {
			t.Fatal("task dock changed composer")
		}
	}
}

func TestTaskListRemainsScrollableWithLongExplanationAndNarrowWidth(t *testing.T) {
	for _, width := range []int{900, 1440} {
		m := layoutModel()
		m.Inspectors["s"] = true

		d := m.Details["s"]
		d.Plan = &domain.Plan{TurnID: "t", Explanation: strings.Repeat("先验证上下文。", 180)}
		for i := 0; i < 40; i++ {
			d.Plan.Steps = append(d.Plan.Steps, domain.PlanStep{Text: strings.Repeat("检查任务布局与状态更新", 5), Status: "pending"})
		}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, width, 650)
		list, ok := ui.Find("任务列表")
		if !ok || list.H < 100 || list.H > 325 || list.Y+list.H > 650 {
			t.Fatal("explanation steals task scrollport", width, list)
		}
		if _, ok := ui.Find("消息输入"); !ok {
			t.Fatal("task view hides ordinary composer")
		}
	}
}

func TestTaskStatusUsesGreenActivityDotAndGrayPendingDot(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		m.Inspectors["s"] = true

		d := m.Details["s"]
		d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "active", Status: "running"}, {Text: "pending", Status: "pending"}}}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.SetDark(dark)
		ui.Advance(300 * time.Millisecond)
		active, _ := ui.Find("进行中：active")
		im := ui.Image()
		green := im.RGBAAt(int(active.X+19), int(active.Y+18))
		if int(green.G)-int(green.R) < 25 || int(green.G)-int(green.B) < 20 {
			t.Fatal("active task is not a green dot", dark, green)
		}
		pending, ok := ui.Find("任务待处理")
		if !ok || pending.W != 6 || pending.H != 20 {
			t.Fatal("pending task is not a compact dot", pending)
		}
		y := int(pending.Y + pending.H/2)
		gray := im.RGBAAt(int(pending.X+pending.W/2), y)
		background := im.RGBAAt(int(pending.X-2), y)
		if gray == background || max(gray.R, gray.G, gray.B)-min(gray.R, gray.G, gray.B) > 16 {
			t.Fatal("pending task dot is not visible in neutral gray", gray)
		}
	}
}

func TestTaskDividerTracksPointerAndBounds(t *testing.T) {
	m := layoutModel()
	m.Inspectors["s"] = true
	d := m.Details["s"]
	d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "task", Status: "pending"}}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	grip, _ := ui.Find("调整侧栏分区高度")
	x, y := grip.X+100, grip.Y+3
	ui.Press(x, y)
	ui.Move(x, y-100)
	ui.Frame()
	dock, _ := ui.Find("任务面板")
	diff, _ := ui.Find("文件更改区域")
	if dock.H != 550 || diff.H != 350 || m.InspectorSplitHeight != 550 {
		t.Fatal("divider does not track pointer", dock, diff, m.InspectorSplitHeight)
	}
	ui.Move(x, y-2000)
	ui.Frame()
	dock, _ = ui.Find("任务面板")
	diff, _ = ui.Find("文件更改区域")
	if dock.H != 660 || diff.H != 240 {
		t.Fatal("task dock consumes diff minimum", dock, diff)
	}
	ui.Release(x, y-2000)
	grip, _ = ui.Find("调整侧栏分区高度")
	y = grip.Y + 3
	ui.Press(x, y)
	ui.Move(x, y+2000)
	ui.Frame()
	dock, _ = ui.Find("任务面板")
	if dock.H != 120 {
		t.Fatal("task dock below minimum", dock)
	}
	ui.Release(x, y+2000)
}
