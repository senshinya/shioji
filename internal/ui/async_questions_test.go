package ui

import (
	"encoding/json"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestAsyncQuestionUsesNativeChoicesAndAnIndependentSubmission(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = protocol.QuestionReplyMinor
	m.Snapshot.DeviceID = "device"
	m.Snapshot.Workspaces[0].DeviceID = m.Snapshot.DeviceID
	m.Readiness.Harnesses = []domain.HarnessInfo{{ID: "codex", State: domain.RuntimeReady, Models: m.Readiness.Models}}
	d := m.Details["s"]
	d.Session.HarnessID = "codex"
	d.Session.ThreadID = "thread"
	m.Snapshot.Sessions[0] = d.Session
	d.Items = []domain.Item{{ID: "ask", Kind: "assistant", Text: "When?\n- After locking\n- While awake", Final: true,
		Questions: []domain.Question{{ID: "question", Text: "When?", Custom: true, Options: []domain.Option{{Label: "After locking"}, {Label: "While awake"}}}}}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "While awake"
	c := app.NewController("")
	ui := n.NewTester(New(m, c, "").View, 980, 790)
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "async-question.png")
	if err := ui.Click("While awake"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("提交答案"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, c, "question.reply")
	reply := job.Input.(protocol.QuestionReplyInput)
	if reply.SessionID != "s" || reply.ItemID != "ask" || reply.QuestionID != "question" || reply.Text != "While awake" || reply.OperationID == "" {
		t.Fatalf("wrong answer submitted: %+v", reply)
	}
	receipt, _ := json.Marshal(domain.Operation{ID: reply.OperationID, SessionID: "s", Kind: "submit", State: "confirmed"})
	job.Apply(m, receipt, nil)
	if m.Drafts["session:s"] != "While awake" { // Identical text still belongs to the separate composer.
		t.Fatal("answer submission cleared the ordinary draft")
	}
	d.Items = append(d.Items, domain.Item{ID: "input:" + reply.OperationID, Kind: "user", Text: reply.Text, ReplyTo: "question", Status: "sent", Final: true})
	m.Details["s"] = d
	ui.Advance(time.Second)
	if !ui.HasText("已回答") || ui.HasText("提交答案") {
		t.Fatal("confirmed answer did not settle the original card")
	}
}
