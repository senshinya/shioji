package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
)

type profileTestHarness struct{ memoryHarness }

func (a profileTestHarness) ProfileModes() []domain.AgentProfileMode {
	return []domain.AgentProfileMode{domain.AgentProfileAccount}
}

func (a profileTestHarness) WithAgentProfile(id, name, directory string, mode domain.AgentProfileMode) (harness.Adapter, error) {
	return memoryHarness{id: id}, nil
}

func TestWorkerUpgradeMigratesSavedNativeProfile(t *testing.T) {
	dir, err := os.MkdirTemp("", "shioji-profile-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	settings := []byte(`{"version":1,"paths":{"saved":""},"selected":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "runtime-settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{Directory: dir, StartPaused: true, ServiceInstance: "candidate", ServiceOperation: "update", Harnesses: []harness.Adapter{
		configurableMemory{memoryHarness: memoryHarness{id: "saved"}},
		configurableMemory{memoryHarness: memoryHarness{id: "discovered"}},
	}}
	server, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	profilePath := filepath.Join(dir, "system-agent-profiles.json")
	if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
		t.Fatal("uncommitted candidate migrated system Profiles", err)
	}
	if err = supervisor.SaveDrainIntent(dir, supervisor.DrainIntent{ID: "update", Instance: "old", DeviceID: server.store.DeviceID()}); err != nil {
		t.Fatal(err)
	}
	resume := supervisor.Control{Instance: "candidate", OperationID: "update"}
	if err = server.ResumeService(resume); err != nil {
		t.Fatal(err)
	}
	migrated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal("upgrade did not persist the system Profile", err)
	}
	var saved []struct{ ID, Name string }
	if err := json.Unmarshal(migrated, &saved); err != nil || len(saved) != 1 || saved[0].ID != "saved" || saved[0].Name == "" {
		t.Fatal("upgrade did not preserve the native Profile identity", saved, err)
	}
	firstWrite, err := os.Stat(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	assertMigrationUnchanged := func() {
		t.Helper()
		current, err := os.Stat(profilePath)
		if err != nil || !os.SameFile(firstWrite, current) {
			t.Fatal("migration rewrote an existing system Profile", err)
		}
		data, err := os.ReadFile(profilePath)
		if err != nil || string(data) != string(migrated) {
			t.Fatal("migration changed an existing system Profile", err)
		}
	}
	if err = server.ResumeService(resume); err != nil {
		t.Fatal(err)
	}
	assertMigrationUnchanged()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	config.StartPaused = false
	config.ServiceInstance, config.ServiceOperation = "", ""
	next, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	assertMigrationUnchanged()
	if got, err := os.ReadFile(filepath.Join(dir, "runtime-settings.json")); err != nil || string(got) != string(settings) {
		t.Fatal("migration changed legacy settings", err)
	}
	if _, err := os.Stat(next.agentProfileHome("saved")); !os.IsNotExist(err) {
		t.Fatal("system Profile must retain its native home", err)
	}
	result, err := next.handle("runtime.catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog := result.(domain.Readiness).Harnesses
	if len(catalog) != 2 {
		t.Fatal(catalog)
	}
	for _, info := range catalog {
		if info.Configured || info.Profile != nil || info.SystemProfile != (info.ID == "saved") {
			t.Errorf("wrong system Profile classification: %+v", info)
		}
	}
}

func TestAgentProfileSurvivesRestartAlongsideLegacySession(t *testing.T) {
	adapter := profileTestHarness{memoryHarness{id: "legacy"}}
	server, workspace := memoryWorker(t, adapter)
	settings := []byte(`{"version":1,"paths":{},"selected":["legacy"]}`)
	if err := os.WriteFile(filepath.Join(server.config.Directory, "runtime-settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}
	submit := func(server *Server, operation, profile, session string) domain.Session {
		t.Helper()
		input := domain.Submission{
			OperationID: operation, WorkspaceID: workspace.ID, SessionID: session, HarnessID: profile, Text: "hello",
			Config: domain.RunConfig{Model: profile + "-model", Permission: "limited"},
		}
		receipt, err := server.submit(input)
		if err != nil {
			t.Fatal(err)
		}
		if receipt = awaitReceipt(t, server, operation); receipt.State != domain.OperationConfirmed {
			t.Fatal(receipt)
		}
		saved, err := server.store.SessionRecord(receipt.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	legacy := submit(server, "old-submit", "legacy", "")
	id := "4caba940-68a1-411a-b011-65057140c964"
	body, _ := json.Marshal(map[string]string{"id": id, "harnessId": "legacy", "name": "Work", "mode": "account"})
	for range 2 {
		if _, err := server.handle("runtime.profile.create", body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := server.recheckRuntime(id); err != nil {
		t.Fatal(err)
	}
	managed := submit(server, "profile-submit", id, "")
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := New(context.Background(), server.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	next.dispatchMu.Lock()
	next.pressureSample = func() uint32 { return 1 }
	next.dispatchMu.Unlock()
	if saved := submit(next, "legacy-resume", "legacy", legacy.ID); saved.ThreadID != legacy.ThreadID {
		t.Fatalf("legacy thread changed: %q to %q", legacy.ThreadID, saved.ThreadID)
	}
	if saved := submit(next, "profile-resume", id, managed.ID); saved.HarnessID != id || saved.ThreadID != managed.ThreadID {
		t.Fatalf("profile binding changed: %+v", saved)
	}
	raw, err := os.ReadFile(filepath.Join(next.config.Directory, "runtime-settings.json"))
	if err != nil || string(raw) != string(settings) {
		t.Fatalf("legacy settings changed: %q, %v", raw, err)
	}
}

func TestMissingProfileHomeCannotReplaceSavedAccountOrBlockLegacy(t *testing.T) {
	server, _ := memoryWorker(t, profileTestHarness{memoryHarness{id: "legacy"}})
	const id = "4caba940-68a1-411a-b011-65057140c964"
	body, _ := json.Marshal(map[string]string{"id": id, "harnessId": "legacy", "name": "Work", "mode": "account"})
	if _, err := server.handle("runtime.profile.create", body); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(server.config.Directory, "agent-profiles", id)
	if err := os.Rename(path, path+"-backup"); err != nil {
		t.Fatal(err)
	}
	next, err := New(context.Background(), server.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	ready := next.runtimeReadiness()
	profile, ok := ready.ForHarness(id)
	if !ok || profile.State != domain.RuntimeUnavailable {
		t.Fatalf("missing private home was treated as a valid profile: %+v", profile)
	}
	if legacy, ok := ready.ForHarness("legacy"); !ok || legacy.State != domain.RuntimeReady {
		t.Fatalf("missing profile blocked the legacy configuration: %+v", legacy)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("worker recreated the missing native account home: %v", err)
	}
}

type profileSignInHarness struct{ signInMemory }

func (a profileSignInHarness) ProfileModes() []domain.AgentProfileMode {
	return []domain.AgentProfileMode{domain.AgentProfileAccount}
}

func (a profileSignInHarness) WithAgentProfile(id, name, directory string, mode domain.AgentProfileMode) (harness.Adapter, error) {
	a.id = id
	return a.signInMemory, nil
}

func TestProfileAuthorizationBindingSurvivesWorkerRestart(t *testing.T) {
	adapter := profileSignInHarness{signInMemory: signInMemory{
		configurableMemory: configurableMemory{memoryHarness: memoryHarness{id: "legacy"}},
		starts:             &atomic.Int32{}, finish: make(chan bool), canceled: &atomic.Bool{},
	}}
	close(adapter.finish)
	server, _ := memoryWorker(t, adapter)
	const id = "4caba940-68a1-411a-b011-65057140c964"
	body, _ := json.Marshal(map[string]string{"id": id, "harnessId": "legacy", "name": "Work", "mode": "account"})
	if _, err := server.handle("runtime.profile.create", body); err != nil {
		t.Fatal(err)
	}
	in := protocol.SignInInput{ID: "login-first-account", HarnessID: id, Method: domain.SignInDeviceCode}
	if _, err := server.signInCommand("runtime.signin.start", in); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		status, err := server.signInCommand("runtime.signin.status", in)
		if err != nil {
			t.Fatal(err)
		}
		if status.Phase == domain.SignInCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := New(context.Background(), server.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	in.ID = "login-other-account"
	if _, err := next.signInCommand("runtime.signin.start", in); err == nil {
		t.Fatal("changing a bound account began a new authorization")
	}
}
