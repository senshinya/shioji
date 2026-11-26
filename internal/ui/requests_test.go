package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
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

func TestMultiQuestionPanelSwitchesPagesSupportsMultiSelectAndSubmitsTogether(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{
		ID: "ask", SessionID: "s", State: "pending", Revision: 1, Kind: "questions", Title: "需要回答",
		Questions: []domain.Question{
			{
				ID: "0", Header: "插件", Text: "要安装哪些插件？", MultiSelect: true, Custom: true,
				Options: []domain.Option{
					{Label: "ryugi", Description: "安装 ryugi"},
					{Label: "impeccable", Description: "安装 impeccable"},
				},
			},
			{
				ID: "1", Header: "作用域", Text: "安装到哪个作用域？", Custom: true,
				Options: []domain.Option{
					{Label: "User", Description: "所有项目可用"},
					{Label: "Project", Description: "仅当前项目"},
				},
			},
		},
	}}
	m.Details["s"] = d
	ctl := app.NewController("")
	ui := n.NewTester(New(m, ctl, "").View, 1100, 720)
	ui.Advance(time.Second)

	if !ui.HasText("要安装哪些插件？") || ui.HasText("安装到哪个作用域？") {
		t.Fatalf("multi-question panel should show one question per page: %v", ui.Texts())
	}
	if _, ok := ui.Find("1 / 2"); !ok {
		t.Fatalf("missing multi-question pager 1 / 2: %v", ui.Texts())
	}
	if !ui.HasText("还有 2 个问题未回答") {
		t.Fatalf("expected 2 unanswered questions indicator: %v", ui.Texts())
	}

	if err := ui.Click("ryugi"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("impeccable"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("自定义答案"); err != nil {
		t.Fatal(err)
	}
	ui.Type("extra-plugin")
	ui.Advance(200 * time.Millisecond)

	if !ui.HasText("还有 1 个问题未回答") {
		t.Fatalf("expected 1 unanswered question remaining after answering first page: %v", ui.Texts())
	}
	if err := ui.Click("提交答案"); err != nil {
		t.Fatal(err)
	}
	for len(ctl.Jobs) > 0 {
		if (<-ctl.Jobs).Method == "request.respond" {
			t.Fatal("partial answers must not submit before all questions are answered")
		}
	}

	if err := ui.Click("下一个请求"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("安装到哪个作用域？") || ui.HasText("要安装哪些插件？") {
		t.Fatalf("pager did not switch to second question: %v", ui.Texts())
	}
	if _, ok := ui.Find("2 / 2"); !ok {
		t.Fatalf("missing multi-question pager 2 / 2: %v", ui.Texts())
	}
	if err := ui.Click("User"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(200 * time.Millisecond)
	if ui.HasText("未回答") {
		t.Fatalf("unanswered indicator remained after all questions answered: %v", ui.Texts())
	}

	if err := ui.Click("提交答案"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, ctl, "request.respond")
	answer := job.Input.(protocol.AnswerInput).Answer
	if answer.Decision != "" {
		t.Fatalf("expected empty decision for questions answer, got %q", answer.Decision)
	}
	if !slices.Equal(answer.Answers["0"], []string{"ryugi", "impeccable", "extra-plugin"}) {
		t.Fatalf("multi-select answers mismatch: %#v", answer.Answers["0"])
	}
	if !slices.Equal(answer.Answers["1"], []string{"User"}) {
		t.Fatalf("single-select answer mismatch: %#v", answer.Answers["1"])
	}
}

func TestRequestRadioAndCheckboxIndicatorAlignsWithTextInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, tc := range []struct {
			name     string
			label    string
			radio    bool
			selected bool
			width    int
		}{
			{"radio-selected-han", "继续检查", true, true, 320},
			{"radio-unselected-han", "继续检查", true, false, 320},
			{"radio-selected-latin", "Continue", true, true, 320},
			{"radio-selected-mixed-wrapped", "Claude Code 检查并安装插件到用户目录的完整说明文本", true, true, 180},
			{"checkbox-checked-han", "继续检查", false, true, 320},
			{"checkbox-unchecked-latin", "Continue", false, false, 320},
		} {
			t.Run(fmt.Sprintf("dark=%v/%s", dark, tc.name), func(t *testing.T) {
				selected := ""
				if tc.selected {
					selected = tc.label
				}
				checked := tc.selected
				ui := n.NewTester(func(c *n.Context) {
					theme := c.Theme()
					n.Column(c).Fill().Padding(16).Background(theme.Surface).Children(func() {
						if tc.radio {
							requestRadio(c, theme, &selected, tc.label, tc.label)
						} else {
							requestCheckbox(c, theme, &checked, tc.label)
						}
					})
				}, tc.width, 120)
				ui.SetDark(dark)
				ui.SetScale(2)
				ui.Advance(time.Second)

				row, ok := ui.Find(tc.label)
				if !ok {
					t.Fatalf("missing choice row for %q", tc.label)
				}
				im := ui.Image()
				bg := im.RGBAAt(2, 2)
				const scale = 2
				indicatorRight := int((row.X + requestChoicePaddingX + requestChoiceIndicatorSize + 2) * scale)
				textLeft := int((row.X + requestChoiceTextIndent - 1) * scale)
				firstLineBottom := int((row.Y + requestChoicePaddingY + requestChoiceLineHeight) * scale)
				rowTop := int(row.Y * scale)
				rowRight := int((row.X + row.W - requestChoicePaddingX) * scale)

				inkBounds := func(x0, x1, y0, y1 int) (top, bottom int) {
					top, bottom = im.Bounds().Max.Y, -1
					for y := max(0, y0); y < min(im.Bounds().Max.Y, y1); y++ {
						for x := max(0, x0); x < min(im.Bounds().Max.X, x1); x++ {
							p := im.RGBAAt(x, y)
							if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 25 {
								top = min(top, y)
								bottom = max(bottom, y)
							}
						}
					}
					return top, bottom
				}

				indTop, indBottom := inkBounds(int(row.X*scale), indicatorRight, rowTop, firstLineBottom)
				txtTop, txtBottom := inkBounds(textLeft, rowRight, rowTop, firstLineBottom)
				if indBottom < 0 || txtBottom < 0 {
					t.Fatalf("missing rendered ink: indicator=[%d,%d] text=[%d,%d]", indTop, indBottom, txtTop, txtBottom)
				}
				indCenter := float64(indTop+indBottom) / (2 * scale)
				txtCenter := float64(txtTop+txtBottom) / (2 * scale)
				if math.Abs(indCenter-txtCenter) > 1.0 {
					t.Fatalf("indicator vertical center %.2f misaligned with first-line text ink center %.2f (ind=[%d,%d] txt=[%d,%d])",
						indCenter, txtCenter, indTop, indBottom, txtTop, txtBottom)
				}
			})
		}
	}
}

func TestClaudeMultiQuestionAndChoiceAlignmentSnapshots(t *testing.T) {
	m := layoutModel()
	m.Readiness.Harnesses = []domain.HarnessInfo{{
		ID: "claude-code", Name: "Claude Code", Icon: "claude", Configured: true, State: domain.RuntimeReady,
		Models:      []domain.Model{{ID: "claude-opus-4-6", Name: "claude-opus-4-6", Default: true}},
		Permissions: []string{"default"},
	}}
	m.Snapshot.Sessions[0].Title = "检查 Claude Code 并安装 ryugi 和 impeccable"
	m.Snapshot.Sessions[0].HarnessID = "claude-code"
	m.Snapshot.Sessions[0].Config = domain.RunConfig{Model: "claude-opus-4-6", Permission: "default"}
	d := m.Details["s"]
	d.Session = m.Snapshot.Sessions[0]
	d.Items = []domain.Item{
		{ID: "u1", Kind: "user", Text: "检查本机 Claude Code 环境，并安装 ryugi 和 impeccable。", Final: true},
		{ID: "a1", Kind: "assistant", Text: "已检查本机 Claude Code 配置与插件目录。在继续安装前，请先确认以下 3 个问题。", Final: true},
		{ID: "request:ask", Kind: "request", Text: "需要回答", Status: "pending"},
	}
	d.Requests = []domain.PendingRequest{{
		ID: "ask", SessionID: "s", State: "pending", Revision: 1, Kind: "questions", Title: "需要回答",
		Questions: []domain.Question{
			{
				ID: "0", Header: "插件", Text: "需要安装哪些插件？（可多选）", MultiSelect: true, Custom: true,
				Options: []domain.Option{
					{Label: "ryugi（设计画板与验收插件）", Description: "在本地画板中管理设计稿、验收截图与批注"},
					{Label: "impeccable（界面走查技能）", Description: "提供视觉细节检查、排版与一致性审查"},
				},
			},
			{
				ID: "1", Header: "作用域", Text: "希望安装到哪个作用域？", Custom: true,
				Options: []domain.Option{
					{Label: "User（全部项目可用，推荐）", Description: "写入 ~/.claude/plugins，对所有仓库生效"},
					{Label: "Project（仅当前仓库）", Description: "仅在当前工作区启用，便于按项目隔离"},
				},
			},
			{
				ID: "2", Header: "重启确认", Text: "安装完成后是否立即重新检测 Claude Code 能力？", Custom: true,
				Options: []domain.Option{
					{Label: "立即检测并刷新", Description: "安装后自动校验命令与插件注册状态"},
					{Label: "稍后手动检测", Description: "仅完成安装，保持当前会话状态不变"},
				},
			},
		},
	}}
	m.Details["s"] = d
	ctl := app.NewController("")
	ui := n.NewTester(New(m, ctl, "").View, 1100, 720)
	ui.SetScale(2)
	ui.Advance(time.Second)

	if err := ui.Click("ryugi（设计画板与验收插件）"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("impeccable（界面走查技能）"); err != nil {
		t.Fatal(err)
	}
	ui.Move(0, 0)
	ui.Advance(400 * time.Millisecond)
	saveNativeSkillImage(t, ui, "claude-multi-question-page1.png")

	if err := ui.Click("下一个请求"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("User（全部项目可用，推荐）"); err != nil {
		t.Fatal(err)
	}
	ui.Move(0, 0)
	ui.Advance(400 * time.Millisecond)
	saveNativeSkillImage(t, ui, "claude-multi-question-page2-radio.png")

	if err := ui.Click("下一个请求"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("立即检测并刷新"); err != nil {
		t.Fatal(err)
	}
	ui.Move(0, 0)
	ui.SetDark(true)
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "claude-multi-question-page3-ready-dark.png")

	// Side-by-side light & dark close-up of radio and checkbox alignment using Shioji's theme.
	renderPair := func(dark bool, name string) {
		selected := "User（全部项目可用，推荐）"
		checked := true
		unchecked := false
		closeup := n.NewTester(func(c *n.Context) {
			theme := applyAppearance(c, m.Preferences)
			n.Column(c).Fill().Padding(24).Gap(8).Background(theme.Background).Children(func() {
				n.Text(c, "单选与多选指示器对齐对比").FontSize(13).FontWeight(500).FixedLineHeight(20)
				n.Column(c).FillWidth().Padding(4).Background(theme.Surface).Border(1, theme.Border).Radius(8).Children(func() {
					requestRadio(c, theme, &selected, "User（全部项目可用，推荐）", "User（全部项目可用，推荐）")
					processingPanelDivider(c, theme)
					requestRadio(c, theme, &selected, "Project", "Project（仅当前仓库）")
					processingPanelDivider(c, theme)
					requestRadio(c, theme, &selected, "Wrapped", "超长多行选项：指示器应始终与首行中文及 Claude Code 英文文本的可见墨迹中心对齐")
					processingPanelDivider(c, theme)
					requestCheckbox(c, theme, &checked, "ryugi（已勾选多选项）")
					processingPanelDivider(c, theme)
					requestCheckbox(c, theme, &unchecked, "impeccable（未勾选多选项）")
				})
			})
		}, 460, 290)
		closeup.SetDark(dark)
		closeup.SetScale(2)
		closeup.Advance(time.Second)
		saveNativeSkillImage(t, closeup, name)
	}
	renderPair(false, "choice-alignment-light.png")
	renderPair(true, "choice-alignment-dark.png")
}
