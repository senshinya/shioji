package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestHomeSendAnimatesBeforeWorkerReceipt(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	start, _ := ui.Find("消息编辑器")
	ui.Advance(100 * time.Millisecond)
	mid, _ := ui.Find("消息编辑器")
	if !ui.HasText("Explain a tree rotation.") || !ui.HasText("正在发送") {
		t.Fatal("click leaves the page waiting for remote initialization", ui.Texts())
	}
	if mid.Y <= start.Y+5 {
		t.Fatal("composer waits for the receipt before descending", start, mid)
	}
	in := job.Input.(domain.Submission)
	if v.Model.SessionID != "" || v.Model.Drafts["home"] != in.Text {
		t.Fatal("preview fabricated a session or discarded the authoritative draft")
	}
	ui.Advance(2 * time.Second)
	if !ui.HasText(in.Text) {
		t.Fatal("slow initialization outlived and removed the local input")
	}
	if len(v.Model.Snapshot.Sessions) != 1 {
		t.Fatal("presentation inserted a fabricated runtime session")
	}
}

func TestHomeSendUsesClickTimeUntilAuthoritativeMessageArrives(t *testing.T) {
	before := time.Now()
	v, ui, job := sendFixture(t, true)
	after := time.Now()
	in := job.Input.(domain.Submission)
	items := v.homeSendDetail(domain.SessionDetail{}).Items
	if len(items) != 1 || items[0].SentAt == nil {
		t.Fatal("pending first message has no send time")
	}
	sent := *items[0].SentAt
	if sent.Before(before) || sent.After(after) {
		t.Fatal("pending timestamp is not the time of submission", sent, before, after)
	}
	ui.Advance(2 * time.Minute)
	if got := v.homeSendDetail(domain.SessionDetail{}).Items[0].SentAt; got == nil || !got.Equal(sent) {
		t.Fatal("pending timestamp changed while waiting", got, sent)
	}
	if !ui.HasText("发送时间：" + sent.Local().Format("15:04")) {
		t.Fatal("pending timestamp is not displayed", ui.Texts())
	}
	id := acceptSend(t, v, job, false)
	confirmed := time.Date(2026, 11, 16, 11, 23, 0, 0, time.Local)
	v.Model.Details[id] = domain.SessionDetail{Session: domain.Session{ID: id}, Items: []domain.Item{{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Status: "sent", TurnID: "turn", SentAt: &confirmed, Final: true}}}
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("发送时间：11:23") {
		t.Fatal("authoritative send time did not replace click time", ui.Texts())
	}
}

func TestHomeSendReceiptKeepsMessageActionSlots(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v, ui, job := sendFixture(t, true, func(m *app.Model) {
				m.WorkerMinor = 12
				m.Readiness.Capabilities.Restart = true
			})
			ui.SetDark(dark)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("home-send-pending-dark-%v.png", dark))
			edit, ok := ui.Find("修改并重新开始")
			if !ok {
				t.Fatal("pending message hides its edit action")
			}
			copy, _ := ui.Find("复制消息")
			stamp, stampOK := ui.Find("发送时间：" + v.homeSend.sentAt.Local().Format("15:04"))
			if !stampOK {
				t.Fatal("pending send time missing")
			}
			_ = ui.Click("修改并重新开始")
			if len(v.Model.HistoryEdits) != 0 || v.Model.Error != "" {
				t.Fatal("pending edit button is active", v.Model.Error)
			}
			if err := ui.Click("复制消息"); err != nil || ui.Clipboard() != job.Input.(domain.Submission).Text {
				t.Fatal("pending message cannot be copied", err)
			}
			id := acceptSend(t, v, job, false)
			ui.Frame()
			check := func(stage, stampLabel string) {
				t.Helper()
				current, ok := ui.Find("修改并重新开始")
				currentCopy, copyOK := ui.Find("复制消息")
				currentStamp, stampOK := ui.Find(stampLabel)
				if !ok || !copyOK || !stampOK || current != edit || currentCopy != copy || currentStamp != stamp {
					t.Fatal(stage+" moved the message actions", edit, current, copy, currentCopy)
				}
			}
			check("receipt before history", "发送时间："+v.homeSend.sentAt.Local().Format("15:04"))
			in := job.Input.(domain.Submission)
			sent := time.Date(2026, 11, 16, 11, 23, 0, 0, time.Local)
			v.Model.Details[id] = domain.SessionDetail{Session: domain.Session{ID: id}, Items: []domain.Item{{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Status: "sent", TurnID: "turn", SentAt: &sent, Final: true}}}
			for range 30 {
				ui.Advance(time.Second / 60)
				check("authoritative history", "发送时间：11:23")
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("home-send-confirmed-dark-%v.png", dark))
			if err := ui.Click("修改并重新开始"); err != nil || v.Model.HistoryEdits[id].ItemID != "input:"+in.OperationID {
				t.Fatal("confirmed edit button did not become available", err, v.Model.Error)
			}
		})
	}
}

func TestWorkerAcceptanceStartsRealConversationBeforeHarnessConfirmation(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	in := job.Input.(domain.Submission)
	body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "accepted", State: "accepted"})
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: "accepted", WorkspaceID: in.WorkspaceID, Status: domain.ExecutionStarting})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(200 * time.Millisecond)
	if v.Model.SessionID != "accepted" || !ui.HasText(in.Text) || !ui.HasText("正在准备会话") {
		t.Fatal("worker receipt waits for harness confirmation", v.Model.SessionID, ui.Texts())
	}
	if v.Model.Drafts["home"] != in.Text || len(v.Model.Pending) != 1 {
		t.Fatal("worker acceptance prematurely confirmed delivery")
	}
	ui.Click("消息输入")
	ui.Type("next draft")
	ui.Key(0, n.KeyEnter)
	if len(v.Model.Pending) != 1 || v.Model.Error != "" {
		t.Fatal("preparation sent a second message", v.Model.Error)
	}
	// Confirmation must preserve independently edited next-turn content.
	v.Model.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "accepted", State: "confirmed"})
	ui.Frame()
	if v.Model.Drafts["session:accepted"] != "next draft" {
		t.Fatal("confirmation lost the next draft")
	}
}

func TestHomeSendRejectionRestoresDraftAndDoesNotReplay(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	in := job.Input.(domain.Submission)
	ui.Advance(200 * time.Millisecond)
	job.Apply(v.Model, nil, &protocol.RemoteError{Message: "not sent"})
	ui.Frame()
	ui.Advance(time.Second)
	if v.Model.Drafts["home"] != in.Text {
		t.Fatal("failed preview lost original draft")
	}
	for len(v.Controller.Jobs) > 0 {
		if next := <-v.Controller.Jobs; next.Method == "session.submit" {
			t.Fatal("failure replayed input")
		}
	}
}

func TestPendingHomeSendSurvivesReconnectWithoutReplayingInput(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	in := job.Input.(domain.Submission)
	ui.Advance(100 * time.Millisecond)
	v.Model.Connected = false
	v.Model.ConnectionEpoch = ""
	ui.Frame()
	ui.Advance(600 * time.Millisecond)
	if !ui.HasText(in.Text) || !ui.HasText("等待重新连接") {
		t.Fatal("disconnect discarded pending presentation", ui.Texts())
	}
	v.Model.Connected = true
	v.Model.ConnectionEpoch = "reconnected"
	ui.Frame()
	v.submit()
	for len(v.Controller.Jobs) > 0 {
		if next := <-v.Controller.Jobs; next.Method == "session.submit" {
			t.Fatal("reconnect resubmitted unknown input")
		}
	}
	if v.Model.Drafts["home"] != in.Text {
		t.Fatal("reconnect lost the durable draft")
	}
}

func TestLateWorkerReceiptDoesNotRestartCompletedHomeAnimation(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	ui.Advance(time.Second)
	before, _ := ui.Find("用户消息气泡")
	input := job.Input.(domain.Submission)
	ack, _ := json.Marshal(domain.Operation{ID: input.OperationID, SessionID: "late", State: "accepted"})
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: "late", WorkspaceID: input.WorkspaceID, Status: domain.ExecutionStarting})
	job.Apply(v.Model, ack, nil)
	ui.Frame()
	after, _ := ui.Find("用户消息气泡")
	if before != after {
		t.Fatal("late acceptance moved a settled input", before, after)
	}
	v.Model.Details["late"] = domain.SessionDetail{Session: domain.Session{ID: "late"}, Items: []domain.Item{{ID: "input:" + input.OperationID, Kind: "user", Text: input.Text, Status: "dispatching", Final: true}}}
	ui.Frame()
	hydrated, _ := ui.Find("用户消息气泡")
	if hydrated != after {
		t.Fatal("history hydration moved a settled input", after, hydrated)
	}
}

func TestWorkerReceiptBeforeCatalogKeepsPreviewWithoutSelectingUnknownSession(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	in := job.Input.(domain.Submission)
	ack, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "not-cataloged", State: "accepted"})
	job.Apply(v.Model, ack, nil)
	ui.Frame()
	if v.Model.SessionID != "" {
		t.Fatal("receipt selected a session before its owning-device catalog arrived")
	}
	if !ui.HasText("正在准备会话") || !ui.HasText(in.Text) {
		t.Fatal("waiting for catalog removed immediate feedback")
	}
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: "not-cataloged", WorkspaceID: in.WorkspaceID, HarnessID: in.HarnessID, Status: domain.ExecutionStarting})
	ui.Frame()
	if v.Model.SessionID != "not-cataloged" {
		t.Fatal("authoritative catalog did not hand off the preview")
	}
}

func TestPreparingFooterHandsOffAtTheSamePosition(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	in := job.Input.(domain.Submission)
	session := domain.Session{ID: "ready", WorkspaceID: in.WorkspaceID, Status: domain.ExecutionStarting}
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, session)
	ack, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: session.ID, State: domain.OperationAccepted})
	job.Apply(v.Model, ack, nil)
	ui.Frame()
	ui.Advance(time.Second)
	before, ok := ui.Find("正在准备会话")
	if !ok {
		t.Fatal(ui.Texts())
	}
	item := domain.Item{ID: "input:" + in.OperationID, TurnID: "t", Kind: "user", Text: in.Text, Final: true, Status: "sent"}
	session.Status = domain.ExecutionRunning
	v.Model.Details[session.ID] = domain.SessionDetail{Session: session, Items: []domain.Item{item}, Turns: []domain.Turn{{ID: "t", Status: domain.ExecutionRunning}}}
	v.Model.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: session.ID, State: domain.OperationConfirmed})
	ui.Frame()
	after, ok := ui.Find("进行中")
	if !ok {
		t.Fatal("real running state disappeared during handoff", ui.Texts())
	}
	if before.X != after.X || before.Y != after.Y {
		t.Fatal("preparing and running footer do not share an anchor", before, after)
	}
}

func TestAcceptedHomeConversationKeepsManualReadingPosition(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	id := acceptSend(t, v, job, true)
	d := v.Model.Details[id]
	for i := 0; i < 40; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprintf("reply-%d", i), Kind: "assistant", Text: strings.Repeat("Earlier answer. ", 12), Final: true})
	}
	v.Model.Details[id] = d
	ui.Frame()
	ui.Advance(time.Second)
	ui.Scroll(800, 350, 0, -300)
	ui.Advance(300 * time.Millisecond)
	if v.lists[id].AtEnd() {
		t.Fatal("newly accepted conversation discards the reader's scroll state")
	}
	d.Items = append(d.Items, domain.Item{ID: "more", Kind: "assistant", Text: "Later answer.", Final: true})
	v.Model.Details[id] = d
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if v.lists[id].AtEnd() {
		t.Fatal("a new reply forces the reader back to latest")
	}
}
