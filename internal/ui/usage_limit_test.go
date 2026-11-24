package ui

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestRegionalAccessRetryFliesFromButtonAndKeepsDraft(t *testing.T) {
	v, ui := readySendFixture(t, false, func(m *app.Model) {
		s := &m.Snapshot.Sessions[0]
		s.HarnessID = "antigravity"
		detail := m.Details[s.ID]
		detail.Session = *s
		detail.Turns = []domain.Turn{{ID: "regional-error", Order: 1, Status: domain.ExecutionCompleted}}
		detail.Items = []domain.Item{{ID: "region", TurnID: "regional-error", Kind: "assistant", Final: true, Text: "\n⚠ FAILED_PRECONDITION (code 400): User location is not supported for the API use.\n"}}
		m.Details[s.ID] = detail
		m.Drafts[m.DraftKey()] = "继续"
	})
	button, found := ui.Find("重试")
	cardText, _ := ui.Find("当前地区不支持使用此 API")
	if !found || button.X <= cardText.X || button.Y <= 0 {
		t.Fatalf("retry action is missing from the right of the notice: %v, %v", button, cardText)
	}
	saveNativeSkillImage(t, ui, "regional-retry-ready.png")
	if err := ui.Click("重试"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(80 * time.Millisecond)
	first, visible := ui.Find("用户消息气泡")
	saveNativeSkillImage(t, ui, "regional-retry-flight.png")
	var job app.Job
	for len(v.Controller.Jobs) > 0 {
		if next := <-v.Controller.Jobs; next.Method == "session.submit" {
			if job.Method != "" {
				t.Fatal("one retry sent multiple inputs")
			}
			job = next
		}
	}
	in, ok := job.Input.(domain.Submission)
	if !ok || in.Text != "继续" || in.Continue || in.SessionID != "s" {
		t.Fatalf("retry did not submit one ordinary user message: %+v", job.Input)
	}
	motion := v.sent
	if motion == nil || motion.phase != sentAnimating || motion.input != "input:"+in.OperationID ||
		math.Abs(float64(motion.source.X+motion.source.W/2-button.X-button.W/2)) > 1 ||
		math.Abs(float64(motion.source.Y+motion.source.H/2-button.Y-button.H/2)) > 1 || motion.source.W <= button.W {
		t.Fatalf("retry bubble did not begin flying from the button before the worker receipt: %+v", motion)
	}
	if !ui.HasText("正在发送") || v.composerDocuments["session:s"].doc.Text().String() != "继续" {
		t.Fatal("retry hid the original composer draft or skipped pending presentation")
	}
	ui.ClickAt(button.X+button.W/2, button.Y+button.H/2)
	for len(v.Controller.Jobs) > 0 {
		if next := <-v.Controller.Jobs; next.Method == "session.submit" {
			t.Fatal("retry submitted again while the first input awaited confirmation")
		}
	}
	ui.Advance(400 * time.Millisecond)
	landed, settled := ui.Find("用户消息气泡")
	if !visible || !settled || first.Y <= landed.Y+1 {
		t.Fatalf("retry bubble did not visibly travel up into the transcript: first=%v landed=%v", first, landed)
	}
	saveNativeSkillImage(t, ui, "regional-retry-landed.png")
	ack, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: in.SessionID, Kind: "submit", State: domain.OperationConfirmed})
	job.Apply(v.Model, ack, nil)
	ui.Frame()
	if v.Model.Drafts["session:s"] != "继续" {
		t.Fatal("confirmation consumed a draft that happened to match the retry text")
	}
}

func TestRegionalAccessWarningUsesComposerDock(t *testing.T) {
	m := layoutModel()
	s := &m.Snapshot.Sessions[0]
	s.HarnessID = "antigravity"
	detail := m.Details[s.ID]
	detail.Session = *s
	const warning = "⚠ FAILED_PRECONDITION (code 400): User location is not supported for the API use."
	detail.Items[0].Text = "已保留的回复正文。\n" + warning + "\n"
	detail.Items[0].Final = true
	detail.Items[0].TurnID = "regional-error"
	detail.Turns = []domain.Turn{{ID: "regional-error", Order: 1, Status: domain.ExecutionCompleted}}
	m.Details[s.ID] = detail
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1100, 750)
	ui.Advance(400 * time.Millisecond)
	saveNativeSkillImage(t, ui, "regional-access-light.png")
	ui.SetDark(true)
	ui.Advance(400 * time.Millisecond)
	saveNativeSkillImage(t, ui, "regional-access-dark.png")
	for _, label := range []string{"已保留的回复正文。", "当前地区不支持使用此 API", "FAILED_PRECONDITION · HTTP 400"} {
		if !ui.HasText(label) {
			t.Errorf("reply is missing %q", label)
		}
	}
	if ui.HasText(warning) {
		t.Error("raw warning is still rendered by Markdown")
	}
	card, _ := ui.Find("当前地区不支持使用此 API")
	dock, _ := ui.Find("消息编辑区")
	editor, _ := ui.Find("消息输入")
	if card.Y < dock.Y || card.Y+card.H > editor.Y {
		t.Errorf("regional warning must sit inside the dock above the editor: card=%v dock=%v editor=%v", card, dock, editor)
	}

	detail.Turns = append(detail.Turns, domain.Turn{ID: "retry", Order: 2, Status: domain.ExecutionRunning})
	detail.Session.Status, s.Status = domain.ExecutionRunning, domain.ExecutionRunning
	m.Details[s.ID] = detail
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	if ui.HasText("当前地区不支持使用此 API") {
		t.Error("previous turn still shows a regional warning after the next turn started")
	}
}

func TestUsageLimitComposerAnimatesArrivalAndDeparture(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 2
	m.ConnectionEpoch = "connected"
	s := &m.Snapshot.Sessions[0]
	m.Drafts[m.DraftKey()] = "待发送草稿"
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1100, 750)
	ui.Advance(400 * time.Millisecond)
	editor, _ := ui.Find("消息输入")
	before, _ := ui.Find("消息编辑区")

	d := m.Details[s.ID]
	s.Status = domain.ExecutionFailed
	d.Session = *s
	d.Turns = []domain.Turn{{ID: "limit", Status: domain.ExecutionFailed, UsageLimit: &domain.UsageLimit{}}}
	m.Details[s.ID] = d
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	opening, _ := ui.Find("消息编辑区")
	ui.Advance(350 * time.Millisecond)
	opened, _ := ui.Find("消息编辑区")
	if _, ok := ui.Find("重置时间暂未知"); !ok {
		t.Fatal("usage-limit notice is missing from the live composer")
	}
	if !(before.H < opening.H && opening.H < opened.H) {
		t.Fatalf("dock did not expand through intermediate heights: %.1f %.1f %.1f", before.H, opening.H, opened.H)
	}
	atOpen, _ := ui.Find("消息输入")
	if math.Abs(float64(editor.Y-atOpen.Y)) > 1 {
		t.Fatalf("editor moved while the notice opened: %v -> %v", editor, atOpen)
	}
	saveNativeSkillImage(t, ui, "usage-limit-light.png")
	ui.SetDark(true)
	ui.Frame()
	saveNativeSkillImage(t, ui, "usage-limit-dark.png")

	s.Status, d.Session.Status = domain.ExecutionRunning, domain.ExecutionRunning
	d.Turns = append(d.Turns, domain.Turn{ID: "retry", Status: domain.ExecutionRunning})
	m.Details[s.ID] = d
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	closing, _ := ui.Find("消息编辑区")
	ui.Advance(280 * time.Millisecond)
	closed, _ := ui.Find("消息编辑区")
	if _, ok := ui.Find("重置时间暂未知"); ok {
		t.Fatal("notice stayed visible after the next turn started")
	}
	if !(closed.H < closing.H && closing.H < opened.H) {
		t.Fatalf("dock did not collapse through intermediate heights: %.1f %.1f %.1f", opened.H, closing.H, closed.H)
	}
	if m.Drafts[m.DraftKey()] != "待发送草稿" {
		t.Fatal("notice altered the draft")
	}
}
