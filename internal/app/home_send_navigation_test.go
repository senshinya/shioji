package app

import (
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/domain"
)

func TestDetachedFirstSendDoesNotConsumeMatchingDraftAfterRestart(t *testing.T) {
	m := model()
	m.WorkerMinor = 12
	file := DraftAttachment{
		Attachment: domain.Attachment{ID: domain.NewID(), Name: "sample.png", MIME: "image/png", Size: 20, SHA256: strings.Repeat("a", 64)},
		DeviceID:   "d", WorkspaceID: "w", Ready: true,
	}
	m.Drafts["home"] = "same text"
	m.Attachments["home"] = []DraftAttachment{file}
	first, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}

	m.PrepareNewSession()
	if m.HasDraftContent("home") {
		t.Error("new composer inherited the submitted input")
	}
	m.Drafts["home"] = "same text"
	m.Attachments["home"] = []DraftAttachment{file}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	reopened := model()
	reopened.WorkerMinor = 12
	if err := reopened.Load(path); err != nil {
		t.Fatal(err)
	}
	if reopened.HasPendingInput() {
		t.Error("older receipt still owns the new composer after restart")
	}
	second, err := reopened.BeginSubmission()
	if err != nil {
		t.Errorf("could not submit the new draft independently: %v", err)
	}
	reopened.ApplyOperation(domain.Operation{ID: first.OperationID, SessionID: "first", State: domain.OperationConfirmed})
	if got := reopened.Drafts["home"]; got != "same text" || len(reopened.Attachments["home"]) != 1 || reopened.SessionID != "" {
		t.Errorf("older receipt consumed new input: text=%q attachments=%v session=%q", got, reopened.Attachments["home"], reopened.SessionID)
	}
	if second.OperationID != "" {
		reopened.ApplyOperation(domain.Operation{ID: second.OperationID, SessionID: "second", State: domain.OperationConfirmed})
		if reopened.SessionID != "second" || reopened.HasDraftContent("home") {
			t.Error("the new composer's own receipt did not complete its send")
		}
	}
}

func TestDetachedRejectedFirstSendRetainsInputWithoutOverwritingNewDraft(t *testing.T) {
	m := model()
	m.Drafts["home"] = "first input"
	first, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	m.PrepareNewSession()
	m.Drafts["home"] = "second draft"
	m.Reject(first, "rejected by device")
	if m.Drafts["home"] != "second draft" {
		t.Fatal("rejection replaced the new draft")
	}

	path := filepath.Join(t.TempDir(), "state.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	reopened := model()
	if err := reopened.Load(path); err != nil {
		t.Fatal(err)
	}
	reopened.RestoreRetainedHomeDraft(first.OperationID)
	if reopened.Drafts["home"] != "second draft" {
		t.Fatal("recovery overwrote the new draft")
	}
	reopened.Drafts["home"] = ""
	reopened.RestoreRetainedHomeDraft(first.OperationID)
	if reopened.Drafts["home"] != "first input" {
		t.Fatal("rejected first input could not be recovered")
	}
}
