package ui

import (
	"encoding/json"
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

func TestPendingSendReferenceSnapshotSurvivesEditorHandoff(t *testing.T) {
	file := domain.Attachment{ID: "file", Name: "tree.go", SourcePath: "/project/tree.go", MIME: "text/plain", Size: 20, SHA256: strings.Repeat("a", 64)}
	v, ui, in := pendingSendRow(t, func(m *app.Model) {
		m.WorkerMinor = 12
		m.Attachments["session:s"] = []app.DraftAttachment{{Attachment: file, InlinePosition: 1, Ready: true, WorkspaceID: "w"}}
	})
	if got := v.composerDocuments["session:s"].doc.Text().String(); got != "" {
		t.Fatalf("departing inline reference remains in editor: %q", got)
	}
	v.Model.Reject(in, "not sent")
	ui.Frame()
	if got := v.composerDocuments["session:s"].doc.Text().String(); !strings.Contains(got, "\uFFFC") || !strings.Contains(got, in.Text) {
		t.Fatalf("rejection lost the inline reference or original text: %q", got)
	}
	if len(v.Model.Attachments["session:s"]) != 1 || v.Model.Attachments["session:s"][0].Attachment != file {
		t.Fatal("editor handoff changed the immutable reference snapshot")
	}
}
