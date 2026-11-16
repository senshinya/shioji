package app

import (
	"path/filepath"
	"testing"

	"shioji/internal/domain"
)

func recentSessionModel() *Model {
	m := harnessModel()
	m.LocalDeviceID = "device-a"
	m.Snapshot.DeviceID = "device-a"
	m.Snapshot.Workspaces = []domain.Workspace{{ID: "w", DeviceID: "device-a"}, {ID: "other-workspace", DeviceID: "device-a"}, {ID: "remote-workspace", DeviceID: "device-b"}}
	m.Readiness.Harnesses[0].Models = append(m.Readiness.Harnesses[0].Models, domain.Model{ID: "a-plus", Efforts: []string{"medium", "high"}, DefaultEffort: "medium"})
	m.Readiness.Harnesses[1].Plan = true
	m.Readiness.Harnesses[1].Permissions = []string{"ask", "yolo"}
	m.Readiness.Harnesses[1].Models = append(m.Readiness.Harnesses[1].Models, domain.Model{ID: "b-pro", Efforts: []string{"low", "high"}, DefaultEffort: "low", Fast: true})
	m.Snapshot.Sessions = []domain.Session{
		{ID: "z-old", WorkspaceID: "w", HarnessID: "alpha", Config: domain.RunConfig{Model: "a-plus", Effort: "medium", Permission: "limited"}, Revision: 1},
		{ID: "a-latest", WorkspaceID: "other-workspace", HarnessID: "beta", Config: domain.RunConfig{Model: "b-pro", Effort: "high", Permission: "yolo", Plan: true, Fast: true}, Revision: 1},
		{ID: "foreign-newest", WorkspaceID: "remote-workspace", HarnessID: "alpha", Config: domain.RunConfig{Model: "a-plus", Effort: "high", Permission: "limited"}, Revision: 1},
	}
	return m
}

func TestHomeDefaultsUseNewestSessionOnTheOwningDevice(t *testing.T) {
	m := recentSessionModel()
	config := m.Config()
	if m.HarnessID() != "beta" || config.Model != "b-pro" || config.Effort != "high" {
		t.Fatal("new conversation did not inherit the same device's newest session across workspaces", m.HarnessID(), config)
	}
	if config.Permission != "yolo" || !config.Plan || !config.Fast {
		t.Fatal("new conversation did not inherit permission, plan and fast choices", config)
	}
	if len(m.Pending) != 0 || len(m.Snapshot.Sessions) != 3 {
		t.Fatal("choosing defaults created or submitted a session")
	}
}

func TestHomeDefaultsReplacePreviousExecutionChoices(t *testing.T) {
	m := recentSessionModel()
	m.Config()
	m.SetConfig("home", domain.RunConfig{Model: "b-pro", Effort: "high", Permission: "yolo", Plan: true, Fast: true})
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "newest-a", WorkspaceID: "w", HarnessID: "beta", Config: domain.RunConfig{Model: "b-pro", Effort: "low", Permission: "ask"}})
	m.PrepareNewSession()
	if got := m.Config(); got != (domain.RunConfig{Model: "b-pro", Effort: "low", Permission: "ask"}) {
		t.Fatal("new conversation retained the previous permission or enabled modes", got)
	}
}

func TestHomeDefaultsValidateInheritedExecutionChoices(t *testing.T) {
	m := recentSessionModel()
	m.Readiness.Harnesses[1].Permissions = []string{"ask"}
	m.Readiness.Harnesses[1].Plan = false
	m.Readiness.Harnesses[1].Models[1].Fast = false
	if got := m.Config(); got != (domain.RunConfig{Model: "b-pro", Effort: "high", Permission: "ask"}) {
		t.Fatal("new conversation retained unsupported inherited choices", got)
	}
}

func TestHomeDefaultsUseCreationOrderRatherThanUpdatesOrNavigation(t *testing.T) {
	m := recentSessionModel()
	old := m.Snapshot.Sessions[0]
	old.Revision = 99
	old.Config.Effort = "high"
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "session", Session: &old}); err != nil {
		t.Fatal(err)
	}
	m.Navigation.Pinned = []SessionLocation{{DeviceID: "device-a", WorkspaceID: "w", SessionID: old.ID}}
	m.Navigation.Workspaces = []WorkspaceNavigation{{DeviceID: "device-a", WorkspaceID: "w", Sessions: []string{old.ID}}}
	if got := m.Config(); m.HarnessID() != "beta" || got.Model != "b-pro" || got.Effort != "high" {
		t.Fatal("old session activity or sidebar order changed creation-based defaults", m.HarnessID(), got)
	}
}

func TestHomeDefaultsKeepAnExistingDraftChoice(t *testing.T) {
	for _, attachmentOnly := range []bool{false, true} {
		m := recentSessionModel()
		m.HomeHarnesses = map[string]string{"w": "alpha"}
		m.homeConfigContext = "w/alpha"
		want := domain.RunConfig{Model: "a-plus", Effort: "high", Permission: "limited", Plan: true, Fast: true}
		m.Configs["home"] = want
		if attachmentOnly {
			m.Attachments["home"] = []DraftAttachment{{Attachment: domain.Attachment{ID: "image"}}}
		} else {
			m.Drafts["home"] = "keep my unsent prompt"
		}
		m.Readiness.Harnesses[0].Plan = true
		m.Readiness.Harnesses[0].Models[1].Fast = true
		if got := m.Config(); m.HarnessID() != "alpha" || got != want {
			t.Fatal("new defaults overwrote an existing draft", attachmentOnly, m.HarnessID(), got)
		}
	}
}

func TestHomeDefaultsKeepALegacyDraftWithoutHarnessMetadata(t *testing.T) {
	m := recentSessionModel()
	want := domain.RunConfig{Model: "a-plus", Effort: "high", Permission: "limited"}
	m.Configs["home"] = want
	m.Drafts["home"] = "saved before per-harness home configuration"
	if got := m.Config(); m.HarnessID() != "alpha" || got != want {
		t.Fatal("default inheritance replaced a saved legacy draft choice", m.HarnessID(), got)
	}
}

func TestHomeDefaultsRefreshOnlyWhenOpeningAnEmptyComposer(t *testing.T) {
	m := recentSessionModel()
	m.Config()
	manual := domain.RunConfig{Model: "b", Permission: "ask"}
	m.SetConfig("home", manual)
	newest := domain.Session{ID: "newest-a", WorkspaceID: "w", HarnessID: "alpha", Config: domain.RunConfig{Model: "a-plus", Effort: "high"}}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, newest)
	if got := m.Config(); m.HarnessID() != "beta" || got.Model != "b" {
		t.Fatal("background session arrival overwrote manual home choices", got)
	}
	m.PrepareNewSession()
	if got := m.Config(); m.HarnessID() != "alpha" || got.Model != "a-plus" || got.Effort != "high" {
		t.Fatal("reopening an empty composer retained stale defaults", m.HarnessID(), got)
	}
}

func TestHomeDefaultsWaitForFreshCatalogAndKeepOfflineManualChoices(t *testing.T) {
	for _, manual := range []bool{false, true} {
		m := recentSessionModel()
		m.Connected = false
		m.HomeHarnesses = map[string]string{"w": "alpha"}
		m.homeConfigContext = "w/alpha"
		previous := domain.RunConfig{Model: "a", Effort: "medium", Permission: "limited"}
		m.Configs["home"] = previous
		if got := m.Config(); got != previous {
			t.Fatal("cached offline catalog changed existing choices", got)
		}
		if manual {
			previous.Model, previous.Effort = "a-plus", "high"
			m.SetConfig("home", previous)
		}
		m.Connected = true
		got := m.Config()
		if manual && (m.HarnessID() != "alpha" || got != previous) {
			t.Fatal("reconnect overwrote a manual offline selection", got)
		}
		if !manual && (m.HarnessID() != "beta" || got.Model != "b-pro" || got.Effort != "high") {
			t.Fatal("fresh catalog did not supply the latest device defaults", got)
		}
	}
}

func TestHomeDefaultsDoNotSilentlyReplaceAnUnavailableModel(t *testing.T) {
	m := recentSessionModel()
	m.Readiness.Harnesses[1].Models = m.Readiness.Harnesses[1].Models[:1]
	if got := m.Config(); got.Model != "b-pro" || !m.ModelUnavailable() || got.Permission != "yolo" {
		t.Fatal("unavailable inherited model was replaced or lost its inherited permission", got)
	}
	m.Drafts["home"] = "keep prompt"
	if _, err := m.BeginSubmission(); err == nil || len(m.Pending) != 0 || m.Drafts["home"] != "keep prompt" {
		t.Fatal("unavailable inherited model accepted or consumed input", err)
	}
}

func TestHomeDefaultsKeepAPendingSubmission(t *testing.T) {
	m := recentSessionModel()
	m.HomeHarnesses = map[string]string{"w": "alpha"}
	m.homeConfigContext = "w/alpha"
	want := domain.RunConfig{Model: "a-plus", Effort: "medium", Permission: "limited"}
	m.Configs["home"] = want
	m.Pending["submitted"] = domain.Submission{OperationID: "submitted", WorkspaceID: "w", Config: want}
	m.PrepareNewSession()
	if got := m.Config(); m.HarnessID() != "alpha" || got != want || len(m.Pending) != 1 {
		t.Fatal("opening home changed an unconfirmed submission", m.HarnessID(), got)
	}
}

func TestHomeDefaultsAfterRestartRefreshOnlyAnEmptyDraft(t *testing.T) {
	for _, draft := range []bool{false, true} {
		m := recentSessionModel()
		m.Config()
		m.SetConfig("home", domain.RunConfig{Model: "b", Permission: "ask"})
		if draft {
			m.Drafts["home"] = "keep saved draft"
		}
		path := filepath.Join(t.TempDir(), "state.json")
		if err := m.Save(path); err != nil {
			t.Fatal(err)
		}
		next := recentSessionModel()
		next.Snapshot.Sessions = append(next.Snapshot.Sessions, domain.Session{ID: "created-while-closed", WorkspaceID: "w", HarnessID: "alpha", Config: domain.RunConfig{Model: "a-plus", Effort: "high"}})
		if err := next.Load(path); err != nil {
			t.Fatal(err)
		}
		got := next.Config()
		if draft && (next.HarnessID() != "beta" || got.Model != "b" || next.Drafts["home"] != "keep saved draft") {
			t.Fatal("restart overwrote the saved draft", next.HarnessID(), got)
		}
		if !draft && (next.HarnessID() != "alpha" || got.Model != "a-plus" || got.Effort != "high") {
			t.Fatal("restart retained empty home defaults from an older session", next.HarnessID(), got)
		}
	}
}
