package app

import (
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func model() *Model {
	m := NewModel()
	m.Connected = true
	m.Readiness = domain.Readiness{State: "ready", Permissions: []string{"default", "read-only"}, Models: []domain.Model{{ID: "real-default", Default: true, Efforts: []string{"low", "high"}, DefaultEffort: "low"}}}
	m.ApplySnapshot(domain.Snapshot{DeviceID: "d", Generation: "g", Workspaces: []domain.Workspace{{ID: "w", DeviceID: "d", Name: "Example"}}})
	m.WorkspaceID = "w"
	return m
}
func TestFirstTurnKeepsDraftUntilHarnessConfirmation(t *testing.T) {
	m := model()
	m.Drafts["home"] = "hello"
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	m.Accepted(in, domain.Operation{ID: in.OperationID, SessionID: "s", State: "accepted"})
	if m.Drafts["home"] != "hello" || m.SessionID != "" {
		t.Fatal("worker acceptance cleared draft before harness acceptance")
	}
	m.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	if m.Drafts["home"] != "" || m.SessionID != "s" {
		t.Fatal("confirmed first turn did not focus session")
	}
}
func TestRejectedAndUnknownSubmissionsRetainDraft(t *testing.T) {
	for _, state := range []string{"rejected", "indeterminate"} {
		m := model()
		m.Drafts["home"] = "hello"
		in, _ := m.BeginSubmission()
		m.Accepted(in, domain.Operation{ID: in.OperationID, SessionID: "s", State: state})
		if m.Drafts["home"] != "hello" {
			t.Fatal("lost draft on " + state)
		}
	}
}
func TestConfirmationDoesNotDeleteNewDraftTypedWhileSubmitting(t *testing.T) {
	m := model()
	m.Drafts["home"] = "first"
	in, _ := m.BeginSubmission()
	m.Accepted(in, domain.Operation{ID: in.OperationID, SessionID: "s", State: "accepted"})
	m.Drafts["home"] = "new draft"
	m.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	if m.Drafts["home"] != "new draft" {
		t.Fatal("confirmation destroyed newer text")
	}
}
func TestDraftsAndQuestionChoicesSurviveNavigationAndRestart(t *testing.T) {
	m := model()
	m.Drafts["home"] = "home"
	m.Drafts["session:s"] = "session"
	m.Answers["s/r/q"] = "Gray"
	m.SessionID = "s"
	path := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if restored.Drafts["home"] != "home" || restored.Drafts["session:s"] != "session" || restored.Answers["s/r/q"] != "Gray" {
		t.Fatal("independent drafts were lost")
	}
	if restored.SessionID != "" {
		t.Fatal("startup must open home")
	}
}
func TestOfflineEmptyAndMissingWorkspaceCannotSubmit(t *testing.T) {
	m := model()
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("empty accepted")
	}
	m.Drafts["home"] = "hello"
	m.Connected = false
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("offline accepted")
	}
	m.Connected = true
	m.WorkspaceID = "missing"
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("missing workspace accepted")
	}
}
func TestModelChangeDropsUnsupportedFastAndEffort(t *testing.T) {
	m := model()
	m.Configs["home"] = domain.RunConfig{Model: "real-default", Effort: "ultra", Fast: true, Permission: "default"}
	cfg := m.Config()
	if cfg.Effort != "low" || cfg.Fast {
		t.Fatalf("stale unsupported config: %+v", cfg)
	}
}
func TestEventGapRequiresSnapshotInsteadOfSkippingOutput(t *testing.T) {
	m := model()
	if err := m.ApplyEvent(domain.Event{Sequence: 2, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "i", Text: "lost prefix"}}); err == nil {
		t.Fatal("cursor gap silently skipped")
	}
	if m.Sequence != 0 {
		t.Fatal("advanced broken cursor")
	}
}
