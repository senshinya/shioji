package app

import (
	"encoding/json"
	"shioji/internal/domain"
	"testing"
)

func TestRefreshedCatalogKeepsRunningSessionOnItsOwnModelsAndCapabilities(t *testing.T) {
	m := NewModel()
	m.Connected = true
	m.WorkerMinor = 14
	m.WorkspaceID = "w"
	m.SessionID = "old"
	m.Snapshot = domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w"}}, Sessions: []domain.Session{{ID: "old", WorkspaceID: "w", HarnessID: "agent", Status: "running", Config: domain.RunConfig{Model: "old-model", Permission: "limited"}}}}
	raw := []byte(`{"harnessId":"agent","harnesses":[{"id":"agent","name":"Agent","state":"ready","models":[{"id":"new-model","default":true}],"permissions":["limited"]}],"sessions":{"old":{"id":"agent","name":"Agent","state":"ready","models":[{"id":"old-model"}],"permissions":["limited"],"capabilities":{"steer":true,"restart":true}}}}`)
	if err := json.Unmarshal(raw, &m.Readiness); err != nil {
		t.Fatal(err)
	}
	if got := m.Config(); got.Model != "old-model" || !m.RunReadiness().Capabilities.Steer {
		t.Fatal("catalog refresh rewrote active session configuration", got, m.RunReadiness())
	}
	if m.RunReadiness().Capabilities.Restart {
		t.Fatal("removed next-turn capability remained enabled through old runtime profile")
	}
	m.SessionID = ""
	if got := m.Config(); got.Model != "new-model" || m.RunReadiness().Capabilities.Steer {
		t.Fatal("home did not adopt new catalog", got)
	}
}
func TestRemovedModelPreservesDraftAndRequiresExplicitNewChoice(t *testing.T) {
	m := NewModel()
	m.Connected = true
	m.WorkerMinor = 14
	m.WorkspaceID = "w"
	m.Snapshot.Workspaces = []domain.Workspace{{ID: "w"}}
	m.Readiness = domain.Readiness{HarnessID: "agent", Harnesses: []domain.HarnessInfo{{ID: "agent", Name: "Agent", State: domain.RuntimeReady, Models: []domain.Model{{ID: "old-model", Default: true}}, Permissions: []string{"limited"}}}}
	selected := m.Config()
	m.Drafts["home"] = "keep this prompt"
	m.Readiness.Harnesses[0].Models = []domain.Model{{ID: "new-model", Default: true}}
	if got := m.Config(); got != selected {
		t.Fatal("removed model silently replaced user choice", selected, got)
	}
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("removed model was submitted")
	}
	if m.Drafts["home"] != "keep this prompt" || len(m.Pending) != 0 {
		t.Fatal("catalog refresh consumed draft or queued an invalid input")
	}
}

func TestCatalogUnavailableDoesNotDisableControlOfAnExecutingTurn(t *testing.T) {
	m := harnessModel()
	m.WorkerMinor = 14
	m.SessionID = "running"
	session := domain.Session{ID: "running", WorkspaceID: "w", HarnessID: "alpha", Status: "running", Config: domain.RunConfig{Model: "a", Effort: "medium", Permission: "limited"}}
	m.Snapshot.Sessions = []domain.Session{session}
	m.Details[session.ID] = domain.SessionDetail{Session: session, Turns: []domain.Turn{{ID: "live", Status: "running"}}}
	raw := []byte(`{"harnessId":"alpha","harnesses":[{"id":"alpha","name":"Alpha","state":"unavailable","models":[{"id":"a"}],"permissions":["limited"]}],"sessions":{"running":{"id":"alpha","name":"Alpha","state":"ready","models":[{"id":"a","efforts":["medium"],"defaultEffort":"medium"}],"permissions":["limited"],"capabilities":{"steer":true,"interrupt":true}}}}`)
	json.Unmarshal(raw, &m.Readiness)
	m.Drafts[m.DraftKey()] = "keep steering live work"
	if _, method, turn, err := m.BeginMessage(true); err != nil || method != "turn.steer" || turn != "live" {
		t.Fatal("new catalog disabled current-turn control", method, turn, err)
	}
}
