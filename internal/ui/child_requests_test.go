package ui

import (
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func childRequestsFixture(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	v, ui := sessionWorkFixture(t)
	d := v.Model.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "child-q", SessionID: "s", WorkID: "agent-a", TurnID: "t", Kind: "questions", Title: "需要回答", State: "pending", Revision: 1, Questions: []domain.Question{{ID: "q", Text: "是否同时覆盖根节点与空子树？", Custom: true, Options: []domain.Option{{Label: "同时覆盖根节点与空子树", Description: "检查根节点替换与空子树"}, {Label: "只检查普通节点", Description: "保留当前检查范围"}}}}}, {ID: "root-q", SessionID: "s", TurnID: "t", Kind: "questions", Title: "需要回答", State: "pending", Revision: 1, Questions: []domain.Question{{ID: "q", Text: "主会话的问题", Custom: true}}}}
	d.Items = append(d.Items, domain.Item{ID: "request:child-q", WorkID: "agent-a", TurnID: "t", Kind: "request", Text: "需要回答", Status: "pending", Order: 2})
	v.Model.Details["s"] = d
	key := v.Model.WorkKey("s", "agent-a")
	child := v.Model.WorkDetails[key]
	child.Items = append(child.Items, domain.WorkItem{Item: d.Items[1], Revision: 3})
	v.Model.WorkDetails[key] = child
	ui.Frame()
	ui.Advance(time.Second)
	return v, ui
}
func TestChildRequestOriginAndWaitingLinkPreserveIndependentDrafts(t *testing.T) {
	v, ui := childRequestsFixture(t)
	v.Model.Answers["s/child-q/q"] = "包含根节点"
	ui.Frame()
	if !ui.HasText("等待回答") {
		t.Fatal("child list has no waiting cue", ui.Texts())
	}
	if err := ui.Click("查看来源：检查边界条件"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("子 Agent 正在检查父节点引用") || !ui.HasText("需要回答") {
		t.Fatal("origin did not reveal child detail", ui.Texts())
	}
	if v.Model.Drafts["session:s"] != "keep draft" || v.Model.Answers["s/child-q/q"] != "包含根节点" {
		t.Fatal("origin changed a draft")
	}
	if err := ui.Click("返回子 Agent列表"); err != nil {
		saveNativeSkillImage(t, ui, "debug-origin.png")
		t.Fatal(err, v.workPanel("agents").selected, ui.Texts())
	}
	ui.Advance(time.Second)
	ui.Click("下一个请求")
	ui.Advance(time.Second)
	if err := ui.Click("处理子 Agent 请求：检查边界条件"); err != nil {
		t.Fatal(err, ui.Texts(), v.workPanel("agents").selected)
	}
	ui.Advance(time.Second)
	if v.Model.Pager["s"] != 0 || v.Model.Collapsed["s"] {
		t.Fatal("waiting link opened wrong request")
	}
	v.Model.Connected = false
	ui.Frame()
	if ui.HasText("等待回答") {
		t.Fatal("offline child presents a live waiting action")
	}
}
func TestChildRequestSubmitLocksOnlyItselfAndShowsFailureInline(t *testing.T) {
	v, ui := childRequestsFixture(t)
	controller := app.NewController("")
	v.Controller = controller
	v.Model.Answers["s/child-q/q"] = "包含根节点"
	ui.Frame()
	ui.Click("提交答案")
	ui.Frame()
	ui.Click("提交答案")
	ui.Frame()
	var jobs []app.Job
	for len(controller.Jobs) > 0 {
		j := <-controller.Jobs
		if j.Method == "request.respond" {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) != 1 {
		t.Fatal("submission queued more than once", len(jobs))
	}
	jobs[0].Apply(v.Model, nil, errors.New("暂时无法发送，请重试"))
	ui.Frame()
	ui.Advance(time.Second)
	body, _ := ui.Find("请求内容区域")
	message, ok := ui.Find("暂时无法发送，请重试")
	if !ok || message.Y < body.Y || message.Y > body.Y+body.H {
		t.Fatal("request error is not inline", body, message)
	}
	if v.Model.Answers["s/child-q/q"] != "包含根节点" || v.Model.Pager["s"] != 0 {
		t.Fatal("failed response lost draft/page")
	}
	ui.Click("提交答案")
	ui.Frame()
	if len(controller.Jobs) == 0 {
		t.Fatal("failed request cannot retry")
	}
}
func TestChildDetailShowsRequestSourceAndLatestReceipt(t *testing.T) {
	v, ui := childRequestsFixture(t)
	ui.Click("查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	// A source inside the child pane must navigate the shared dock, never create
	// a second form or an unrelated child composer.
	v.Model.Pager["s"] = 1
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("子 Agent 请求来源：需要回答"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Advance(time.Second)
	if v.Model.Pager["s"] != 0 {
		t.Fatal("child source did not target its request")
	}
	d := v.Model.Details["s"]
	d.Requests[0].State = "answered"
	d.Items[1].Status = "answered"
	v.Model.Details["s"] = d
	key := v.Model.WorkKey("s", "agent-a")
	child := v.Model.WorkDetails[key]
	child.Items[1].Item.Status = "answered"
	child.Items = append(child.Items, domain.WorkItem{Item: domain.Item{ID: "request-result:child-q", WorkID: "agent-a", TurnID: "t", Kind: "request_result", Presentation: "questions", Final: true, Order: 4, Parts: []domain.ContentPart{{Text: "同时覆盖根节点与空子树"}}}, Revision: 4})
	v.Model.WorkDetails[key] = child
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("已回答 · 同时覆盖根节点与空子树") || ui.HasText("回答结果") {
		t.Fatal("child receipt was not folded into its original source", ui.Texts())
	}
}
