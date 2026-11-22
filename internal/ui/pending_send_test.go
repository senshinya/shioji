package ui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func pendingSendRow(t *testing.T, configure ...func(*app.Model)) (*View, *n.Tester, domain.Submission) {
	t.Helper()
	v, ui, job := sendFixture(t, false, configure...)
	in := job.Input.(domain.Submission)
	body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationAccepted})
	job.Apply(v.Model, body, nil)
	d := v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Attachments: in.Attachments, Skills: in.Skills, ReferencePositions: in.ReferencePositions, Status: "dispatching", Final: true})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	return v, ui, in
}

func TestExistingSendAnimatesBeforeWorkerReceipt(t *testing.T) {
	v, ui, job := sendFixture(t, false)
	in := job.Input.(domain.Submission)
	ui.Advance(100 * time.Millisecond)
	if !ui.HasText(in.Text) || !ui.HasText("正在发送") {
		t.Fatal("existing conversation waits for the worker receipt before presenting the send")
	}
	if got := v.composerDocuments["session:s"].doc.Text().String(); got != "" {
		t.Fatalf("outgoing message remains in editor until receipt: %q", got)
	}
	if v.sent == nil || v.sent.phase != sentAnimating {
		t.Fatal("send motion has not started before receipt")
	}
	if v.Model.Drafts["session:s"] != in.Text || len(v.Model.Pending) != 1 {
		t.Fatal("local presentation discarded durable unconfirmed input")
	}
	for _, item := range v.Model.Details["s"].Items {
		if item.ID == "input:"+in.OperationID {
			t.Fatal("local presentation fabricated authoritative history")
		}
	}
	ui.Advance(2 * time.Second)
	if !ui.HasText(in.Text) {
		t.Fatal("local message disappeared while receipt was delayed")
	}
	v.Model.Reject(in, "not sent")
	ui.Frame()
	if got := v.composerDocuments["session:s"].doc.Text().String(); got != in.Text {
		t.Fatalf("rejection did not restore original input: %q", got)
	}
}

func TestExistingSendWaitingStatusFollowsNewMessage(t *testing.T) {
	v, ui, job := sendFixture(t, false, func(m *app.Model) {
		d := m.Details["s"]
		d.Items[0].TurnID = "earlier-turn"
		d.Turns = []domain.Turn{{ID: "earlier-turn", Status: domain.ExecutionCompleted}}
		m.Details["s"] = d
	})
	ui.Advance(time.Second)
	if !ui.HasText("正在发送") {
		t.Fatal("the previous turn footer swallowed the new send status")
	}
	input := job.Input.(domain.Submission)
	rows := v.transcriptRows(v.homeSendDetail(v.Model.Details["s"]).Items)
	rows = withTurnFooters(rows, map[string]domain.Turn{"earlier-turn": {ID: "earlier-turn", Status: domain.ExecutionCompleted}})
	rows = v.homeSendRows(rows)
	if len(rows) < 2 || rows[len(rows)-2].key != "input:"+input.OperationID || rows[len(rows)-1].preparing != "正在发送" {
		t.Fatal("waiting status is not directly after its submitted input")
	}
	if v.toolArrivals.footerKeys["earlier-turn"] != "" {
		t.Fatal("new submission took over the previous turn footer identity")
	}
}

func TestExistingSendKeepsNextDraftBeforeAnyReceipt(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "rejected"}[rejected], func(t *testing.T) {
			v, ui, job := sendFixture(t, false)
			ui.Click("消息输入")
			ui.Type("Next independent draft")
			ui.Key(0, n.KeyEnter)
			if len(v.Model.Pending) != 1 {
				t.Fatal("pending send allowed another submission")
			}
			if rejected {
				v.Model.Reject(job.Input.(domain.Submission), "not sent")
			} else {
				acceptSend(t, v, job, true)
			}
			ui.Frame()
			if got := v.Model.Drafts["session:s"]; got != "Next independent draft" || !ui.Focused("消息输入") {
				t.Fatal("receipt replaced or unfocused the independently edited next draft", got)
			}
		})
	}
}

func TestPendingSendLeavesEditorBeforeHarnessConfirmation(t *testing.T) {
	v, ui, in := pendingSendRow(t)
	saveNativeSkillImage(t, ui, "pending-send.png")
	if !ui.HasText(in.Text) {
		t.Fatal("pending message disappeared from conversation")
	}
	if got := v.composerDocuments["session:s"].doc.Text().String(); got != "" {
		t.Fatalf("outgoing text remains in the editor while waiting for confirmation: %q", got)
	}
	if v.Model.Drafts["session:s"] != in.Text || len(v.Model.Pending) != 1 {
		t.Fatal("presentation discarded the durable unconfirmed input")
	}
}

func TestPendingSendKeepsNextDraftIndependent(t *testing.T) {
	v, ui, in := pendingSendRow(t)
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("Next draft")
	ui.Frame()
	if got := v.Model.Drafts["session:s"]; got != "Next draft" {
		t.Fatalf("next draft includes the departing message: %q", got)
	}
	v.Model.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationConfirmed})
	ui.Frame()
	if v.Model.Drafts["session:s"] != "Next draft" || !ui.Focused("消息输入") {
		t.Fatal("confirmation replaced the next draft or its focus")
	}
}

func TestPendingSendRejectionRestoresUntouchedEditor(t *testing.T) {
	v, ui, in := pendingSendRow(t)
	v.Model.Reject(in, "not sent")
	ui.Frame()
	ui.Advance(time.Second)
	if got := v.composerDocuments["session:s"].doc.Text().String(); got != in.Text {
		t.Fatalf("rejection failed to restore the original draft: %q", got)
	}
}

func TestPendingSendKeepsNewAttachmentVisible(t *testing.T) {
	v, ui, _ := pendingSendRow(t)
	a := domain.Attachment{ID: "next-image", Name: "next.png", MIME: "image/png", Size: 20, SHA256: strings.Repeat("a", 64)}
	v.Model.Attachments["session:s"] = []app.DraftAttachment{{Attachment: a, Ready: true, WorkspaceID: "w"}}
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("next.png") {
		t.Fatal("next attachment is hidden by the pending send")
	}
	ui.Click("消息输入")
	ui.Type("Next draft")
	ui.Frame()
	if files := v.Model.Attachments["session:s"]; len(files) != 1 || files[0].Attachment != a {
		t.Fatal("typing the next draft discarded its attachment", files)
	}
}

func TestPendingSendRejectionKeepsNewAttachmentOnly(t *testing.T) {
	original := domain.Attachment{ID: "first-image", Name: "first.png", MIME: "image/png", Size: 20, SHA256: strings.Repeat("a", 64)}
	v, ui, in := pendingSendRow(t, func(m *app.Model) {
		m.WorkerMinor = 12
		m.Attachments["session:s"] = []app.DraftAttachment{{Attachment: original, Ready: true, WorkspaceID: "w", DeviceID: m.Snapshot.Workspaces[0].DeviceID}}
	})
	next := domain.Attachment{ID: "next-image", Name: "next.png", MIME: "image/png", Size: 20, SHA256: strings.Repeat("b", 64)}
	v.appendAttachments("session:s", []app.DraftAttachment{{Attachment: next}})
	ui.Frame()
	v.Model.Reject(in, "not sent")
	ui.Frame()
	if text := v.composerDocuments["session:s"].doc.Text().String(); text != "" {
		t.Fatalf("rejection put old text in the attachment-only draft: %q", text)
	}
	if files := v.Model.Attachments["session:s"]; len(files) != 1 || files[0].Attachment != next {
		t.Fatalf("rejection changed the new draft's attachments: %v", files)
	}
}

func TestPendingSendReferenceSnapshotSurvivesEditorHandoff(t *testing.T) {
	file := domain.Attachment{ID: "file", Name: "tree.go", SourcePath: "/project/tree.go", MIME: "text/plain", Size: 20, SHA256: strings.Repeat("a", 64)}
	image := domain.Attachment{ID: "image", Name: "tree.png", MIME: "image/png", Size: 20, SHA256: strings.Repeat("b", 64)}
	v, ui, in := pendingSendRow(t, func(m *app.Model) {
		m.WorkerMinor = 12
		m.Attachments["session:s"] = []app.DraftAttachment{
			{Attachment: file, InlinePosition: 1, Ready: true, WorkspaceID: "w"},
			{Attachment: image, Ready: true, WorkspaceID: "w"},
		}
	})
	if got := v.composerDocuments["session:s"].doc.Text().String(); got != "" {
		t.Fatalf("departing inline reference remains in editor: %q", got)
	}
	feedbackClick(t, ui, "消息输入")
	ui.Type("temporary")
	ui.Key(n.Cmd, n.KeyA)
	ui.Key(0, n.KeyBackspace)
	if m := v.Model; m.HasDraftContent("session:s") {
		t.Fatal("test did not clear the stored draft while the send was pending")
	}
	v.Model.Reject(in, "not sent")
	ui.Frame()
	if got := v.composerDocuments["session:s"].doc.Text().String(); !strings.Contains(got, "\uFFFC") || !strings.Contains(got, in.Text) {
		t.Fatalf("rejection lost the inline reference or original text: %q", got)
	}
	files := v.Model.Attachments["session:s"]
	if len(files) != 2 || !slices.ContainsFunc(files, func(a app.DraftAttachment) bool { return a.Attachment == file && a.InlinePosition == 1 }) ||
		!slices.ContainsFunc(files, func(a app.DraftAttachment) bool { return a.Attachment == image }) {
		t.Fatal("rejection did not restore the attachments and original reference position", files)
	}
}
