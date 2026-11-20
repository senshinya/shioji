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
			if math.Abs(float64(r.X-heading.X-40)) > .5 {
				t.Errorf("tool details should be inset by one 16 DIP icon column: heading=%+v label=%+v", heading, r)
			}
		}
		footer, _ := ui.Find("回复信息")
		if !(done.Y > b.Y+b.H && footer.Y > done.Y+done.H) {
			t.Fatal("group completion is not before the separate turn footer")
		}
		x, y := int(a.X-16), int((a.Y+a.H+b.Y)/2)
		line, bg := ui.Image().RGBAAt(x, y), ui.Image().RGBAAt(x-4, y)
		if line == bg {
			t.Fatalf("tool icons are not connected by a visible line (dark=%t)", dark)
		}
		if ui.Image().RGBAAt(int(heading.X+8), y) != bg {
			t.Fatal("timeline rail starts under the title instead of the inset tool icons")
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

func TestRunningToolTimelineEndsAtItsLastIcon(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := timelineFixture(t, dark)
		d := v.Model.Details["s"]
		d.Items[1].Final, d.Items[1].Status, d.Items[1].Text = false, "running", ""
		d.Turns[0].Status = "running"
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		last, _ := ui.Find("second command")
		x := int(last.X - 16)
		for y := int(last.Y + 20); y < int(last.Y+last.H+8); y++ {
			if ui.Image().RGBAAt(x, y) != ui.Image().RGBAAt(x-4, y) {
				t.Fatalf("last running command leaves a rail below its icon (dark=%v, y=%d)", dark, y)
			}
		}
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
		want := "调用失败"
		if status == "declined" {
			want = "已拒绝"
		}
		if !ui.HasText(want) {
			t.Fatalf("%s call lost its terminal state", status)
		}
		if !ui.HasText("调用结束") || ui.HasText("执行失败") {
			t.Fatal("one unsuccessful call is presented as a group failure")
		}
	}
}

func TestToolIssuesStayWithTheirCallWhileMoreCommandsArrive(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := timelineFixture(t, dark)
		d := v.Model.Details["s"]
		code := 1
		d.Items[0].Status, d.Items[0].ExitCode = "failed", &code
		d.Turns[0].Status = "running"
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		issue, ok := ui.Find("退出码 1")
		first, _ := ui.Find("first command")
		if !ok || math.Abs(float64(issue.Y-first.Y)) > 1 {
			t.Fatal("command issue is not beside its own heading", issue, first)
		}
		for _, status := range []string{"running", "completed"} {
			if len(d.Items) == 2 {
				d.Items = append(d.Items, domain.Item{ID: "retry", TurnID: "t", Kind: "tool", Presentation: "command", Name: "retry command"})
			}
			d.Items[2].Status, d.Items[2].Final = status, status == "completed"
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			after, ok := ui.Find("退出码 1")
			if !ok || math.Abs(float64(after.Y-issue.Y)) > 1 {
				t.Fatal("later call moves or erases an earlier issue", issue, after)
			}
			if ui.HasText("执行失败") || ui.HasText("已完成") || ui.HasText("调用结束") || !ui.HasText("进行中") {
				t.Fatal("current batch completion prematurely ends the open tool group")
			}
		}
		ui.Click("执行工具")
		ui.Advance(200 * time.Millisecond)
		if !ui.HasText("1 项未成功") || ui.HasText("退出码 1") {
			t.Fatal("collapsed group loses the issue summary")
		}
		d.Items = append(d.Items, domain.Item{ID: "explanation", TurnID: "t", Kind: "assistant", Text: "The retry succeeded.", Final: true})
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Click("3 次工具调用")
		ui.Advance(time.Second)
		if !ui.HasText("调用结束") || !ui.HasText("退出码 1") || ui.HasText("1 项未成功") || ui.HasText("本轮失败") {
			t.Fatal("closed group loses its factual issues or claims the turn failed")
		}
	}
}

func TestToolGroupReceiptWaitsForAConfirmedBoundary(t *testing.T) {
	for _, status := range []domain.ExecutionState{"running", "starting", "unknown", "", "completed", "failed", "interrupted", "abandoned"} {
		v, ui := timelineFixture(t, false)
		d := v.Model.Details["s"]
		d.Turns[0].Status = status
		v.Model.Details["s"] = d
		ui.Frame()
		want := status == "completed" || status == "failed" || status == "interrupted" || status == "abandoned"
		if ui.HasText("已完成") != want {
			t.Fatalf("group completion guessed from turn status %q", status)
		}
	}
}

func TestTurnFailureIsDistinctAndShowsItsReason(t *testing.T) {
	v, ui := timelineFixture(t, false)
	d := v.Model.Details["s"]
	d.Turns[0].Status, d.Turns[0].Error = "failed", "Model request timed out"
	v.Model.Details["s"] = d
	ui.Frame()
	if !ui.HasText("本轮失败") || !ui.HasText("Model request timed out") || ui.HasText("执行失败") {
		t.Fatal("turn failure lacks a distinct label and actual reason")
	}
}

func TestCommandIssueFadesInWithoutMovingTheCall(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := timelineFixture(t, dark)
		d := v.Model.Details["s"]
		d.Turns[0].Status = "running"
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		before, _ := ui.Find("first command")
		code := 2
		d.Items[0].Status, d.Items[0].ExitCode = "failed", &code
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(50 * time.Millisecond)
		issue, _ := ui.Find("退出码 2")
		middle := toolLabelContrast(ui.Image(), issue, dark)
		ui.Advance(200 * time.Millisecond)
		issue, _ = ui.Find("退出码 2")
		settled := toolLabelContrast(ui.Image(), issue, dark)
		after, _ := ui.Find("first command")
		if middle >= settled-3 || math.Abs(float64(before.Y-after.Y)) > 1 {
			t.Fatal("issue flashes in or moves the call", dark, middle, settled, before, after)
		}
	}
}
