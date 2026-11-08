package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"testing"
)

type configurableMemory struct {
	memoryHarness
	executable string
}

func (a configurableMemory) Installation() domain.RuntimeInstallation {
	return domain.RuntimeInstallation{Executable: a.executable, Configurable: true}
}
func (a configurableMemory) WithExecutable(path string) (harness.Adapter, error) {
	a.executable = path
	return a, nil
}
func TestRuntimeSettingsBelongToWorkerAndPersistWithoutStoppingSession(t *testing.T) {
	s, w := memoryWorker(t, configurableMemory{memoryHarness: memoryHarness{id: "custom", hold: true}})
	op, err := s.submit(domain.Submission{OperationID: "live-settings", WorkspaceID: w.ID, Text: "keep running", Config: domain.RunConfig{Model: "custom-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	active := s.currentRuntime(op.SessionID)
	path := "/usr/bin/true"
	_, err = s.configureRuntime(protocol.RuntimeConfiguration{HarnessID: "custom", Executable: path})
	if err != nil {
		t.Fatal(err)
	}
	if active.Stopped() {
		t.Fatal("configuration stopped active session")
	}
	data, err := os.ReadFile(filepath.Join(s.config.Directory, "runtime-settings.json"))
	if err != nil || len(data) == 0 {
		t.Fatal("configuration not persisted", err)
	}
	if s.adapter("custom").(configurableMemory).executable != path {
		t.Fatal("new configuration not adopted")
	}
	if _, err = s.configureRuntime(protocol.RuntimeConfiguration{HarnessID: "custom", Executable: "relative-command"}); err == nil {
		t.Fatal("relative user path accepted")
	}
	// Restart loading must override an old launch argument with the saved choice.
	a := configurableMemory{memoryHarness: memoryHarness{id: "custom"}, executable: "old-argument"}
	other := &Server{ctx: context.Background(), config: Config{Directory: s.config.Directory, Harnesses: []harness.Adapter{a}}}
	if err = other.configureAdapters(); err != nil {
		t.Fatal(err)
	}
	if other.adapter("custom").(configurableMemory).executable != path {
		t.Fatal("saved path lost on worker restart")
	}
}
func TestOldRuntimeAuthenticationFailureCannotInvalidateNewCatalog(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	op, err := s.submit(domain.Submission{OperationID: "old-auth", WorkspaceID: w.ID, Text: "keep", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	old := s.currentRuntime(op.SessionID)
	a.version.Store(2)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	s.reportRuntimeSetup(op.SessionID, old, &harness.SetupError{Issue: domain.RuntimeAuthenticationExpired, Message: "old auth expired"})
	if s.runtimeReadiness().State != domain.RuntimeReady {
		t.Fatal("retired credentials blocked the new catalog")
	}
}
func TestAgentSelectionRejectsInvalidBatchWithoutChangingSavedConfiguration(t *testing.T) {
	s, _ := memoryWorker(t, configurableMemory{memoryHarness: memoryHarness{id: "custom"}})
	result, err := s.configureAgents(protocol.DeviceAgents{Agents: []protocol.RuntimeConfiguration{{HarnessID: "custom", Executable: "/usr/bin/true"}}})
	if err != nil || !result.Ready {
		t.Fatal("valid device setup rejected", result, err)
	}
	before, err := os.ReadFile(filepath.Join(s.config.Directory, "runtime-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.configureAgents(protocol.DeviceAgents{Agents: []protocol.RuntimeConfiguration{{HarnessID: "custom", Executable: "relative"}}})
	if err == nil {
		t.Fatal("invalid batch accepted")
	}
	after, _ := os.ReadFile(filepath.Join(s.config.Directory, "runtime-settings.json"))
	if string(after) != string(before) {
		t.Fatal("invalid agent setup changed device configuration")
	}

}

func TestDisableAllAgentsPersistsAndKeepsLiveRuntimeControls(t *testing.T) {
	s, w := memoryWorker(t, configurableMemory{memoryHarness: memoryHarness{id: "custom", hold: true}})
	op, err := s.submit(domain.Submission{OperationID: "keep-live-agent", WorkspaceID: w.ID, Text: "keep", Config: domain.RunConfig{Model: "custom-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	live := s.currentRuntime(op.SessionID)
	result, err := s.configureAgents(protocol.DeviceAgents{})
	if err != nil || !result.Ready {
		t.Fatal("disable rejected", result, err)
	}
	if live.Stopped() {
		t.Fatal("disabled active runtime")
	}
	r, release, err := s.borrowInputRuntime(domain.Submission{SessionID: op.SessionID})
	if err != nil || r != live {
		t.Fatal("lost live controls", err)
	}
	release()
	if _, _, err = s.borrowCatalog("custom"); err == nil {
		t.Fatal("disabled catalog available")
	}
	if ready := s.runtimeReadiness(op.SessionID); ready.State == domain.RuntimeReady || len(ready.Harnesses) > 0 || len(ready.Sessions) != 1 {
		t.Fatal("disabled catalog leaked into readiness", ready)
	}
	other := &Server{ctx: context.Background(), config: Config{Directory: s.config.Directory, Harnesses: []harness.Adapter{configurableMemory{memoryHarness: memoryHarness{id: "custom"}}}}}
	if err = other.configureAdapters(); err != nil {
		t.Fatal(err)
	}
	if other.agentEnabled("custom") {
		t.Fatal("empty selection became all agents after restart")
	}
}

type optionsMemory struct {
	configurableMemory
	options domain.RuntimeOptions
}

func (a optionsMemory) WithExecutable(path string) (harness.Adapter, error) {
	a.executable = path
	return a, nil
}
func (a optionsMemory) WithRuntimeOptions(o domain.RuntimeOptions) (harness.Adapter, error) {
	if o.Directory == "invalid" {
		return nil, context.Canceled
	}
	a.options = o
	return a, nil
}
func TestRuntimeOptionsPersistAtomicallyAndSurviveAgentToggle(t *testing.T) {
	a := optionsMemory{configurableMemory: configurableMemory{memoryHarness: memoryHarness{id: "custom", hold: true}}}
	s, _ := memoryWorker(t, a)
	options := domain.RuntimeOptions{Directory: "private", Instructions: "explain", TitleModel: "custom-model"}
	if _, err := s.configureRuntime(protocol.RuntimeConfiguration{HarnessID: "custom", Options: &options}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(s.config.Directory, "runtime-settings.json"))
	invalid := domain.RuntimeOptions{Directory: "invalid"}
	if _, err := s.configureRuntime(protocol.RuntimeConfiguration{HarnessID: "custom", Options: &invalid}); err == nil {
		t.Fatal("invalid options accepted")
	}
	after, _ := os.ReadFile(filepath.Join(s.config.Directory, "runtime-settings.json"))
	if string(data) != string(after) {
		t.Fatal("invalid settings persisted")
	}
	if _, err := s.configureAgents(protocol.DeviceAgents{Agents: []protocol.RuntimeConfiguration{{HarnessID: "custom"}}}); err != nil {
		t.Fatal(err)
	}
	next := &Server{ctx: context.Background(), config: Config{Directory: s.config.Directory, Harnesses: []harness.Adapter{a}}}
	if err := next.configureAdapters(); err != nil {
		t.Fatal(err)
	}
	if got := next.adapter("custom").(optionsMemory).options; got.Directory != "private" || got.Instructions != "explain" || got.TitleModel != "custom-model" {
		t.Fatal("options lost", got)
	}
}

func TestInvalidSavedRuntimeOptionsOnlyDisableTheirHarness(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			dir, err := os.MkdirTemp("", "sh-settings-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			selected := []string{"healthy"}
			if enabled {
				selected = append(selected, "custom")
			}
			data, err := json.Marshal(runtimeSettingsDocument{Version: 1, Selected: selected, Options: map[string]domain.RuntimeOptions{"custom": {Directory: "invalid"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "runtime-settings.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{
				optionsMemory{configurableMemory: configurableMemory{memoryHarness: memoryHarness{id: "custom"}}},
				configurableMemory{memoryHarness: memoryHarness{id: "healthy"}},
			}})
			if err != nil {
				t.Fatal("saved Agent configuration prevented worker startup", err)
			}
			defer s.Close()
			profiles := s.runtimeProfiles(nil, true)
			bad := profiles.Harnesses[0]
			good, _ := profiles.ForHarness("healthy")
			if bad.State != domain.RuntimeUnavailable || bad.Error == "" || good.State != domain.RuntimeReady {
				t.Fatal("configuration failure was not isolated", profiles)
			}
			if bad.Installation == nil || bad.Installation.Options.Directory != "invalid" {
				t.Fatal("saved options unavailable for repair", bad)
			}
			if _, err := s.openCatalog(context.Background(), "custom", false); err == nil {
				t.Fatal("invalid saved configuration fell back to defaults")
			}
			if _, err := s.configureRuntime(protocol.RuntimeConfiguration{HarnessID: "custom"}); err == nil {
				t.Fatal("executable-only update discarded invalid saved options")
			}
			if _, err := s.configureAgents(protocol.DeviceAgents{Agents: []protocol.RuntimeConfiguration{{HarnessID: "custom"}}}); err == nil {
				t.Fatal("Agent selection discarded invalid saved options")
			}
			fixed := domain.RuntimeOptions{Directory: "repaired"}
			if enabled {
				result, err := s.configureAgents(protocol.DeviceAgents{Agents: []protocol.RuntimeConfiguration{{HarnessID: "custom", Options: &fixed}, {HarnessID: "healthy"}}})
				if err != nil || !result.Ready {
					t.Fatal("could not repair selected Agent", result, err)
				}
			} else if _, err := s.configureRuntime(protocol.RuntimeConfiguration{HarnessID: "custom", Options: &fixed}); err != nil {
				t.Fatal("could not repair saved configuration", err)
			}
			bad = s.runtimeProfiles(nil, true).Harnesses[0]
			if bad.State != domain.RuntimeReady || s.agentEnabled("custom") != enabled {
				t.Fatal("repair lost readiness or changed selection", bad)
			}
		})
	}
}
