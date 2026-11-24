package ui

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestAntigravityHistoryQuotaUsesComposerNotice(t *testing.T) {
	const warning = "⚠ Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 2h22m39s."
	const title = "使用额度已用尽"
	v, tester := readySendFixture(t, false, func(m *app.Model) {
		m.Drafts[m.DraftKey()] = "还未发送的草稿"
	})
	m := v.Model
	session := &m.Snapshot.Sessions[0]
	session.HarnessID = "antigravity"
	detail := m.Details[session.ID]
	detail.Session = *session
	before, _ := tester.Find("消息编辑区")
	editor, _ := tester.Find("消息输入")
	completed := tester.Now().Add(-2*time.Hour - 22*time.Minute - 36*time.Second)
	detail.Turns = []domain.Turn{{ID: "quota", Order: 1, Status: domain.ExecutionCompleted, CompletedAt: &completed}}
	detail.Items = []domain.Item{{ID: "reply", Kind: "assistant", TurnID: "quota", Order: 1, Final: true, Text: "已保留的回复正文。\n" + warning + "\n"}}
	m.Details[session.ID] = detail
	tester.Frame()
	tester.Advance(100 * time.Millisecond)
	opening, _ := tester.Find("消息编辑区")
	tester.Advance(300 * time.Millisecond)
	opened, _ := tester.Find("消息编辑区")
	saveNativeSkillImage(t, tester, "antigravity-history-quota.png")
	if !tester.HasText("已保留的回复正文。") || !tester.HasText(title) || !tester.HasText("预计 00:00:03 后重置") || tester.HasText(warning) {
		t.Fatal("the stored Antigravity warning did not become a quota card with its original reply and remaining time")
	}
	card, _ := tester.Find(title)
	retry, found := tester.Find("重试")
	atOpen, _ := tester.Find("消息输入")
	if !found || retry.X <= card.X+card.W || card.Y < opened.Y || card.Y+card.H > atOpen.Y ||
		!(before.H < opening.H && opening.H < opened.H) || math.Abs(float64(editor.Y-atOpen.Y)) > 1 {
		t.Fatalf("notice did not expand above the anchored editor with retry to the right: %v %v %v %v %v", card, retry, before, opening, opened)
	}
	tester.Advance(time.Second)
	if !tester.HasText("预计 00:00:02 后重置") {
		t.Fatal("the quota countdown did not tick without a model update")
	}
	tester.Advance(2 * time.Second)
	if !tester.HasText("预计重置时间已到") || tester.HasText("预计 00:00:00 后重置") {
		t.Fatal("the history warning did not settle at its estimated reset time")
	}
	tester.SetDark(true)
	tester.Frame()
	saveNativeSkillImage(t, tester, "antigravity-history-quota-dark.png")
	retry, _ = tester.Find("重试")
	if err := tester.Click("重试"); err != nil {
		t.Fatal(err)
	}
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
	if !ok || in.SessionID != session.ID || in.Text != "继续" {
		t.Fatalf("retry did not use the ordinary notice retry path: %+v", job.Input)
	}
	if v.sent == nil ||
		math.Abs(float64(v.sent.source.X+v.sent.source.W/2-retry.X-retry.W/2)) > 1 ||
		math.Abs(float64(v.sent.source.Y+v.sent.source.H/2-retry.Y-retry.H/2)) > 1 {
		t.Fatalf("retry send did not start from the card button at %v", retry)
	}
	ack, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: in.SessionID, Kind: "submit", State: domain.OperationConfirmed})
	job.Apply(m, ack, nil)
	detail.Turns = append(detail.Turns, domain.Turn{ID: "retry", Order: 2, Status: domain.ExecutionRunning})
	detail.Session.Status = domain.ExecutionRunning
	m.Details[session.ID] = detail
	tester.Frame()
	tester.Advance(100 * time.Millisecond)
	closing, _ := tester.Find("消息编辑区")
	tester.Advance(300 * time.Millisecond)
	closed, _ := tester.Find("消息编辑区")
	if tester.HasText(title) || !(closed.H < closing.H && closing.H < opened.H) {
		t.Fatalf("notice did not collapse when the next turn started: %v %v %v", opened, closing, closed)
	}
	if m.Drafts[m.DraftKey()] != "还未发送的草稿" {
		t.Fatal("retry consumed the ordinary composer draft")
	}
}
