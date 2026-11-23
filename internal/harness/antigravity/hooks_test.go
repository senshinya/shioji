package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestNativeHookToggleUpdatesTheNamedDefinitionWithinItsOwningScope(t *testing.T) {
	profile, project := t.TempDir(), t.TempDir()
	globalPath := filepath.Join(profile, "config", "hooks.json")
	projectPath := filepath.Join(project, "_agents", "hooks.json")
	global := `{"one":{"enabled":false,"PreInvocation":[{"command":"global-before"}],"Stop":[{"type":"command","command":"global-after","timeout":5}],"future":{"keep":true}}}`
	local := `{"one":{"Stop":[{"type":"command","command":"local"}]}}`
	writeMCPFixture(t, globalPath, global)
	writeMCPFixture(t, projectPath, local)
	r := &runtime{skillProfile: profile}
	provider, ok := any(r).(harness.HookProvider)
	if !ok {
		t.Fatal("Antigravity does not expose its Hooks configuration")
	}
	ctx := context.Background()
	launch := domain.LaunchContext{CWD: project}
	all, err := provider.Hooks(ctx, launch, "")
	if err != nil || all.RuntimeKnown || len(all.Entries) != 2 {
		t.Fatalf("native Hook sources: %+v, %v", all, err)
	}
	device, err := provider.Hooks(ctx, domain.LaunchContext{}, "")
	if err != nil || len(device.Entries) != 1 {
		t.Fatalf("global Hook list: %+v, %v", device, err)
	}
	one := device.Entries[0]
	if one.Enabled || !one.CanEnable || one.CanTrust || one.Scope != domain.HookUser || one.SourcePath != globalPath || all.Entries[0].ID == all.Entries[1].ID {
		t.Fatalf("incorrect named Hook: %+v", one)
	}
	change := domain.HookChange{Action: domain.HookSetEnabled, Enabled: true, ID: one.ID, Revision: device.Revision, DefinitionRevision: one.DefinitionRevision}
	wrongScope := change
	wrongScope.Revision = all.Revision
	if _, err := provider.ChangeHook(ctx, launch, wrongScope); err == nil {
		t.Fatal("project page accepted a device edit")
	}
	result, err := provider.ChangeHook(ctx, domain.LaunchContext{}, change)
	if err != nil || !result.Saved || len(result.Catalog.Entries) != 1 || !change.Observed(result.Catalog) {
		t.Fatalf("toggle could not be reconciled by retained identity: %+v, %v", result, err)
	}
	if _, err := provider.ChangeHook(ctx, domain.LaunchContext{}, change); err == nil {
		t.Fatal("stale Hook revision wrote the configuration")
	}
	after, err := os.ReadFile(projectPath)
	if err != nil || string(after) != local {
		t.Fatal("device edit changed the workspace file")
	}
	raw, err := os.ReadFile(globalPath)
	if err != nil || !containsAll(string(raw), `"enabled": true`, `"future"`, "global-before", "global-after") {
		t.Fatalf("native Hook data was lost: %s, %v", raw, err)
	}
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}

func TestNativeHookWithUnreviewableCommandCannotBeEnabled(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "config", "hooks.json")
	writeMCPFixture(t, path, `{"large":{"enabled":false,"Stop":[{"command":"`+strings.Repeat("x", 33<<10)+`"}]}}`)
	r := &runtime{skillProfile: profile}
	catalog, err := r.Hooks(context.Background(), domain.LaunchContext{}, "")
	if err != nil || len(catalog.Entries) != 1 {
		t.Fatalf("native hook catalog: %+v %v", catalog, err)
	}
	hook := catalog.Entries[0]
	if hook.CanEnable || hook.ReadOnlyReason == "" {
		t.Fatal("only part of the configuration can be reviewed, but the whole Hook could be enabled")
	}
	_, err = r.ChangeHook(context.Background(), domain.LaunchContext{}, domain.HookChange{
		Action: domain.HookSetEnabled, ID: hook.ID, Enabled: true, Revision: catalog.Revision, DefinitionRevision: hook.DefinitionRevision})
	if err == nil {
		t.Fatal("unreviewable command was written through a direct worker call")
	}
}

func TestProjectNativeConfigParentsStayWithinAcceptedRoots(t *testing.T) {
	repo := t.TempDir()
	parent := filepath.Join(repo, "services")
	working := filepath.Join(parent, "api")
	for _, dir := range []string{working, filepath.Join(repo, ".git")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	launch := domain.LaunchContext{CWD: working}
	if got := projectNativeConfigBases(launch); !slices.Equal(got, []string{working}) {
		t.Fatalf("scanned a Git ancestor outside the accepted directory: %v", got)
	}
	launch.AdditionalDirectories = []string{repo}
	if got := projectNativeConfigBases(launch); !slices.Equal(got, []string{working, parent, repo}) {
		t.Fatalf("missed the accepted Git parent ancestry: %v", got)
	}
}
