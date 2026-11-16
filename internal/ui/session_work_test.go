package ui

import (
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func sessionWorkFixture(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	m := layoutModel()
	m.Inspectors["s"] = true
	m.InspectorWidth = 380
	m.Drafts["session:s"] = "keep draft"
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "保留结构化计划", Status: "running"}}}
	d.Works = []domain.WorkActivity{{ID: "agent-a", Kind: "agent", Title: "检查边界条件", Brief: "检查根节点与空子树", TurnID: "t", State: "running", Latest: "读取 rotate.go", Revision: 1}, {ID: "command-a", Kind: "background", Title: "运行回归测试", Brief: "go test -race ./...", TurnID: "t", State: "running", Latest: "TestRotateLeft", Revision: 2}}
	d.Items[0].Text = "旋转实现已检查，子任务仍在独立验证边界与测试。"
	d.Session.Title = "检查红黑树旋转"
	for i := range m.Snapshot.Sessions {
		if m.Snapshot.Sessions[i].ID == "s" {
			m.Snapshot.Sessions[i].Title = d.Session.Title
		}
	}
	for i := range m.Snapshot.Workspaces {
		m.Snapshot.Workspaces[i].Name = "红黑树"
	}
	m.Details["s"] = d
	m.MergeWorkDetail(domain.WorkDetail{SessionID: "s", Work: d.Works[0], Sequence: 2, Items: []domain.WorkItem{{Item: domain.Item{ID: "child-reply", Kind: "assistant", Text: "子 Agent 正在检查父节点引用", Final: true}, Revision: 2}}})
	m.MergeWorkDetail(domain.WorkDetail{SessionID: "s", Work: d.Works[1], Sequence: 2, Items: []domain.WorkItem{{Item: domain.Item{ID: "output", Kind: "tool", Presentation: "command", Text: "first\nlast", Status: "running"}, Revision: 2}}})
	v := New(m, nil, "")
	v.changesFor("s").load = changesLoaded
	v.changesFor("s").Files = []domain.ChangedFile{{Path: "rotate.go"}}
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	return v, ui
}
func TestSessionWorkViewsKeepIndependentDetailsAndComposer(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	upper, ok := ui.Find("子 Agent 面板")
	if !ok {
		t.Fatal("subagent view missing", ui.Texts())
	}
	lower, ok := ui.Find("后台任务面板")
	if !ok || upper.Y+upper.H != lower.Y || lower.Y+lower.H != 900 {
		t.Fatal("work views not split in same sidebar", upper, lower)
	}
	if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("查看后台任务：运行回归测试"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("子 Agent 正在检查父节点引用") || !ui.HasText("first\nlast") {
		t.Fatal("details replaced the other view", ui.Texts())
	}
	ui.Click("任务")
	ui.Advance(time.Second)
	ui.Click("后台任务")
	ui.Advance(time.Second)
	if !ui.HasText("子 Agent 正在检查父节点引用") || !ui.HasText("first\nlast") {
		t.Fatal("tab change lost independent details", ui.Texts())
	}
	ui.Click("返回子 Agent 列表")
	ui.Advance(time.Second)
	if !ui.HasText("first\nlast") || v.Model.Drafts["session:s"] != "keep draft" || len(v.Model.Details["s"].Items) != 1 {
		t.Fatal("back changed other view, composer or transcript")
	}
}

func TestSessionWorkNativeAppearance(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			v, ui := sessionWorkFixture(t)
			d := v.Model.Details["s"]
			duration := int64(12300)
			d.Works = append(d.Works, domain.WorkActivity{ID: "complete-agent", Kind: "agent", TurnID: "t", Title: "检查红黑树不变量", Latest: "节点颜色与黑高保持一致", State: "completed", DurationMS: &duration}, domain.WorkActivity{ID: "complete-background", Kind: "background", TurnID: "t", Title: "go test ./...", Latest: "ok  tree/rotation", State: "completed", DurationMS: &duration})
			v.Model.Details["s"] = d
			ui.SetDark(dark)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "work-"+name+"-lists.png")
			ui.Click("查看子 Agent：检查边界条件")
			ui.Advance(time.Second)
			ui.Click("查看后台任务：运行回归测试")
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "work-"+name+"-details.png")
			ui.SetSize(1050, 720)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "work-"+name+"-narrow.png")
			ui.SetSize(1440, 900)
			ui.Advance(time.Second)
			v.Model.Connected = false
			ui.Frame()
			ui.Advance(time.Second)
			if ui.HasText("进行中") {
				t.Fatal("offline details claim live work", ui.Texts())
			}
			saveNativeSkillImage(t, ui, "work-"+name+"-offline.png")
		})
	}
}

func TestSessionWorkDockingAndArrivalKeepSelections(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	ui.Click("查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	// Drag all lower views into the upper group, then split one out again.
	for _, label := range []string{"任务", "后台任务"} {
		tab, ok := ui.Find(label)
		if !ok {
			t.Fatal("missing tab", label)
		}
		panel, _ := ui.Find("更改侧栏")
		ui.Press(tab.X+14, tab.Y+14)
		ui.Move(panel.X+30, 24)
		ui.Release(panel.X+30, 24)
		ui.Advance(time.Second)
	}
	if _, ok := ui.Find("调整侧栏分区高度"); ok {
		t.Fatal("merging all tabs left a second group")
	}
	tab, _ := ui.Find("后台任务")
	panel, _ := ui.Find("更改侧栏")
	ui.Press(tab.X+14, tab.Y+14)
	ui.Move(panel.X+180, 850)
	ui.Advance(100 * time.Millisecond)
	preview, ok := ui.Find("下方分屏预览")
	if !ok || preview.H != 438 {
		t.Fatal("split preview does not match half height", preview)
	}
	ui.Release(panel.X+180, 850)
	ui.Advance(time.Second)
	if lower, ok := ui.Find("后台任务面板"); !ok || lower.H != 450 {
		t.Fatal("split not half height", lower)
	}
	ui.Click("子 Agent")
	ui.Advance(time.Second)
	if !ui.HasText("子 Agent 正在检查父节点引用") {
		t.Fatal("docking lost selected detail")
	}
	d := v.Model.Details["s"]
	d.Works = append(d.Works, domain.WorkActivity{ID: "new", Kind: "agent", TurnID: "t", Title: "后到任务", State: "running", Revision: 3})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if v.workPanel("agents").selected != "agent-a" || !ui.HasText("子 Agent 正在检查父节点引用") {
		t.Fatal("arrival stole selection")
	}
}

func TestBackgroundDetailFollowsAppendsAndPausesWhileReading(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	key := v.Model.WorkKey("s", "command-a")
	d := v.Model.WorkDetails[key]
	d.Items[0].Item.Text = strings.Repeat("stable output line\n", 80)
	v.Model.WorkDetails[key] = d
	ui.Click("查看后台任务：运行回归测试")
	ui.Advance(time.Second)
	state := v.workPanel("background").detail("command-a")
	if state.scroll.MaxY <= 0 || state.scroll.MaxY-state.scroll.Y > 1 {
		t.Fatal("initial output not at end", state.scroll)
	}
	port, _ := ui.Find("后台任务内容")
	ui.Scroll(port.X+100, port.Y+100, 0, -180)
	ui.Advance(time.Second)
	before := state.scroll.Y
	if state.following {
		t.Fatal("user read did not pause follow")
	}
	d.Items[0].Item.Text += "new final line\n"
	v.Model.WorkDetails[key] = d
	ui.Frame()
	ui.Advance(time.Second)
	if state.scroll.Y != before {
		t.Fatal("append moved a reader", before, state.scroll.Y)
	}
	if err := ui.Click("跟随最新输出"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if state.scroll.MaxY-state.scroll.Y > 1 {
		t.Fatal("resume did not reach last line", state.scroll)
	}
	d.Items[0].Item.Text += strings.Repeat("batch append\n", 20)
	v.Model.WorkDetails[key] = d
	ui.Frame()
	ui.Advance(time.Second)
	if state.scroll.MaxY-state.scroll.Y > 1 {
		t.Fatal("multi-line append lost follow", state.scroll)
	}
}

func TestWorkReadFailureWaitsForRetryAndEvictionReloads(t *testing.T) {
	v, _ := sessionWorkFixture(t)
	v.Controller = app.NewController("")
	w := v.Model.Details["s"].Works[0]
	u := v.workPanel("agents").detail(w.ID)
	v.readSessionWork(w, u, false)
	job := <-v.Controller.Jobs
	job.Apply(v.Model, nil, errors.New("temporarily unavailable"))
	v.readSessionWork(w, u, false)
	select {
	case <-v.Controller.Jobs:
		t.Error("failed read retried without user action")
	default:
	}
	if u.phase != workReadFailed || u.error == "" {
		t.Error("failed state cleared")
	}
	u.phase = workRead
	delete(v.Model.WorkDetails, v.Model.WorkKey("s", w.ID))
	v.readSessionWork(w, u, false)
	select {
	case <-v.Controller.Jobs:
	default:
		t.Error("evicted detail not reloaded")
	}
}

func TestChildToolResourcesRenderNativeFile(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	key := v.Model.WorkKey("s", "agent-a")
	d := v.Model.WorkDetails[key]
	d.Items = append(d.Items, domain.WorkItem{Item: domain.Item{ID: "resource", Kind: "tool", Presentation: "files", Name: "生成审阅文件", Final: true, Parts: []domain.ContentPart{{Kind: "file", Attachment: &domain.Attachment{ID: "report", Name: "review.txt", MIME: "text/plain", Size: 42}}}}})
	v.Model.WorkDetails[key] = d
	ui.Click("查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	ui.Click("生成审阅文件")
	ui.Advance(time.Second)
	if !ui.HasText("review.txt") {
		t.Fatal("child native file result was lost", ui.Texts())
	}

}

func TestParallelWorkReadsShareBoundedClientCache(t *testing.T) {
	m := layoutModel()
	m.SecondarySessionID = "secondary"
	d := m.Details["s"]
	d.Session.ID = "secondary"
	m.Details["secondary"] = d
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, d.Session)
	root := New(m, app.NewController(""), "")
	for i := 0; i < 40; i++ {
		clone := *m
		clone.SessionID = "secondary"
		child := New(&clone, root.Controller, "")
		child.parent = root
		child.readSessionWork(domain.WorkActivity{ID: fmt.Sprint(i), Revision: 1}, &workDetailUI{}, false)
		select {
		case <-root.Controller.Jobs:
		default:
			t.Fatal("detail read not sent")
		}
	}
	if len(m.WorkDetails) > 32 {
		t.Fatal("secondary projections bypassed detail cache limit", len(m.WorkDetails))
	}
}

func TestEmptyWorkRosterKeepsExistingInspectorViews(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	d := v.Model.Details["s"]
	d.Works = nil
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("子 Agent 面板"); !ok {
		t.Fatal("history replacement unexpectedly removed work views")
	}
	if !ui.HasText("暂无子 Agent") || !ui.HasText("暂无后台任务") {
		t.Fatal("empty views missing", ui.Texts())
	}
}
