package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestLongQuestionCanRevealAndKeepASelectionFromHiddenOptions(t *testing.T) {
	m := layoutModel()
	m.Preferences.InterfaceZoom = 1.5
	d := m.Details["s"]
	q := domain.Question{ID: "long", Text: "检查哪些文件？"}
	for i := range 9 {
		q.Options = append(q.Options, domain.Option{Label: fmt.Sprintf("范围 %d", i+1)})
	}
	d.Requests = []domain.PendingRequest{{ID: "r", SessionID: "s", State: "pending", Kind: "questions", Title: "需要回答", Questions: []domain.Question{q}}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "普通消息仍可编辑"
	ui := n.NewTester(New(m, nil, "").View, 850, 600)
	ui.Advance(time.Second)
	if ui.HasText("范围 9") {
		t.Fatal("the remaining options are mounted before expansion")
	}
	if err := ui.Click("显示全部 9 个选项"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	list, ok := ui.Find("问题选项内容")
	if !ok {
		t.Fatal("missing question scroll area")
	}
	ui.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 550)
	ui.Advance(time.Second)
	if err := ui.Click("范围 9"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("收起选项"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if m.Answers["s/r/long"] != "范围 9" || m.Drafts["session:s"] != "普通消息仍可编辑" || !ui.HasText("已选「范围 9」") {
		t.Fatal("collapsing hid which selection will be submitted")
	}
	list, _ = ui.Find("问题选项内容")
	prompt, visible := ui.Find(q.Text)
	if !visible || prompt.H < 29 || prompt.Y < list.Y {
		t.Fatalf("collapsed options clipped the question prompt: %v inside %v", prompt, list)
	}
}

func TestQuestionDistinguishesPresetFromRetainedCustomAnswer(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "r", SessionID: "s", State: "pending", Kind: "questions", Title: "需要回答",
		Questions: []domain.Question{{ID: "q", Text: "如何处理？", Options: []domain.Option{{Label: "继续"}}, Custom: true}}}}
	m.Details["s"] = d
	ctl := app.NewController("")
	ui := n.NewTester(New(m, ctl, "").View, 850, 600)
	if err := ui.Click("自定义答案"); err != nil {
		t.Fatal(err)
	}
	ui.Type("只核对原始记录")
	if err := ui.Click("继续"); err != nil {
		t.Fatal(err)
	}
	if !ui.HasText("将提交预设「继续」") || m.Answers["custom:s/r/q"] != "只核对原始记录" {
		t.Fatal("preset and saved custom answer are ambiguous")
	}
	if err := ui.Click("使用自定义答案"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("将提交自定义答案") {
		t.Fatal("switching to the saved draft did not update the submit source")
	}
	if err := ui.Click("提交答案"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, ctl, "request.respond")
	if got := job.Input.(protocol.AnswerInput).Answer.Answers["q"]; len(got) != 1 || got[0] != "只核对原始记录" {
		t.Fatalf("submitted %v instead of the active answer", got)
	}
}

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

func TestAnsweredRequestExpandsAtItsOriginalPosition(t *testing.T) {
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
	later, _ := ui.Find("问题发出后补充的消息。")
	answer, ok := ui.Find("已回答 · 逐项验收")
	if !ok || answer.Y >= later.Y || ui.HasText("选择哪种方式？") || ui.HasText("回答结果") {
		t.Fatal("collapsed answer must appear only on the original question", ui.Texts())
	}
	saveNativeSkillImage(t, ui, "question-receipt-collapsed.png")
	if err := ui.Click("已回答 · 逐项验收"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	later, _ = ui.Find("问题发出后补充的消息。")
	question, ok := ui.Find("选择哪种方式？")
	if !ok || question.Y <= answer.Y || question.Y >= later.Y || m.Drafts["session:s"] != "保留普通草稿" {
		t.Fatal("question details did not expand inline or changed the ordinary draft", ui.Texts())
	}
	saveNativeSkillImage(t, ui, "question-receipt-expanded.png")
}

func TestQuestionReceiptWaitsForConfirmationAndUsesSavedAnswers(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "request:q", Kind: "request", Text: "需要回答", Status: "submitting"}}
	d.Requests = []domain.PendingRequest{{ID: "q", SessionID: "s", Kind: "questions", State: "submitting", Receipt: []domain.ContentPart{
		{Label: "选择哪种方式？", Text: "逐项验收"}, {Label: "临时口令", Text: "已提交敏感答案"},
	}}}
	m.Details["s"] = d
	m.Connected = false
	ui := n.NewTester(New(m, nil, "").View, 1050, 720)
	if !ui.HasText("需要回答 · 待核对") || ui.HasText("已回答 2 个问题") {
		t.Fatal("unconfirmed answer presented as confirmed", ui.Texts())
	}
	d.Items[0].Status = "answered"
	d.Requests[0].State = "answered"
	m.Details["s"] = d
	m.Connected = true
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("已回答 2 个问题"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	if !ui.HasText("逐项验收") || !ui.HasText("已提交敏感答案") {
		t.Fatal("original history page lost its saved answer", ui.Texts())
	}
	ui.SetDark(true)
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "question-receipt-multiple-dark.png")
}

func TestApprovalDecisionAppearsOnlyOnItsOriginalRow(t *testing.T) {
	for _, example := range []struct {
		kind, decision, detail string
		paged, journalOnly     bool
	}{
		{kind: "approval", decision: "允许这一次"},
		{kind: "approval", decision: "始终允许此命令", paged: true},
		{kind: "approval", decision: "本会话允许"},
		{kind: "approval", decision: "拒绝", journalOnly: true},
		{kind: "permissions", decision: "已允许本轮访问", detail: "读取 /fixture"},
	} {
		t.Run(example.decision, func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			title := "允许执行此命令？"
			if example.kind == "permissions" {
				title = "允许额外访问？"
			}
			parts := []domain.ContentPart{{Text: example.decision}}
			if example.detail != "" {
				parts = append(parts, domain.ContentPart{Text: example.detail})
			}
			d.Items = []domain.Item{
				{ID: "request:r", Kind: "request", Text: title, Status: "answered", Final: true},
				{ID: "later", Kind: "assistant", Text: "请求之后发出的消息。", Final: true},
			}
			if !example.journalOnly {
				d.Requests = []domain.PendingRequest{{ID: "r", SessionID: "s", Kind: example.kind, State: "answered", Receipt: parts}}
			}
			if !example.paged {
				d.Items = append(d.Items, domain.Item{ID: "request-result:r", Kind: "request_result", Presentation: example.kind, Text: "审批结果", Parts: parts, Final: true})
			}
			m.Details["s"] = d
			ui := n.NewTester(New(m, nil, "").View, 1440, 900)
			later, _ := ui.Find("请求之后发出的消息。")
			label := title + " · " + example.decision
			source, ok := ui.Find(label)
			if !ok || source.Y >= later.Y || ui.HasText("回答结果") || ui.HasText(title+" · 已处理") {
				t.Fatal("decision should only appear at the original request", ui.Texts())
			}
			if example.detail != "" {
				if ui.HasText(example.detail) {
					t.Fatal("grant detail must begin collapsed")
				}
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				grant, visible := ui.Find(example.detail)
				later, _ = ui.Find("请求之后发出的消息。")
				if !visible || grant.Y <= source.Y || grant.Y >= later.Y {
					t.Fatal("grant detail should expand beneath the original row", ui.Texts())
				}
			}
		})
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
					v.questionField(c, theme, r, domain.Question{ID: "q", Text: "单选", Options: []domain.Option{{Label: "选项甲"}, {Label: "选项乙"}}}, false)
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
		if outside := img.RGBAAt(x-6, y); outside != top {
			t.Fatal("options introduced a second surface", multi)
		}
		gap := img.RGBAAt(x+2, y)
		if gap == top || gap != img.RGBAAt(x+int(first.W/2), y) {
			t.Fatal("hover background does not leave space before the radio or checkbox", multi, gap, top)
		}
	}
}

func TestFormOptionGrowsWithWrappedText(t *testing.T) {
	request := domain.PendingRequest{ID: "form", SessionID: "s"}
	v := New(layoutModel(), nil, "")
	field := domain.FormField{ID: "choices", Title: "范围", Type: "array", Options: []domain.FormOption{
		{Value: "short", Label: "一行"},
		{Value: "long", Label: "请核对所有未完成的审批事项和本机工作区的目录权限以及归档会话的恢复入口"},
	}}
	ui := n.NewTester(func(c *n.Context) { v.formField(c, c.Theme(), request, field) }, 220, 240)
	short, ok := ui.Find(field.Options[0].Label)
	long, wrapped := ui.Find(field.Options[1].Label)
	if !ok || !wrapped || short.H != 32 || long.H < 48 {
		t.Fatalf("choice heights single=%+v wrapped=%+v", short, long)
	}
}
