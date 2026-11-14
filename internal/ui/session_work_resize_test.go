package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func longChildConversation(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	v, ui := sessionWorkFixture(t)
	key := v.Model.WorkKey("s", "agent-a")
	d := v.Model.WorkDetails[key]
	d.Items = nil
	for i := 0; i < 120; i++ {
		text := fmt.Sprintf("检查步骤 %03d：保持父节点引用与树的黑高。\n\n", i) + strings.Repeat("宽度改变时，这段原生 Markdown 应当正常换行，保留当前阅读位置。", 3) + "\n\n```go\nfunc verify(node *Node) bool {\n    return node != nil && node.Parent != node\n}\n```"
		d.Items = append(d.Items, domain.WorkItem{Item: domain.Item{ID: fmt.Sprint("step-", i), Kind: "assistant", Text: text, Final: true, Order: int64(i + 1)}, Revision: 2})
	}
	v.Model.WorkDetails[key] = d
	if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	return v, ui
}

// Resizing a long child conversation must only mount the visible history.
// Restoring an ordinary Scroll here makes this fail with all 120 steps mounted.
func TestChildDetailResizeBuildsOnlyVisibleHistory(t *testing.T) {
	v, ui := longChildConversation(t)
	grip, ok := ui.Find("调整右侧栏宽度")
	if !ok {
		t.Fatal("missing width grip")
	}
	x, y := grip.X+3, grip.Y+90
	ui.Press(x, y)
	for frame := 0; frame < 8; frame++ {
		ui.Move(x-float32(frame*12), y)
		ui.Advance(time.Second / 60)
	}
	ui.Release(x-84, y)
	ui.Advance(time.Second)
	mounted := 0
	for _, text := range ui.Texts() {
		if strings.HasPrefix(text, "检查步骤 ") {
			mounted++
		}
	}
	t.Logf("mounted history steps=%d width=%.0f", mounted, v.Model.InspectorWidth)
	if mounted == 0 || !ui.HasText("检查步骤 119") {
		t.Fatal("latest child history disappeared")
	}
	if mounted > 20 {
		t.Fatalf("resize rebuilt %d history steps outside the viewport", mounted)
	}
}

func TestChildDetailResizePreservesReadingAndFollow(t *testing.T) {
	v, ui := longChildConversation(t)
	state := v.workPanel("agents").detail("agent-a")
	if !state.list.AtEnd() || !state.list.FollowingEnd() {
		t.Fatal("child history did not open at latest")
	}
	resize := func(delta float32) {
		t.Helper()
		grip, _ := ui.Find("调整右侧栏宽度")
		x, y := grip.X+3, grip.Y+90
		ui.Press(x, y)
		for frame := 1; frame <= 12; frame++ {
			ui.Move(x+delta*float32(frame)/12, y)
			ui.Advance(time.Second / 60)
		}
		ui.Release(x+delta, y)
		ui.Advance(time.Second)
	}
	resize(-150)
	if !state.list.AtEnd() || !state.list.FollowingEnd() {
		t.Fatal("width reflow lost latest following")
	}
	port, ok := ui.Find("子 Agent内容")
	if !ok {
		t.Fatal("missing child scroll port")
	}
	ui.Scroll(port.X+80, port.Y+100, 0, -180)
	ui.Advance(time.Second)
	if state.list.FollowingEnd() {
		t.Fatal("mouse wheel did not pause latest following")
	}
	state.list.ScrollTo(55, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	first, _ := state.list.Visible()
	if state.list.FollowingEnd() {
		t.Fatal("reading history kept following latest")
	}
	resize(120)
	after, _ := state.list.Visible()
	if first != after {
		t.Fatalf("width reflow moved reading anchor: %d -> %d", first, after)
	}
	key := v.Model.WorkKey("s", "agent-a")
	d := v.Model.WorkDetails[key]
	d.Items = append(d.Items, domain.WorkItem{Item: domain.Item{ID: "new-output", Kind: "assistant", Text: "最新独立输出", Final: true, Order: 121}, Revision: 3})
	v.Model.WorkDetails[key] = d
	ui.Frame()
	ui.Advance(time.Second)
	after, _ = state.list.Visible()
	if first != after {
		t.Fatalf("append moved history reader: %d -> %d", first, after)
	}
	if err := ui.Click("返回子 Agent列表"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	after, _ = state.list.Visible()
	if first != after {
		t.Fatalf("reopening child lost reading anchor: %d -> %d", first, after)
	}
	if err := ui.Click("跟随最新输出"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !state.list.AtEnd() || !ui.HasText("最新独立输出") {
		t.Fatal("resume did not reach appended output")
	}
	resize(-60)
	d.Items = append(d.Items, domain.WorkItem{Item: domain.Item{ID: "next-output", Kind: "assistant", Text: "继续实时输出", Final: true, Order: 122}, Revision: 4})
	v.Model.WorkDetails[key] = d
	ui.Frame()
	ui.Advance(time.Second)
	if !state.list.AtEnd() || !state.list.FollowingEnd() || !ui.HasText("继续实时输出") {
		t.Fatal("resize broke subsequent output following")
	}
	if v.Model.Drafts["session:s"] != "keep draft" {
		t.Fatal("resize changed parent composer")
	}
}
