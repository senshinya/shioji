package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

func timelineFixture(t *testing.T, dark bool) (*View, *n.Tester) {
	t.Helper()
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "a", TurnID: "t", Kind: "tool", Name: "first command", Text: "first result\nsecond line", Status: "completed", Final: true}, {ID: "b", TurnID: "t", Kind: "tool", Name: "second command", Text: "second result", Status: "completed", Final: true}}
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.SetDark(dark)
	if err := ui.Click("2 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(180 * time.Millisecond)
	return v, ui
}

func TestExpandedToolsShareTimelineRailAndEndWithCompleted(t *testing.T) {
	for _, dark := range []bool{false, true} {
		_, ui := timelineFixture(t, dark)
		heading, _ := ui.Find("2 次工具调用")
		a, _ := ui.Find("first command")
		b, _ := ui.Find("second command")
		done, ok := ui.Find("已完成")
		if !ok {
			t.Fatal("expanded completed calls have no timeline completion receipt")
		}
		for _, r := range []n.Rect{a, b, done} {
			if math.Abs(float64(r.X-heading.X-24)) > .5 {
				t.Fatalf("timeline label is not beside the icon column: heading=%+v label=%+v", heading, r)
			}
		}
		footer, _ := ui.Find("本轮完成")
		if !(done.Y > b.Y+b.H && footer.Y > done.Y+done.H) {
			t.Fatal("group completion is not before the separate turn footer")
		}
		x, y := int(heading.X+8), int((a.Y+a.H+b.Y)/2)
		line, bg := ui.Image().RGBAAt(x, y), ui.Image().RGBAAt(x-4, y)
		if line == bg {
			t.Fatalf("tool icons are not connected by a visible line (dark=%t)", dark)
		}
		ui.Click("2 次工具调用")
		ui.Advance(180 * time.Millisecond)
		if ui.HasText("已完成") || ui.HasText("first command") {
			t.Fatal("collapsed group leaves its timeline receipt visible")
		}
	}
}

func TestToolTimelineCompletionFollowsConfirmedToolLifecycle(t *testing.T) {
	v, ui := timelineFixture(t, false)
	d := v.Model.Details["s"]
	d.Items[1].Final = false
	d.Items[1].Status = "inProgress"
	v.Model.Details["s"] = d
	ui.Frame()
	if ui.HasText("已完成") {
		t.Fatal("unfinished tool group claims completion")
	}
	d.Items[1].Final = true
	d.Items[1].Status = "completed"
	v.Model.Details["s"] = d
	ui.Frame()
	if !ui.HasText("已完成") {
		t.Fatal("confirmed tool completion has no receipt")
	}
}

func TestFailedOrDeclinedToolsDoNotShowSuccessfulGroupReceipt(t *testing.T) {
	for _, status := range []string{"failed", "declined"} {
		v, ui := timelineFixture(t, false)
		d := v.Model.Details["s"]
		d.Items[1].Status = status
		v.Model.Details["s"] = d
		ui.Frame()
		if ui.HasText("已完成") {
			t.Fatalf("%s call is presented as completed successfully", status)
		}
		want := "执行失败"
		if status == "declined" {
			want = "已拒绝"
		}
		if !ui.HasText(want) {
			t.Fatalf("%s call lost its terminal state", status)
		}
	}
}
