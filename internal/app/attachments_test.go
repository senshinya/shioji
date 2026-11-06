package app

import (
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestImageOnlyDraftIsAcceptedAndRetainedUntilConfirmation(t *testing.T) {
	m := NewModel()
	m.Connected = true
	m.Readiness.State = "ready"
	m.WorkerMinor = 6
	m.WorkspaceID = "w"
	m.Snapshot.Workspaces = []domain.Workspace{{ID: "w"}}
	ref := domain.Attachment{ID: domain.NewID(), Name: "screenshot.png", MIME: "image/png", Size: 20, SHA256: strings.Repeat("a", 64)}
	m.Attachments["home"] = []DraftAttachment{{Attachment: ref, DeviceID: m.KnownDeviceID, WorkspaceID: "w", Ready: true}}
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Attachments) != 1 || len(m.Attachments["home"]) != 1 {
		t.Fatal("attachment lost before confirmation")
	}
	m.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	if len(m.Attachments["home"]) != 0 {
		t.Fatal("confirmed attachment not cleared")
	}
}

func TestQueueEditPreservesNewAttachmentsAddedDuringSave(t *testing.T) {
	m := NewModel()
	m.SessionID = "s"
	m.QueueEdits["s"] = QueueEdit{ID: "q", Displaced: "ordinary"}
	m.Drafts["session:s"] = "edited"
	a := domain.Attachment{ID: "new"}
	m.Attachments["session:s"] = []DraftAttachment{{Attachment: a}}
	m.FinishQueueEdit("s", "q", "edited", domain.QueueState{SessionID: "s", Revision: 2}, nil)
	if m.QueueEdits["s"].ID == "" || m.Drafts["session:s"] != "edited" || len(m.Attachments["session:s"]) != 1 {
		t.Fatal("in-flight save discarded newer edit")
	}
}

func TestHomeAttachmentCannotUseAnotherWorkspaceUpload(t *testing.T) {
	m := NewModel()
	m.Connected = true
	m.Readiness.State = "ready"
	m.WorkerMinor = 6
	m.WorkspaceID = "b"
	m.Snapshot.Workspaces = []domain.Workspace{{ID: "b"}}
	m.Drafts["home"] = "text"
	m.Attachments["home"] = []DraftAttachment{{Attachment: domain.Attachment{ID: "a"}, WorkspaceID: "a", Ready: true}}
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("foreign workspace resource submitted")
	}
	if m.Drafts["home"] != "text" {
		t.Fatal("blocked send erased draft")
	}
}
