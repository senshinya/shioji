package app

import (
	"path/filepath"
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

func TestFileReferenceSurvivesSavedDraftAndQueueEdit(t *testing.T) {
	m := NewModel()
	m.WorkspaceID = "w"
	m.SessionID = "s"
	a := domain.Attachment{ID: "ref", Name: "tree.go", SourcePath: "/device/work/tree.go"}
	m.Attachments["session:s"] = []DraftAttachment{{Attachment: a, Ready: true, WorkspaceID: "w"}}
	m.Drafts["session:s"] = "ordinary"
	queued := domain.Attachment{ID: "queued-ref", Name: "other.go", SourcePath: "/device/work/other.go"}
	m.Details["s"] = domain.SessionDetail{Queue: domain.QueueState{SessionID: "s", Revision: 1, Items: []domain.QueueItem{{ID: "q", Input: domain.Submission{WorkspaceID: "w", Text: "queued", Attachments: []domain.Attachment{queued}}}}}}
	if err := m.EditQueued("q"); err != nil {
		t.Fatal(err)
	}
	b, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "client.json")
	if err = WriteState(path, b); err != nil {
		t.Fatal(err)
	}
	next := NewModel()
	if err = next.Load(path); err != nil {
		t.Fatal(err)
	}
	if next.Attachments["session:s"][0].Attachment.SourcePath != queued.SourcePath {
		t.Fatal("queue reference lost after restart")
	}
	next.CancelQueueEdit("s")
	if next.Drafts["session:s"] != "ordinary" || next.Attachments["session:s"][0].Attachment != a {
		t.Fatal("cancel did not restore displaced file reference")
	}
}

func TestSkillDraftSurvivesRestartAndCancelQueueEdit(t *testing.T) {
	m := harnessModel()
	m.Readiness.Harnesses[0].Capabilities.Skills = true
	m.WorkerMinor = 11
	m.SessionID = "s"
	m.Snapshot.Sessions = []domain.Session{{ID: "s", WorkspaceID: "w", HarnessID: "alpha", Status: "completed"}}
	ordinary := DraftSkill{Reference: domain.SkillReference{ID: "opaque-skill", Name: "review"}, WorkspaceID: "w", HarnessID: "alpha"}
	m.DraftSkills["session:s"] = []DraftSkill{ordinary}
	m.Drafts["session:s"] = "draft"
	queued := domain.SkillReference{ID: "queued-skill", Name: "test"}
	m.Details["s"] = domain.SessionDetail{Queue: domain.QueueState{SessionID: "s", Revision: 1, Items: []domain.QueueItem{{ID: "q", Input: domain.Submission{WorkspaceID: "w", Text: "queued", Skills: []domain.SkillReference{queued}}}}}}
	if err := m.EditQueued("q"); err != nil {
		t.Fatal(err)
	}
	b, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err = WriteState(path, b); err != nil {
		t.Fatal(err)
	}
	next := NewModel()
	if err = next.Load(path); err != nil {
		t.Fatal(err)
	}
	if next.DraftSkills["session:s"][0].Reference != queued {
		t.Fatal("edited Skill lost after reload")
	}
	next.CancelQueueEdit("s")
	if next.DraftSkills["session:s"][0] != ordinary || next.Drafts["session:s"] != "draft" {
		t.Fatal("displaced Skill or text lost")
	}
}

func TestLateSkillConfirmationKeepsNewHomeWorkspaceSelection(t *testing.T) {
	m := harnessModel()
	m.WorkerMinor = 11
	m.Readiness.Harnesses[0].Capabilities.Skills = true
	ref := domain.SkillReference{ID: "global-skill", Name: "review"}
	m.DraftSkills["home"] = []DraftSkill{{Reference: ref, WorkspaceID: "w", HarnessID: "alpha"}}
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	m.WorkspaceID = "second"
	m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: "second"})
	m.DraftSkills["home"] = []DraftSkill{{Reference: ref, WorkspaceID: "second", HarnessID: "alpha"}}
	m.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "accepted", State: "confirmed"})
	if len(m.DraftSkills["home"]) != 1 || m.DraftSkills["home"][0].WorkspaceID != "second" {
		t.Fatal("late confirmation deleted another workspace's Skill")
	}
}

func TestRecoveryDraftDeduplicatesSameScopedSkill(t *testing.T) {
	m := harnessModel()
	ref := DraftSkill{Reference: domain.SkillReference{ID: "global", Name: "review"}, WorkspaceID: "w", HarnessID: "alpha"}
	ref.InlinePosition = 2
	m.DraftSkills["home"] = []DraftSkill{ref}
	ref.InlinePosition = 5
	m.DraftSkills["session:s"] = []DraftSkill{ref}
	m.Details["s"] = domain.SessionDetail{Session: domain.Session{ID: "s", WorkspaceID: "w", HarnessID: "alpha", Status: "abandoned"}}
	if err := m.PrepareRecoveryDraft("s"); err != nil {
		t.Fatal(err)
	}
	if len(m.DraftSkills["home"]) != 1 || len(m.DraftSkills["session:s"]) != 1 {
		t.Fatal("recovery duplicated or consumed original Skill")
	}
	if m.DraftSkills["home"][0].InlinePosition != 0 || m.DraftSkills["session:s"][0].InlinePosition != 5 {
		t.Fatal("recovery reused stale positions or mutated original draft")
	}
}
