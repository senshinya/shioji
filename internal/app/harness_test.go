package app

import (
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func harnessModel() *Model {
	m := NewModel()
	m.Connected = true
	m.WorkerMinor = 10
	m.WorkspaceID = "w"
	m.Snapshot.Workspaces = []domain.Workspace{{ID: "w"}}
	m.Readiness = domain.Readiness{HarnessID: "alpha", Harnesses: []domain.HarnessInfo{
		{ID: "alpha", Name: "Alpha", State: "ready", Models: []domain.Model{{ID: "a", Default: true, Fast: true, Efforts: []string{"medium"}, DefaultEffort: "medium"}}, Permissions: []string{"limited"}, Plan: true, Capabilities: domain.HarnessCapabilities{Steer: true}},
		{ID: "beta", Name: "Beta", State: "ready", Models: []domain.Model{{ID: "b", Default: true}}, Permissions: []string{"ask"}},
	}}
	return m
}
func TestHarnessSelectionKeepsDraftAndIndependentConfiguration(t *testing.T) {
	m := harnessModel()
	m.Drafts["home"] = "keep ordinary draft"
	first := m.Config()
	first.Fast, first.Plan = true, true
	m.Configs["home"] = first
	if err := m.SelectHarness("beta"); err != nil {
		t.Fatal(err)
	}
	if c := m.Config(); c.Model != "b" || c.Permission != "ask" || c.Effort != "" || c.Fast || c.Plan {
		t.Fatal("borrowed another harness's configuration", c)
	}
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	if in.HarnessID != "beta" || in.Config.Model != "b" || m.Drafts["home"] != "keep ordinary draft" {
		t.Fatal("wrong submission or lost draft", in)
	}
	m.Reject(in, "not sent")
	if err = m.SelectHarness("alpha"); err != nil {
		t.Fatal(err)
	}
	if c := m.Config(); c.Model != "a" || !c.Fast || !c.Plan || c.Effort != "medium" {
		t.Fatal("lost original harness settings", c)
	}
}
func TestExistingSessionUsesItsHarnessWhenAnotherIsDeviceDefault(t *testing.T) {
	m := harnessModel()
	m.SessionID = "saved"
	m.Snapshot.Sessions = []domain.Session{{ID: "saved", WorkspaceID: "w", HarnessID: "beta", Status: "completed"}}
	m.Drafts["session:saved"] = "next"
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	if in.HarnessID != "beta" || in.Config.Model != "b" {
		t.Fatal("existing conversation adopted device default", in)
	}
	if err = m.SelectHarness("alpha"); err == nil {
		t.Fatal("existing session changed harness")
	}
}
func TestUnavailableHarnessPreservesSelectionWithoutAcceptingInput(t *testing.T) {
	m := harnessModel()
	m.Drafts["home"] = "keep"
	if err := m.SelectHarness("beta"); err != nil {
		t.Fatal(err)
	}
	m.Readiness.Harnesses = m.Readiness.Harnesses[:1]
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("missing adapter silently fell back")
	}
	if m.HarnessID() != "beta" || m.Drafts["home"] != "keep" || len(m.Pending) != 0 {
		t.Fatal("lost unavailable selection or draft")
	}
}

func TestUnavailableCatalogDoesNotErasePreviouslyChosenConfiguration(t *testing.T) {
	m := harnessModel()
	c := m.Config()
	c.Fast, c.Plan = true, true
	m.Configs["home"] = c
	m.Readiness = domain.Readiness{HarnessID: "alpha", State: "unavailable"}
	if got := m.Config(); got != c {
		t.Fatalf("offline catalog overwrote saved choices: %+v", got)
	}
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("offline configuration authorized a submission")
	}
}

func TestHomeHarnessConfigurationsStayWithWorkspaceAcrossNavigation(t *testing.T) {
	m := harnessModel()
	m.Readiness.Harnesses[1].Models = append(m.Readiness.Harnesses[1].Models, domain.Model{ID: "b-plus"})
	if err := m.SelectHarness("beta"); err != nil {
		t.Fatal(err)
	}
	c := m.Config()
	c.Model = "b-plus"
	m.Configs["home"] = c
	m.WorkspaceID = "second"
	second := m.Config()
	if second.Model != "a" {
		t.Fatal("new workspace inherited another harness")
	}
	second.Plan = true
	m.Configs["home"] = second
	m.WorkspaceID = "w"
	if got := m.Config(); got.Model != "b-plus" {
		t.Fatal("workspace lost its chosen model", got)
	}
	m.WorkspaceID = "second"
	if got := m.Config(); !got.Plan {
		t.Fatal("workspace lost its planning selection", got)
	}
}

func TestLegacyHomeConfigurationSurvivesFirstCatalogArrival(t *testing.T) {
	m := NewModel()
	m.WorkspaceID = "w"
	m.WorkerMinor = 10
	m.Configs["home"] = domain.RunConfig{Model: "a-plus", Permission: "limited", Effort: "medium"}
	m.Config() // The initial disconnected render has no harness identity yet.
	ready := harnessModel().Readiness
	ready.Harnesses[0].Models = append(ready.Harnesses[0].Models, domain.Model{ID: "a-plus", Efforts: []string{"medium"}, DefaultEffort: "medium"})
	m.Readiness = ready
	if got := m.Config(); got.Model != "a-plus" || got.Effort != "medium" {
		t.Fatal("first catalog arrival discarded legacy settings", got)
	}
}

func TestHomeHarnessConfigurationSurvivesRestartOnAnotherWorkspace(t *testing.T) {
	m := harnessModel()
	if err := m.SelectHarness("beta"); err != nil {
		t.Fatal(err)
	}
	m.Drafts["home"] = "unsent"
	m.WorkspaceID = "second" // Visit another workspace's session without rendering home.
	m.SessionID = "saved"
	data, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteState(path, data); err != nil {
		t.Fatal(err)
	}
	next := harnessModel()
	if err := next.Load(path); err != nil {
		t.Fatal(err)
	}
	next.SessionID = ""
	if got := next.Config(); got.Model != "a" {
		t.Fatal("wrong workspace configuration after restart", got)
	}
	next.WorkspaceID = "w"
	if got := next.Config(); got.Model != "b" || next.Drafts["home"] != "unsent" {
		t.Fatal("saved harness choices or draft were lost", got)
	}
}

func TestSavedDefaultHarnessChoicesSurviveOfflineFirstRender(t *testing.T) {
	m := harnessModel()
	c := m.Config()
	c.Plan = true
	m.Configs["home"] = c
	b, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteState(path, b); err != nil {
		t.Fatal(err)
	}
	next := NewModel()
	next.WorkerMinor = 10
	if err := next.Load(path); err != nil {
		t.Fatal(err)
	}
	next.Config()
	next.Readiness = m.Readiness
	if got := next.Config(); got != c {
		t.Fatal("offline first render erased saved default choices", got, c)
	}
}
