package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestMCPMultiSelectRequiresExplicitSubmitAndKeepsComposer(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	minimum := 1
	d.Requests = []domain.PendingRequest{{ID: "form", SessionID: "s", Kind: "form", Title: "来自 checks 的请求", State: "pending", Revision: 1, Fields: []domain.FormField{{ID: "checks", Title: "检查项目", Type: "array", Required: true, MinItems: &minimum, Options: []domain.FormOption{{Value: "transport", Label: "连接恢复"}, {Value: "draft", Label: "草稿保留"}}}}}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "继续阅读"
	c := app.NewController("")
	ui := n.NewTester(New(m, c, "").View, 1440, 900)
	if err := ui.Click("连接恢复"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("草稿保留"); err != nil {
		t.Fatal(err)
	}
	for len(c.Jobs) > 0 {
		if (<-c.Jobs).Method == "request.respond" {
			t.Fatal("selection submitted form")
		}
	}
	if err := ui.Click("确认表单"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "request.respond" {
			a := job.Input.(protocol.AnswerInput).Answer
			if len(a.Answers["checks"]) != 2 || a.Decision != "accept" {
				t.Fatal("multi selection lost", a)
			}
			found = true
		}
	}
	if !found || m.Drafts["session:s"] != "继续阅读" {
		t.Fatal("submit lost request or ordinary draft")
	}
	if _, ok := ui.Find("消息输入"); !ok {
		t.Fatal("form displaced normal composer")
	}
}
func TestSecretAnswerNeverEntersSavedClientState(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "secret", SessionID: "s", Kind: "questions", Title: "临时口令", State: "pending", Revision: 1, Questions: []domain.Question{{ID: "q", Text: "输入临时口令", Secret: true}}}}
	m.Details["s"] = d
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1440, 900)
	if err := ui.Click("敏感答案"); err != nil {
		t.Fatal(err)
	}
	ui.Type("do-not-save-secret")
	ui.Frame()
	b, _ := m.EncodeState()
	if strings.Contains(string(b), "do-not-save-secret") {
		t.Fatal("secret persisted")
	}
	if err := ui.Click("提交答案"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "request.respond" {
			a := job.Input.(protocol.AnswerInput).Answer
			if a.Answers["q"][0] != "do-not-save-secret" {
				t.Fatal("secret response missing")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("secret answer could not be submitted")
	}
}
func TestPermissionPickerCanDeclineWriteAndGrantOnlyRead(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "p", SessionID: "s", Kind: "permissions", Title: "额外权限", State: "pending", Revision: 1, Choices: []domain.RequestChoice{{ID: "read:0", Label: "读取 /remote/read"}, {ID: "write:0", Label: "写入 /remote/write"}}}}
	m.Details["s"] = d
	c := app.NewController("")
	ui := n.NewTester(New(m, c, "").View, 1440, 900)
	if err := ui.Click("写入 /remote/write"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("允许本轮"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "request.respond" {
			a := job.Input.(protocol.AnswerInput).Answer
			if a.Decision != "turn" || len(a.Answers["permissions"]) != 1 || a.Answers["permissions"][0] != "read:0" {
				t.Fatal("unchecked write permission included", a)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("permission grant missing")
	}
}

func TestSourceRequestOpensItsOwnPage(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "request:second", Kind: "request", Text: "第二个请求", Status: "pending"}, {ID: "request:done", Kind: "request", Text: "已回答的问题", Status: "answered"}}
	d.Requests = []domain.PendingRequest{{ID: "first", SessionID: "s", Kind: "questions", Title: "第一个请求", State: "pending", Questions: []domain.Question{{ID: "a", Text: "问题 A"}, {ID: "b", Text: "问题 B"}}}, {ID: "second", SessionID: "s", Kind: "questions", Title: "第二个请求", State: "pending", Questions: []domain.Question{{ID: "c", Text: "问题 C"}}}, {ID: "done", SessionID: "s", Kind: "questions", Title: "已回答的问题", State: "answered", Receipt: []domain.ContentPart{{Label: "选择", Text: "已提交的答案"}}}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if err := ui.Click("第二个请求"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if m.Pager["s"] != 2 || !ui.HasText("问题 C") {
		t.Fatal("source request opens wrong page", m.Pager)
	}
}

func TestAnsweredRequestShowsResultAfterLaterMessages(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{
		{ID: "request:q", Kind: "request", Text: "需要回答", Status: "answered", Final: true},
		{ID: "later", Kind: "assistant", Text: "问题发出后补充的消息。", Final: true},
		{ID: "request-result:q", Kind: "request_result", Presentation: "questions", Text: "已提交回答", Parts: []domain.ContentPart{{Label: "选择哪种方式？", Text: "逐项验收"}}, Final: true},
	}
	d.Requests = []domain.PendingRequest{{ID: "q", SessionID: "s", State: "answered", Kind: "questions", Receipt: []domain.ContentPart{{Label: "选择哪种方式？", Text: "逐项验收"}}}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "保留普通草稿"
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	source, ok := ui.Find("需要回答 · 已处理")
	if !ok {
		t.Fatal("source lost its handled marker")
	}
	later, _ := ui.Find("问题发出后补充的消息。")
	answer, ok := ui.Find("逐项验收")
	if !ok || answer.Y <= later.Y || source.Y >= later.Y {
		t.Fatal("answer is not in a new result component after later messages", source, later, answer)
	}
	if m.Collapsed["receipt:request:q"] || !ui.HasText("选择哪种方式？") || m.Drafts["session:s"] != "保留普通草稿" {
		t.Fatal("answer result depends on source disclosure or changed the draft")
	}
}

func TestRequiredEmptyMultiSelectCanBeSubmitted(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	zero := 0
	d.Requests = []domain.PendingRequest{{ID: "empty", SessionID: "s", Kind: "form", Title: "空检查", State: "pending", Revision: 1, Fields: []domain.FormField{{ID: "checks", Title: "检查项", Type: "array", Required: true, MaxItems: &zero, Options: []domain.FormOption{{Value: "a", Label: "检查 A"}}}}}}
	m.Details["s"] = d
	c := app.NewController("")
	ui := n.NewTester(New(m, c, "").View, 1440, 900)
	if err := ui.Click("确认表单"); err != nil {
		t.Fatal(err)
	}
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "request.respond" {
			a := job.Input.(protocol.AnswerInput).Answer
			if values, present := a.Answers["checks"]; !present || len(values) != 0 {
				t.Fatal("empty selection omitted", a)
			}
			return
		}
	}
	t.Fatal("valid empty multiselect could not be submitted")
}

func TestOptionHoverKeepsEqualOuterInsetsAndSpaceBeforeTheControl(t *testing.T) {
	for _, multi := range []bool{false, true} {
		v := New(layoutModel(), nil, "")
		r := domain.PendingRequest{ID: "r", SessionID: "s", State: "pending"}
		ui := n.NewTester(func(c *n.Context) {
			theme := c.Theme()
			n.Column(c).Fill().Padding(20).Gap(8).Background(theme.Surface).Children(func() {
				if multi {
					v.formField(c, theme, r, domain.FormField{ID: "q", Title: "多选", Type: "array", Options: []domain.FormOption{{Value: "a", Label: "选项甲"}, {Value: "b", Label: "选项乙"}}})
				} else {
					v.questionField(c, theme, r, domain.Question{ID: "q", Text: "单选", Options: []domain.Option{{Label: "选项甲"}, {Label: "选项乙"}}})
				}
			})
		}, 400, 240)
		ui.Advance(time.Second)
		first, _ := ui.Find("选项甲")
		last, _ := ui.Find("选项乙")
		ui.Move(first.X+first.W/2, first.Y+first.H/2)
		ui.Advance(200 * time.Millisecond)
		img := ui.Image()
		x, y := int(first.X), int(first.Y+first.H/2)
		top := img.RGBAAt(x+30, int(first.Y)-2)
		if left, right, bottom := img.RGBAAt(x-2, y), img.RGBAAt(int(first.X+first.W)+2, y), img.RGBAAt(x+30, int(last.Y+last.H)+2); left != top || right != top || bottom != top {
			t.Fatal("option outer insets differ", multi, left, top, right, bottom)
		}
		if outside := img.RGBAAt(x-6, y); outside == top {
			t.Fatal("hover left inset is wider than the four-pixel top inset", multi)
		}
		gap := img.RGBAAt(x+2, y)
		if gap == top || gap != img.RGBAAt(x+int(first.W/2), y) {
			t.Fatal("hover background does not leave space before the radio or checkbox", multi, gap, top)
		}
	}
}
