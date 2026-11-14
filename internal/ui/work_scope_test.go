package ui

import (
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestWorkInspectorShowsCurrentInputNewestFirst(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	d := v.Model.Details["s"]
	d.Items = []domain.Item{
		{ID: "old-input", Kind: "user", TurnID: "old", Text: "检查上一轮实现", Final: true},
		{ID: "past-source", Kind: "tool", TurnID: "old", Name: "上一轮任务", Final: true},
		{ID: "input", Kind: "user", TurnID: "t", Text: "继续检查代码和回归测试", Final: true},
		{ID: "first-source", Kind: "tool", TurnID: "t", Name: "较早的检查", Final: true},
		{ID: "last-source", Kind: "tool", TurnID: "t", Name: "最新的检查", Final: true},
	}
	d.Works = []domain.WorkActivity{
		{ID: "past", Kind: "agent", TurnID: "old", SourceItemID: "past-source", Title: "上一轮 Agent", State: "running"},
		{ID: "past-task", Kind: "background", TurnID: "old", SourceItemID: "past-source", Title: "上一轮任务", State: "running"},
		{ID: "first-agent", Kind: "agent", TurnID: "t", SourceItemID: "first-source", Title: "较早 Agent", State: "completed", Revision: 99},
		{ID: "first-task", Kind: "background", TurnID: "t", SourceItemID: "first-source", Title: "较早任务", State: "completed", Revision: 99},
		{ID: "last-agent", Kind: "agent", TurnID: "t", SourceItemID: "last-source", Title: "最新 Agent", State: "completed", Revision: 1},
		{ID: "last-task", Kind: "background", TurnID: "t", SourceItemID: "last-source", Title: "最新任务", State: "completed", Revision: 1},
	}
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "work-scope-lists.png")
	if ui.HasText("上一轮 Agent") || ui.HasText("上一轮任务") {
		t.Error("inspector includes work from an older turn")
	}
	for _, labels := range [][2]string{{"查看子 Agent：最新 Agent", "查看子 Agent：较早 Agent"}, {"查看后台任务：最新任务", "查看后台任务：较早任务"}} {
		latest, ok := ui.Find(labels[0])
		earliest, older := ui.Find(labels[1])
		if !ok || !older || latest.Y >= earliest.Y {
			t.Error("work list is not newest first", labels, latest, earliest)
		}
	}
	if err := ui.Click("查看子 Agent：最新 Agent"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	// An accepted same-turn insertion starts a fresh visible work scope.
	d.Items = append(d.Items, domain.Item{ID: "inserted", Kind: "user", TurnID: "t", Text: "继续", Final: true})
	d.Requests = []domain.PendingRequest{{ID: "older-question", WorkID: "last-agent", TurnID: "t", State: "pending"}}
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "work-scope-cleared.png")
	if !ui.HasText("暂无子 Agent") || !ui.HasText("暂无后台任务") || v.workPanel("agents").selected != "" {
		t.Error("new input retains older work or reopens an older request detail", ui.Texts())
	}
	if len(v.Model.Details["s"].Works) != 6 || len(v.Model.Details["s"].Requests) != 1 {
		t.Fatal("visible reset deletes authoritative work or pending requests")
	}
	// Late updates to an older source cannot repopulate the cleared list.
	d.Works[2].State, d.Works[2].Revision = "running", 100
	d.Items = append(d.Items, domain.Item{ID: "late-output", Kind: "assistant", TurnID: "old", Text: "迟到输出", Final: true})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("暂无子 Agent") || !ui.HasText("暂无后台任务") {
		t.Error("late work updates restore the previous input's roster")
	}
	// Work created by the current input appears normally.
	d.Items = append(d.Items, domain.Item{ID: "new-source", Kind: "tool", TurnID: "t", Final: true})
	d.Works = append(d.Works, domain.WorkActivity{ID: "new-agent", Kind: "agent", TurnID: "t", SourceItemID: "new-source", Title: "本次 Agent", State: "completed"}, domain.WorkActivity{ID: "new-task", Kind: "background", TurnID: "t", SourceItemID: "new-source", Title: "本次任务", State: "completed"})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "work-scope-current.png")
	if !ui.HasText("本次 Agent") || !ui.HasText("本次任务") || ui.HasText("较早 Agent") || ui.HasText("较早任务") {
		t.Error("current-input work does not replace the cleared roster")
	}
	ui.Click("查看子 Agent：本次 Agent")
	ui.Advance(time.Second)
	d.Items = append(d.Items, domain.Item{ID: "reply", Kind: "assistant", TurnID: "t", Text: "Agent 正文不清空列表", Final: true}, domain.Item{ID: "result", Kind: "request_result", TurnID: "t"}, domain.Item{ID: "queued", Kind: "queued", Text: "未投递的队列消息"}, domain.Item{ID: "child-input", WorkID: "last-agent", Kind: "user", TurnID: "t", Text: "子会话输入"})
	v.Model.Details["s"] = d
	v.Model.Drafts["session:s"] = "尚未发送的草稿"
	ui.Frame()
	ui.Advance(time.Second)
	if v.workPanel("agents").selected != "new-agent" || !ui.HasText("本次 Agent") {
		t.Fatal("non-user updates or drafts clear the current work selection")
	}
	// A new turn can have its accepted input before its turn ID is attached.
	d.Items = append(d.Items, domain.Item{ID: "next-input", Kind: "user", Text: "下一轮", Final: true})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "work-scope-next.png")
	if !ui.HasText("暂无子 Agent") || !ui.HasText("暂无后台任务") {
		t.Fatal("accepted new input waits for a turn receipt before clearing old work", ui.Texts())
	}
}
