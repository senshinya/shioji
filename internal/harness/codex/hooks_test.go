package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/testutil"
)

func nativeHooksFixture(t *testing.T) (*Runtime, harness.HookProvider, string, string) {
	t.Helper()
	p := testutil.NewProvider(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(f, "hooks=true\n[projects.%q]\ntrust_level=\"trusted\"\n[unrelated_private]\nvalue=\"retained-hook-fixture-secret\"\n", root)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	writeHookFixture(t, filepath.Join(p.Home, "hooks.json"), "Stop", "true # global")
	writeHookFixture(t, filepath.Join(root, ".codex/hooks.json"), "PreToolUse", "true # project")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := Start(ctx, Options{Home: p.Home, Arguments: []string{"-C", root}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	manager, ok := any(r).(harness.HookProvider)
	if !ok {
		t.Fatal("native runtime has no hook management provider")
	}
	return r, manager, root, p.Home
}

func writeHookFixture(t *testing.T, path, event, command string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"hooks": map[string]any{event: []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func hookEntry(t *testing.T, catalog domain.HookCatalog, event string) domain.HookEntry {
	t.Helper()
	for _, entry := range catalog.Entries {
		if entry.Event == event {
			return entry
		}
	}
	t.Fatalf("missing %s hook: %+v", event, catalog)
	return domain.HookEntry{}
}

func TestNativeHookTrustKeepsDisabledStateAndRejectsChangedDefinition(t *testing.T) {
	_, manager, root, home := nativeHooksFixture(t)
	ctx := context.Background()
	launch := domain.LaunchContext{CWD: root}
	global, err := manager.Hooks(ctx, domain.LaunchContext{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(global.Entries) != 1 || global.Entries[0].Scope != domain.HookUser {
		t.Fatal("global inventory includes project hooks", global)
	}
	project, err := manager.Hooks(ctx, launch, "")
	if err != nil {
		t.Fatal(err)
	}
	p := hookEntry(t, project, "PreToolUse")
	g := hookEntry(t, project, "Stop")
	if !p.CanTrust || !p.CanEnable || p.Scope != domain.HookProject || g.CanTrust || g.CanEnable {
		t.Fatal("source ownership permits inherited edits", project)
	}
	change := domain.HookChange{ID: p.ID, Revision: project.Revision, DefinitionRevision: p.DefinitionRevision, Action: domain.HookSetEnabled, Enabled: false}
	if _, err = manager.ChangeHook(ctx, launch, change); err != nil {
		t.Fatal(err)
	}
	disabled, err := manager.Hooks(ctx, launch, "")
	if err != nil {
		t.Fatal(err)
	}
	p = hookEntry(t, disabled, "PreToolUse")
	if p.Enabled || p.Trust != domain.HookUntrusted {
		t.Fatal("disable also trusted definition", p)
	}
	change = domain.HookChange{ID: p.ID, Revision: disabled.Revision, DefinitionRevision: p.DefinitionRevision, Action: domain.HookTrustDefinition}
	if _, err = manager.ChangeHook(ctx, launch, change); err != nil {
		t.Fatal(err)
	}
	trusted, err := manager.Hooks(ctx, launch, "")
	if err != nil {
		t.Fatal(err)
	}
	approved := hookEntry(t, trusted, "PreToolUse")
	if approved.Enabled || approved.Trust != domain.HookTrusted {
		t.Fatal("trust changed enablement or failed to trust exact version", approved)
	}
	writeHookFixture(t, filepath.Join(root, ".codex/hooks.json"), "PreToolUse", "true # revised project")
	if _, err = manager.ChangeHook(ctx, launch, change); err == nil {
		t.Fatal("stale approval trusted revised definition")
	}
	modified, err := manager.Hooks(ctx, launch, "")
	if err != nil {
		t.Fatal(err)
	}
	current := hookEntry(t, modified, "PreToolUse")
	if current.Trust != domain.HookModified || current.DefinitionRevision == approved.DefinitionRevision {
		t.Fatal("changed definition retained approval", current)
	}
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || !strings.Contains(string(raw), "retained-hook-fixture-secret") {
		t.Fatal("hook write lost unrelated private setting", err)
	}
}

func TestNativeHookInventoryDoesNotCreateOrResumeThread(t *testing.T) {
	r, manager, root, _ := nativeHooksFixture(t)
	ctx := context.Background()
	var before, after struct{ Data []string }
	if err := r.call(ctx, "thread/loaded/list", map[string]any{}, &before); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Hooks(ctx, domain.LaunchContext{CWD: root}, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.call(ctx, "thread/loaded/list", map[string]any{}, &after); err != nil {
		t.Fatal(err)
	}
	if len(before.Data) != 0 || len(after.Data) != 0 {
		t.Fatal("inventory created a runtime thread", before, after)
	}
}

func TestNativeGlobalHooksDoNotDependOnCatalogProcessProject(t *testing.T) {
	_, manager, root, _ := nativeHooksFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".codex/config.toml"), []byte("this is not valid TOML ["), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.Hooks(context.Background(), domain.LaunchContext{}, "")
	if err != nil || len(catalog.Entries) != 1 || catalog.Entries[0].Event != "Stop" {
		t.Fatal("global inventory inherited the catalog process's project", catalog, err)
	}
}

func TestNativeHookRefreshAdoptsIndependentProcessWithoutReplayingThread(t *testing.T) {
	_, first, root, home := nativeHooksFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := Start(ctx, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	second := any(r).(harness.HookProvider)
	var created struct{ Thread struct{ ID string } }
	if err = r.call(ctx, "thread/start", map[string]any{"cwd": root, "model": "fixture-model", "approvalPolicy": "never", "sandbox": "read-only"}, &created); err != nil {
		t.Fatal(err)
	}
	launch := domain.LaunchContext{CWD: root}
	catalog, err := first.Hooks(ctx, launch, "")
	if err != nil {
		t.Fatal(err)
	}
	h := hookEntry(t, catalog, "PreToolUse")
	if _, err = first.ChangeHook(ctx, launch, domain.HookChange{ID: h.ID, Revision: catalog.Revision, DefinitionRevision: h.DefinitionRevision, Action: domain.HookTrustDefinition}); err != nil {
		t.Fatal(err)
	}
	before, err := second.Hooks(ctx, launch, created.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hookEntry(t, before, "PreToolUse").Trust != domain.HookUntrusted {
		t.Fatal("fixture did not retain independent configuration", before)
	}
	path := filepath.Join(home, "config.toml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = second.RefreshHooks(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := second.Hooks(ctx, launch, created.Thread.ID)
	if err != nil || !after.RuntimeKnown || hookEntry(t, after, "PreToolUse").Trust != domain.HookTrusted {
		t.Fatal("native process did not adopt saved hook trust", after, err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(config) {
		t.Fatal("configuration refresh rewrote user's file", err)
	}
	var loaded struct{ Data []string }
	if err = r.call(ctx, "thread/loaded/list", map[string]any{}, &loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Data) != 1 || loaded.Data[0] != created.Thread.ID {
		t.Fatal("refresh started or replayed another thread", loaded)
	}
}

func TestNativeUnsupportedHookNeverBecomesTrustable(t *testing.T) {
	_, manager, root, _ := nativeHooksFixture(t)
	path := filepath.Join(root, ".codex/hooks.json")
	raw := []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"prompt","prompt":"inspect only"}]}]}}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.Hooks(context.Background(), domain.LaunchContext{CWD: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	var entry *domain.HookEntry
	for i := range catalog.Entries {
		if catalog.Entries[i].Scope == domain.HookProject {
			entry = &catalog.Entries[i]
			break
		}
	}
	change := domain.HookChange{ID: "unavailable-handler", Revision: catalog.Revision, DefinitionRevision: "unavailable", Action: domain.HookTrustDefinition}
	if entry == nil {
		// Current native engines explicitly omit unsupported prompt handlers.
		if len(catalog.Warnings) == 0 {
			t.Fatal("unsupported definition disappeared without a warning")
		}
	} else {
		if entry.Kind != "unknown" || entry.CanEnable || entry.CanTrust || entry.ReadOnlyReason == "" {
			t.Fatal("incomplete definition can be authorized", entry)
		}
		change.ID, change.DefinitionRevision = entry.ID, entry.DefinitionRevision
	}
	if _, err = manager.ChangeHook(context.Background(), domain.LaunchContext{CWD: root}, change); err == nil {
		t.Fatal("unsupported handler was trusted without review contents")
	}
}
