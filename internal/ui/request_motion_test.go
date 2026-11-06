package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestAppendingRequestKeepsCurrentCardPaintAndDraft(t *testing.T) {
	for _, collapsed := range []bool{false, true} {
		t.Run(fmt.Sprint(collapsed), func(t *testing.T) {
			v := requestMotionModel()
			d := v.Model.Details["s"]
			d.Requests[0].Questions = d.Requests[0].Questions[:1]
			v.Model.Details["s"] = d
			v.Model.Collapsed["s"] = collapsed
			v.Model.Answers["s/questions/short"] = "保留这份答案"
			ui := n.NewTester(v.View, 1440, 900)
			ui.Advance(time.Second)
			heading, _ := ui.Find("需要回答")
			background := ui.Image().RGBAAt(int(heading.X-8), int(heading.Y+8))
			d.Requests = append(d.Requests, domain.PendingRequest{ID: "new", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "新追加的问题", Custom: true}}})
			v.Model.Details["s"] = d
			for i := 0; i < 30; i++ {
				ui.Frame()
				heading, _ = ui.Find("需要回答")
				got := ui.Image().RGBAAt(int(heading.X-8), int(heading.Y+8))
				if got != background {
					t.Fatalf("card flashes on appended request, frame %d: %v -> %v", i, background, got)
				}
				if v.Model.Pager["s"] != 0 || v.Model.Collapsed["s"] != collapsed || v.Model.Answers["s/questions/short"] != "保留这份答案" {
					t.Fatal("arrival changed current question state")
				}
				if !collapsed && !ui.HasText("简短问题") {
					t.Fatal("current question disappears on arrival")
				}
				ui.Advance(time.Second / 60)
			}
		})
	}
}

func TestAppendedQuestionHasVisibleNoticeWithoutChangingTheCurrentPage(t *testing.T) {
	for _, collapsed := range []bool{false, true} {
		v := requestMotionModel()
		ui := n.NewTester(v.View, 1440, 900)
		ui.Advance(time.Second)
		v.Model.Collapsed["s"] = collapsed
		ui.Frame()
		ui.Advance(time.Second)
		d := v.Model.Details["s"]
		d.Requests = append(d.Requests, domain.PendingRequest{ID: "new", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "new", Text: "新追加的问题", Custom: true}}})
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(400 * time.Millisecond)
		if !ui.HasText("新增 1 个问题") || ui.HasText("新追加的问题") {
			t.Fatal("append notice is missing or repeats the hidden question body")
		}
		if v.Model.Collapsed["s"] != collapsed || v.Model.Pager["s"] != 0 {
			t.Fatal("notice changed current disclosure or page")
		}
		if err := ui.Click("查看新增问题"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(400 * time.Millisecond)
		if v.Model.Pager["s"] != 2 || v.Model.Collapsed["s"] {
			t.Fatal("explicit view action did not open the newly arrived question")
		}
	}
}

func TestRequestPaginationDoesNotChangeTheHeaderGeometry(t *testing.T) {
	var gap float32
	for count := 1; count <= 2; count++ {
		v := requestMotionModel()
		d := v.Model.Details["s"]
		d.Requests[0].Questions = d.Requests[0].Questions[:count]
		v.Model.Details["s"] = d
		ui := n.NewTester(v.View, 1440, 900)
		ui.Advance(time.Second)
		heading, _ := ui.Find("需要回答")
		body, _ := ui.Find("请求内容区域")
		if count == 1 {
			gap = body.Y - heading.Y
		} else if body.Y-heading.Y != gap {
			t.Fatal("pagination changes the title bar geometry", gap, body.Y-heading.Y)
		}
	}
}

func TestLastRequestShrinksOutWithTheDock(t *testing.T) {
	v := requestMotionModel()
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("会话底部面板")
	d := v.Model.Details["s"]
	d.Requests[0].State = "answered"
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	middle, _ := ui.Find("会话底部面板")
	ui.Advance(500 * time.Millisecond)
	after, _ := ui.Find("会话底部面板")
	if !(after.H < middle.H && middle.H < before.H) {
		t.Fatal("request removal drops the dock height before the card exits", before, middle, after)
	}
	if ui.FramePending() {
		t.Fatal("closed request card keeps rebuilding")
	}
}

func requestMotionModel() *View {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "questions", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "short", Text: "简短问题", Custom: true}, {ID: "long", Text: strings.Repeat("解释这个较长的问题，并保留正在输入的普通草稿。", 12), Custom: true}}}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "ordinary draft"
	return New(m, nil, "")
}

func TestRequestPagerSlidesContentWhileResizingItsHeight(t *testing.T) {
	v := requestMotionModel()
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("需要回答")
	composer, _ := ui.Find("消息编辑器")
	ui.Click("下一个请求")
	ui.Advance(70 * time.Millisecond)
	middle, _ := ui.Find("需要回答")
	question, _ := ui.Find(v.Model.Details["s"].Requests[0].Questions[1].Text)
	ui.Advance(350 * time.Millisecond)
	after, _ := ui.Find("需要回答")
	settled, _ := ui.Find(v.Model.Details["s"].Requests[0].Questions[1].Text)
	if !(before.Y > middle.Y && middle.Y > after.Y) {
		t.Fatal("page height jumps", before, middle, after)
	}
	if question.X <= settled.X+1 {
		t.Fatal("next page has no horizontal entrance", question, settled)
	}
	heightProgress := (before.Y - middle.Y) / (before.Y - after.Y)
	body, _ := ui.Find("请求内容区域")
	contentProgress := 1 - (question.X-settled.X)/body.W
	if math.Abs(float64(heightProgress-contentProgress)) > .06 {
		t.Fatal("page height and horizontal motion use different progress", heightProgress, contentProgress)
	}
	if got, _ := ui.Find("消息编辑器"); got != composer || v.Model.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("paging moved the composer or changed its draft")
	}
	ui.Click("上一个请求")
	ui.Advance(70 * time.Millisecond)
	previous, _ := ui.Find("简短问题")
	ui.Advance(350 * time.Millisecond)
	previousSettled, _ := ui.Find("简短问题")
	if previous.W >= previousSettled.W-1 {
		t.Fatal("previous page does not enter from the left", previous, previousSettled)
	}
}

func TestRequestDisclosureKeepsContentAttachedToItsHeading(t *testing.T) {
	v := requestMotionModel()
	v.Model.Collapsed["s"] = true
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("需要回答")
	ui.Click("需要回答")
	ui.Advance(70 * time.Millisecond)
	middle, _ := ui.Find("需要回答")
	ui.Advance(300 * time.Millisecond)
	after, _ := ui.Find("需要回答")
	if !(before.Y > middle.Y && middle.Y > after.Y) {
		t.Fatal("disclosure height jumps ahead of its contents", before, middle, after)
	}
	ui.Click("需要回答")
	ui.Advance(300 * time.Millisecond)
	ui.Click("需要回答")
	for i := 0; i < 9; i++ {
		ui.Advance(16 * time.Millisecond)
		heading, _ := ui.Find("需要回答")
		question, ok := ui.Find("简短问题")
		body, _ := ui.Find("请求内容区域")
		card, _ := ui.Find("待处理请求卡片")
		padding := heading.Y - card.Y - 1
		if math.Abs(float64(card.H-(26+2*padding+body.H))) > 1 {
			t.Fatal("disclosure surface does not follow the actual content and padding height", heading, body, card)
		}
		if ok && question.H > 1 && math.Abs(float64(question.Y-heading.Y-32)) > 1 {
			t.Fatal("content and expanding surface use different positions", i, heading, question)
		}
	}
}

func TestRequestMotionReversesAndHonorsReducedMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		v := requestMotionModel()
		ui := n.NewTester(v.View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		ui.Advance(time.Second)
		ui.Click("需要回答")
		ui.Advance(60 * time.Millisecond)
		ui.Frame()
		before, _ := ui.Find("需要回答")
		ui.Click("需要回答")
		ui.Frame()
		reversed, _ := ui.Find("需要回答")
		if !reduced && math.Abs(float64(before.Y-reversed.Y)) > 1 {
			t.Fatal("reversing disclosure jumps the card", before, reversed)
		}
		ui.Advance(300 * time.Millisecond)
		ui.Click("下一个请求")
		ui.Advance(70 * time.Millisecond)
		moving, _ := ui.Find(v.Model.Details["s"].Requests[0].Questions[1].Text)
		ui.Advance(300 * time.Millisecond)
		settled, _ := ui.Find(v.Model.Details["s"].Requests[0].Questions[1].Text)
		if reduced && moving != settled {
			t.Fatal("Reduce Motion still interpolates request geometry", moving, settled)
		}
		if ui.FramePending() {
			t.Fatal("settled request animation keeps rebuilding")
		}
	}
}

func TestConversationToolbarOnlyKeepsTheSidebarEntry(t *testing.T) {
	v := New(parallelModel(), nil, "")
	d := v.Model.Details["s"]
	d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "Task", Status: "pending"}}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	for _, label := range []string{"并排查看会话", "查看任务"} {
		if _, ok := ui.Find(label); ok {
			t.Fatal("redundant toolbar entry remains", label)
		}
	}
	if err := ui.Click("展开更改侧栏"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if _, ok := ui.Find("任务面板"); !ok {
		t.Fatal("sidebar entry does not expose the plan")
	}
}

func TestFirstRequestGrowsItsActualDockHeightWithoutMovingTheComposer(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		v := requestMotionModel()
		d := v.Model.Details["s"]
		requests := d.Requests
		d.Requests = nil
		v.Model.Details["s"] = d
		ui := n.NewTester(v.View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		ui.Advance(time.Second)
		ui.Click("消息输入")
		before, _ := ui.Find("会话底部面板")
		composer, _ := ui.Find("消息编辑器")
		d.Requests = requests
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		middle, _ := ui.Find("会话底部面板")
		ui.Advance(time.Second)
		after, _ := ui.Find("会话底部面板")
		if !reduced && !(before.H < middle.H && middle.H < after.H) {
			t.Fatal("first request does not grow its actual dock height", before, middle, after)
		}
		if reduced && middle != after {
			t.Fatal("reduced motion still animates arrival", middle, after)
		}
		if got, _ := ui.Find("消息编辑器"); got != composer || !ui.Focused("消息输入") {
			t.Fatal("question arrival moves or steals focus from the composer", composer, got)
		}
		if ui.HasText("新增 2 个问题") {
			t.Fatal("first question redundantly shows an append notice")
		}
		if ui.FramePending() {
			t.Fatal("settled arrival keeps rebuilding")
		}
	}
}

func TestNewQuestionNoticeTracksUnseenPagesAcrossRemovalAndNavigation(t *testing.T) {
	v := requestMotionModel()
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("下一个请求")
	ui.Advance(time.Second)
	d := v.Model.Details["s"]
	d.Requests = append(d.Requests, domain.PendingRequest{ID: "new", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "a", Text: "新增甲", Custom: true}, {ID: "b", Text: "新增乙", Custom: true}}})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("新增 2 个问题") || v.Model.Pager["s"] != 1 {
		t.Fatal("notice fails to count newly appended pages")
	}
	ui.Click("查看新增问题")
	ui.Advance(time.Second)
	if v.Model.Pager["s"] != 2 || !ui.HasText("新增甲") || !ui.HasText("新增 1 个问题") {
		t.Fatal("viewing one added page cleared the other unseen question")
	}
	// Resolving a different request shifts indices, but not the current page.
	d.Requests[0].State = "answered"
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if v.Model.Pager["s"] != 0 || !ui.HasText("新增甲") {
		t.Fatal("resolution of another request replaced the current question")
	}
	ui.Click("查看新增问题")
	ui.Advance(time.Second)
	if v.Model.Pager["s"] != 1 || !ui.HasText("新增乙") || ui.HasText("新增 1 个问题") {
		t.Fatal("viewed notices remain visible")
	}
}

func TestRequestArrivalNoticeStaysInTheHeaderWithoutMovingTheCurrentQuestion(t *testing.T) {
	for _, collapsed := range []bool{false, true} {
		v := requestMotionModel()
		v.Model.Collapsed["s"] = collapsed
		ui := n.NewTester(v.View, 1440, 900)
		ui.Advance(time.Second)
		before, _ := ui.Find("需要回答")
		d := v.Model.Details["s"]
		d.Requests = append(d.Requests, domain.PendingRequest{ID: "new", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "a", Text: "追加的问题", Custom: true}}})
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		middle, _ := ui.Find("需要回答")
		moving, _ := ui.Find("查看新增问题")
		ui.Advance(time.Second)
		after, _ := ui.Find("需要回答")
		settled, _ := ui.Find("查看新增问题")
		if before.Y != middle.Y || middle.Y != after.Y || moving.Y != settled.Y || !(moving.W > 0 && moving.W < settled.W) {
			t.Fatal("new question notice moves the question instead of entering the title bar", collapsed, before, middle, after, moving, settled)
		}
	}
}

func TestRequestPresenceReversesWithoutResettingOrStealingFocus(t *testing.T) {
	v := requestMotionModel()
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("消息输入")
	d := v.Model.Details["s"]
	d.Requests[0].State = "answered"
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	ui.Frame()
	before, _ := ui.Find("会话底部面板")
	d.Requests[0].State = "pending"
	v.Model.Details["s"] = d
	ui.Frame()
	reversed, _ := ui.Find("会话底部面板")
	if math.Abs(float64(before.H-reversed.H)) > 1 {
		t.Fatal("presence reversal resets the request height", before, reversed)
	}
	ui.Advance(time.Second)
	if !ui.Focused("消息输入") || v.Model.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("presence reversal changed focus or draft")
	}
	if ui.FramePending() {
		t.Fatal("reversed presence never settles")
	}
}

func TestRequestCardMovesWithoutFadingInEitherDirection(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v := requestMotionModel()
			ui := n.NewTester(v.View, 1440, 900)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			heading, _ := ui.Find("需要回答")
			surface := ui.Image().RGBAAt(int(heading.X-8), int(heading.Y+8))
			d := v.Model.Details["s"]
			for _, active := range []bool{false, true} {
				d.Requests[0].State = "answered"
				if active {
					d.Requests[0].State = "pending"
				}
				v.Model.Details["s"] = d
				ui.Frame()
				ui.Advance(70 * time.Millisecond)
				dock, _ := ui.Find("会话底部面板")
				got := ui.Image().RGBAAt(int(heading.X-8), int(dock.Y+20))
				if got != surface {
					t.Fatal("request surface fades during movement", active, surface, got)
				}
				ui.Advance(time.Second)
			}
		})
	}
}

func TestRequestArrivalNoticeSlidesOutAfterFourSeconds(t *testing.T) {
	for _, collapsed := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			v := requestMotionModel()
			v.Model.Collapsed["s"] = collapsed
			ui := n.NewTester(v.View, 1440, 900)
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)
			ui.Click("消息输入")
			d := v.Model.Details["s"]
			d.Requests = append(d.Requests, domain.PendingRequest{ID: "new", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "新问题", Custom: true}}})
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(3 * time.Second)
			shown, ok := ui.Find("查看新增问题")
			if !ok || !ui.HasText("新增 1 个问题") {
				t.Fatal("notice disappears before its reading interval")
			}
			heading, _ := ui.Find("需要回答")
			ui.Advance(1070 * time.Millisecond)
			if !reduced {
				moving, visible := ui.Find("查看新增问题")
				if !visible || moving.X <= shown.X || moving.W >= shown.W || moving.W <= 0 {
					t.Fatal("notice does not slide out to the right", shown, moving)
				}
			}
			ui.Advance(400 * time.Millisecond)
			if _, visible := ui.Find("查看新增问题"); visible || ui.HasText("新增 1 个问题") {
				t.Fatal("notice does not leave after the interval", collapsed, reduced, shown)
			}
			if got, _ := ui.Find("需要回答"); got.Y != heading.Y || v.Model.Pager["s"] != 0 || v.Model.Collapsed["s"] != collapsed || !ui.Focused("消息输入") {
				t.Fatal("timed notice changed the question or composer focus")
			}
			if v.Model.Details["s"].Requests[1].State != "pending" {
				t.Fatal("notice timeout resolved the pending question")
			}
			if ui.FramePending() {
				t.Fatal("hidden notice keeps requesting frames")
			}
		}
	}
}

func TestAnotherQuestionRestartsTheNoticeInterval(t *testing.T) {
	v := requestMotionModel()
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	d := v.Model.Details["s"]
	appendRequest := func(id string) {
		d.Requests = append(d.Requests, domain.PendingRequest{ID: id, SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "新问题", Custom: true}}})
		v.Model.Details["s"] = d
		ui.Frame()
	}
	appendRequest("first")
	ui.Advance(3500 * time.Millisecond)
	appendRequest("second")
	ui.Advance(3500 * time.Millisecond)
	if !ui.HasText("新增 2 个问题") {
		t.Fatal("second arrival did not extend the notice interval")
	}
	ui.Advance(time.Second)
	if ui.HasText("新增 2 个问题") {
		t.Fatal("extended notice never exits")
	}
	appendRequest("third")
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("新增 1 个问题") {
		t.Fatal("new arrival does not restart the hidden notice count from one")
	}
}

func TestCollapsedRequestsShowTotalInACompactCenteredHeading(t *testing.T) {
	v := requestMotionModel()
	v.Model.Pager["s"] = 1
	v.Model.Collapsed["s"] = true
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if !ui.HasText("2 个问题") || ui.HasText("2 / 2") {
		t.Fatal("collapsed header does not summarize the total")
	}
	for _, label := range []string{"上一个请求", "下一个请求"} {
		if _, ok := ui.Find(label); ok {
			t.Fatal("collapsed header still exposes paging", label)
		}
	}
	card, ok := ui.Find("待处理请求卡片")
	heading, _ := ui.Find("需要回答")
	if !ok || card.H != 34 || math.Abs(float64((heading.Y-card.Y)-(card.Y+card.H-heading.Y-heading.H))) > .5 {
		t.Fatal("collapsed card is too tall or has unequal vertical insets", card, heading)
	}
	if heading.W > 220 {
		t.Fatal("request heading has an oversized hover and click target", heading)
	}
	ui.ClickAt(card.X+card.W/2, heading.Y+heading.H/2)
	ui.Advance(300 * time.Millisecond)
	if !v.Model.Collapsed["s"] {
		t.Fatal("blank header space toggles the card")
	}
	ui.Click("需要回答")
	ui.Advance(300 * time.Millisecond)
	if v.Model.Collapsed["s"] || v.Model.Pager["s"] != 1 || !ui.HasText("2 / 2") {
		t.Fatal("expanding does not restore the retained page")
	}
}

func TestExpandedRequestHeadingAlsoKeepsItsHitTargetCompact(t *testing.T) {
	v := requestMotionModel()
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	heading, _ := ui.Find("需要回答")
	if heading.W > 160 {
		t.Fatal("expanded heading includes the blank toolbar width", heading)
	}
	ui.ClickAt(heading.X+400, heading.Y+heading.H/2)
	ui.Advance(300 * time.Millisecond)
	if v.Model.Collapsed["s"] {
		t.Fatal("blank toolbar space collapses the request")
	}
}

func TestCollapsedTotalMovesIntoTheExpandedDenominator(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		v := requestMotionModel()
		v.Model.Collapsed["s"] = true
		v.Model.Pager["s"] = 1
		ui := n.NewTester(v.View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		ui.Advance(time.Second)
		card, _ := ui.Find("待处理请求卡片")
		counter, ok := ui.Find("2 个问题")
		closed, numberOK := ui.Find("待处理总数")
		if !ok || !numberOK || math.Abs(float64(card.X+card.W-counter.X-counter.W-15)) > .5 {
			t.Fatal("collapsed count is not at the right edge", card, counter, closed)
		}
		ui.Click("需要回答")
		ui.Advance(70 * time.Millisecond)
		middle, ok := ui.Find("待处理总数")
		if !ok {
			t.Fatal("total disappears while becoming the denominator")
		}
		ui.Advance(300 * time.Millisecond)
		open, _ := ui.Find("待处理总数")
		if !ui.HasText("2 / 2") {
			t.Fatal("expanded page counter is missing")
		}
		if reduced {
			if middle != open {
				t.Fatal("reduced motion still interpolates the total", middle, open)
			}
		} else if !(middle.X > min(closed.X, open.X) && middle.X < max(closed.X, open.X)) {
			t.Fatal("total jumps instead of moving into the denominator", closed, middle, open)
		}
		ui.Click("需要回答")
		ui.Advance(300 * time.Millisecond)
		returned, _ := ui.Find("待处理总数")
		if returned != closed || !ui.HasText("2 个问题") {
			t.Fatal("collapse does not restore the same total", closed, returned)
		}
	}
}

func TestNewAnswerReceiptActuallyAnimatesInTheVirtualTranscript(t *testing.T) {
	v := requestMotionModel()
	d := v.Model.Details["s"]
	d.Requests = nil
	d.Items = []domain.Item{{ID: "before", Kind: "assistant", Text: "继续输出的说明。", Final: true, Order: 1}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("消息输入")
	d.Items = append(d.Items, domain.Item{ID: "receipt", Kind: "request_result", Text: "已提交回答", Final: true, Order: 2, Parts: []domain.ContentPart{{Label: "选择哪一种？", Text: "逐项检查"}}})
	v.Model.Details["s"] = d
	ui.Frame()
	start, _ := ui.Find("回答结果")
	ui.Advance(70 * time.Millisecond)
	middle, _ := ui.Find("回答结果")
	ui.Advance(300 * time.Millisecond)
	settled, _ := ui.Find("回答结果")
	if !(start.Y > middle.Y && middle.Y > settled.Y) {
		t.Fatal("new receipt has no actual entrance in a virtual list row", start, middle, settled)
	}
	if v.visibleOutputAnimating() || !ui.Focused("消息输入") || v.Model.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("receipt animation does not finish or changes the composer")
	}
}

func TestAnswerReceiptHistoryAndReducedMotionAppearWithoutReplay(t *testing.T) {
	for _, mode := range []string{"history", "reduce", "reconnect", "older-page"} {
		v := requestMotionModel()
		d := v.Model.Details["s"]
		d.Requests = nil
		d.Items = []domain.Item{{ID: "current", Kind: "assistant", Text: "Current message", Final: true, Order: 10}}
		receipt := domain.Item{ID: "receipt", Kind: "request_result", Text: "已提交回答", Final: true, Order: 2, Parts: []domain.ContentPart{{Label: "选择", Text: "已确认"}}}
		if mode == "history" {
			d.Items = append(d.Items, receipt)
		}
		v.Model.Details["s"] = d
		ui := n.NewTester(v.View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: mode == "reduce"})
		ui.Advance(time.Second)
		if mode != "history" {
			if mode == "reduce" {
				receipt.Order = 11
			}
			d.Items = append(d.Items, receipt)
			v.Model.Details["s"] = d
			if mode == "reconnect" {
				v.Model.ConnectionEpoch = "new"
			}
			ui.Frame()
		}
		before, _ := ui.Find("回答结果")
		ui.Advance(300 * time.Millisecond)
		after, _ := ui.Find("回答结果")
		if before != after || v.visibleOutputAnimating() {
			t.Fatal("existing/reduced receipt replays its entrance", mode, before, after)
		}
	}
}

func TestFollowingReplyWaitsForReceiptEntrance(t *testing.T) {
	for _, arrival := range []string{"together", "during-entrance"} {
		t.Run(arrival, func(t *testing.T) {
			v := requestMotionModel()
			d := v.Model.Details["s"]
			d.Requests, d.Items = nil, nil
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1440, 900)
			ui.Advance(time.Second)
			ui.Click("消息输入")
			d.Items = []domain.Item{{ID: "receipt", Kind: "request_result", Text: "已提交回答", Final: true, Order: 1, Parts: []domain.ContentPart{{Label: "选择哪一种？", Text: "逐项检查"}}}}
			if arrival == "together" {
				d.Items = append(d.Items, domain.Item{ID: "after", Kind: "assistant", Text: "后续正文第一段，", Order: 2})
			}
			v.Model.Details["s"] = d
			ui.Frame()
			start, _ := ui.Find("回答结果")
			ui.Advance(50 * time.Millisecond)
			if arrival == "during-entrance" {
				d.Items = append(d.Items, domain.Item{ID: "after", Kind: "assistant", Text: "后续正文第一段，", Order: 2})
			}
			d.Items[1].Text += "第二段继续抵达。"
			d.Items[1].Final = true
			v.Model.Details["s"] = d
			ui.Frame()
			for i := 0; i < 7; i++ {
				ui.Advance(time.Second / 60)
				for _, text := range ui.Texts() {
					if strings.HasPrefix(text, "后") {
						t.Fatalf("following prose reveals before the receipt settles: %q", text)
					}
				}
			}
			middle, _ := ui.Find("回答结果")
			ui.Advance(100 * time.Millisecond)
			settled, _ := ui.Find("回答结果")
			if !(start.Y > middle.Y && middle.Y > settled.Y) {
				t.Fatal("test did not cover the receipt's actual moving interval", start, middle, settled)
			}
			if !ui.HasText("后") || ui.HasText("第二段继续抵达。") {
				t.Fatal("following reply does not begin progressively after the receipt", ui.Texts())
			}
			ui.Advance(time.Second)
			if !ui.HasText("后续正文第一段，第二段继续抵达。") || v.visibleOutputAnimating() {
				t.Fatal("waiting loses received chunks or leaves a reveal pending", ui.Texts())
			}
			if !ui.Focused("消息输入") || v.Model.Drafts["session:s"] != "ordinary draft" {
				t.Fatal("receipt/reply handoff changes the composer")
			}
		})
	}
}

func TestReceiptHandoffDoesNotDelayHistoryOrReducedMotion(t *testing.T) {
	for _, mode := range []string{"history", "reconnect", "reduce"} {
		t.Run(mode, func(t *testing.T) {
			v := requestMotionModel()
			d := v.Model.Details["s"]
			d.Requests, d.Items = nil, nil
			items := []domain.Item{
				{ID: "receipt", Kind: "request_result", Text: "已提交回答", Final: true, Order: 1, Parts: []domain.ContentPart{{Text: "逐项检查"}}},
				{ID: "after", Kind: "assistant", Text: "已确认的完整正文。", Final: true, Order: 2},
			}
			if mode == "history" {
				d.Items = items
			}
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1440, 900)
			if mode == "reduce" {
				ui.SetPreferences(n.Preferences{ReduceMotion: true})
			}
			if mode != "history" {
				d.Items = items
				v.Model.Details["s"] = d
				if mode == "reconnect" {
					v.Model.ConnectionEpoch = "reconnected"
				}
				ui.Frame()
			}
			if !ui.HasText("已确认的完整正文。") || v.visibleOutputAnimating() {
				t.Fatal("history/reduced motion waits for a receipt or replays prose", ui.Texts())
			}
		})
	}
}

func TestReplyArrivingAfterReceiptSettlesStartsImmediately(t *testing.T) {
	v := requestMotionModel()
	d := v.Model.Details["s"]
	d.Requests, d.Items = nil, nil
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	d.Items = []domain.Item{{ID: "receipt", Kind: "request_result", Text: "已提交回答", Final: true, Order: 1, Parts: []domain.ContentPart{{Text: "逐项检查"}}}}
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	d.Items = append(d.Items, domain.Item{ID: "after", Kind: "assistant", Text: "后续正文继续抵达。", Final: true, Order: 2})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(40 * time.Millisecond)
	if !ui.HasText("后") {
		t.Fatal("late prose waits again after the receipt is already settled", ui.Texts())
	}
}
