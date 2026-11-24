package antigravity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestSharedUserSkillsRequireExplicitConfiguration(t *testing.T) {
	for _, source := range []string{"manifest", "standard-directory-link"} {
		t.Run(source, func(t *testing.T) {
			home, profile, project := t.TempDir(), t.TempDir(), t.TempDir()
			shared := filepath.Join(home, ".agents", "skills")
			sharedFile := filepath.Join(shared, "shared", "SKILL.md")
			writeMCPFixture(t, sharedFile, "---\nname: shared\n---\nShared user instructions")
			foreign := filepath.Join(home, "unrelated")
			writeMCPFixture(t, filepath.Join(foreign, "foreign", "SKILL.md"), "---\nname: foreign\n---\nOutside skill roots")
			writeMCPFixture(t, filepath.Join(project, ".agents", "skills.json"), skillEntries(t, shared))
			if err := os.Symlink(foreign, filepath.Join(profile, "skills")); err != nil {
				t.Fatal(err)
			}

			launch := domain.LaunchContext{CWD: project}
			r := &runtime{skillHome: home, skillProfile: profile, session: domain.Session{Context: launch}}
			before, err := r.Skills(t.Context(), launch)
			if err != nil || len(before.Skills) != 0 {
				t.Fatalf("unconfigured shared or foreign skills = %+v, %v", before, err)
			}

			switch source {
			case "manifest":
				writeMCPFixture(t, filepath.Join(profile, "config", "skills.json"), skillEntries(t, shared, foreign))
			case "standard-directory-link":
				if err := os.Mkdir(filepath.Join(profile, "config"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(shared, filepath.Join(profile, "config", "skills")); err != nil {
					t.Fatal(err)
				}
			}
			got, err := r.SkillInventory(t.Context(), launch)
			if err != nil || len(got.Entries) != 1 {
				t.Fatalf("configured inventory = %+v, %v", got, err)
			}
			entry := got.Entries[0]
			if !entry.Enabled || entry.Scope != domain.SkillUser || entry.Path != sharedFile {
				t.Fatalf("unexpected shared source: %+v", entry)
			}
			blocks, err := r.skillBlocks(t.Context(), []domain.SkillReference{entry.SkillReference})
			if err != nil || len(blocks) != 1 || !strings.Contains(blocks[0].Text, "Shared user instructions") {
				t.Fatalf("shared skill input = %+v, %v", blocks, err)
			}
			if source == "standard-directory-link" {
				writeMCPFixture(t, filepath.Join(profile, "config", "skills.json"), `{"exclude":["skills/shared"]}`)
				catalog, err := r.Skills(t.Context(), launch)
				if err != nil || len(catalog.Skills) != 0 {
					t.Fatalf("linked skill ignored its configured exclusion: %+v, %v", catalog, err)
				}
			}
		})
	}
}

func TestSharedUserSkillRootCannotLinkOutsideHome(t *testing.T) {
	home, profile, outside := t.TempDir(), t.TempDir(), t.TempDir()
	shared := filepath.Join(home, ".agents", "skills")
	writeMCPFixture(t, filepath.Join(outside, "foreign", "SKILL.md"), "---\nname: foreign\n---\nOutside home")
	if err := os.Mkdir(filepath.Dir(shared), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, shared); err != nil {
		t.Fatal(err)
	}
	writeMCPFixture(t, filepath.Join(profile, "config", "skills.json"), skillEntries(t, shared))
	r := &runtime{skillHome: home, skillProfile: profile}
	got, err := r.Skills(t.Context(), domain.LaunchContext{})
	if err != nil || len(got.Skills) != 0 {
		t.Fatalf("shared root escaped home: %+v, %v", got, err)
	}
}

func TestSharedDirectoryLinkToAGYDoesNotReplaceStandardSource(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".gemini")
	standard := filepath.Join(profile, "config", "skills")
	file := filepath.Join(standard, "review", "SKILL.md")
	writeMCPFixture(t, file, "---\nname: review\n---\nStandard user skill")
	if err := os.Mkdir(filepath.Join(home, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(standard, filepath.Join(home, ".agents", "skills")); err != nil {
		t.Fatal(err)
	}
	r := &runtime{skillHome: home, skillProfile: profile}
	got, err := r.SkillInventory(t.Context(), domain.LaunchContext{})
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Path != file {
		t.Fatalf("shared alias changed the standard source: %+v, %v", got, err)
	}
}

func skillEntries(t *testing.T, paths ...string) string {
	t.Helper()
	entries := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, map[string]string{"path": path})
	}
	data, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSkillCatalogScansLegacyGeminiDirectories(t *testing.T) {
	project, extra := t.TempDir(), t.TempDir()
	native := filepath.Join(project, ".agents", "skills", "review", "SKILL.md")
	writeMCPFixture(t, native, "---\nname: review\n---\nNative instructions")
	writeMCPFixture(t, filepath.Join(project, ".gemini", "skills", "review", "SKILL.md"), "---\nname: review\n---\nLegacy duplicate")
	legacy := filepath.Join(project, ".gemini", "skills", "legacy", "SKILL.md")
	writeMCPFixture(t, legacy, "---\nname: legacy\n---\nLegacy instructions")
	writeMCPFixture(t, filepath.Join(extra, ".gemini", "skills", "extra", "SKILL.md"), "---\nname: extra\n---\nAdditional directory")

	launch := domain.LaunchContext{CWD: project, AdditionalDirectories: []string{extra}}
	r := &runtime{session: domain.Session{Context: launch}}
	inventory, err := r.SkillInventory(t.Context(), launch)
	if err != nil {
		t.Fatal(err)
	}
	var enabled []string
	for _, entry := range inventory.Entries {
		if !entry.Enabled {
			continue
		}
		if entry.Scope != domain.SkillProject || entry.Name == "review" && entry.Path != native {
			t.Fatalf("unexpected selected source: %+v", entry)
		}
		enabled = append(enabled, entry.Name)
	}
	slices.Sort(enabled)
	if !slices.Equal(enabled, []string{"extra", "legacy", "review"}) {
		t.Fatalf("enabled skills = %v", enabled)
	}
	for _, entry := range inventory.Entries {
		if entry.Path != legacy {
			continue
		}
		blocks, err := r.skillBlocks(t.Context(), []domain.SkillReference{entry.SkillReference})
		if err != nil || len(blocks) != 1 || !strings.Contains(blocks[0].Text, "Legacy instructions") {
			t.Fatalf("legacy skill input = %+v, %v", blocks, err)
		}
	}
}

func TestSkillsBrowseAndReferenceTheSelectedDeviceSource(t *testing.T) {
	profile, project, other := t.TempDir(), t.TempDir(), t.TempDir()
	create := func(root, rel, text string) string {
		t.Helper()
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "SKILL.md")
		document := "---\nname: review\ndescription: Review a change\n---\n" + text
		if err := os.WriteFile(path, []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	global := create(profile, "antigravity-cli/skills/review", "global instructions")
	create(project, ".agents/skills/review", "project instructions")
	create(other, "other", "outside the workspace")
	if err := os.Symlink(filepath.Join(other, "other"), filepath.Join(project, ".agents/skills/foreign")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	launch := domain.LaunchContext{CWD: project}
	r := &runtime{skillProfile: profile, session: domain.Session{Context: launch}}
	r.native.AgentCapabilities.PromptCapabilities.EmbeddedContext = true
	inventory, err := r.SkillInventory(ctx, launch)
	if err != nil || len(inventory.Entries) != 2 || inventory.Entries[0].CanEnable || inventory.Entries[0].Path != global {
		t.Fatalf("inventory: %+v, %v", inventory, err)
	}
	catalog, err := r.Skills(ctx, launch)
	if err != nil || len(catalog.Skills) != 1 || catalog.Skills[0].ID != inventory.Entries[0].ID {
		t.Fatalf("composer catalog: %+v, %v", catalog, err)
	}
	selected := catalog.Skills[0].SkillReference
	document, err := r.ReadSkill(ctx, launch, selected.ID)
	if err != nil || !strings.Contains(document.Text, "global instructions") {
		t.Fatalf("skill document: %+v, %v", document, err)
	}
	blocks, err := r.promptContent(ctx, harness.TurnInput{Skills: []domain.SkillReference{selected}})
	if err != nil || len(blocks) != 1 || blocks[0].Resource == nil || !strings.Contains(blocks[0].Resource.Text, "global instructions") {
		t.Fatalf("prompt didn't preserve the selected source: %+v, %v", blocks, err)
	}
	if err := os.WriteFile(global, []byte(document.Text+" replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.promptContent(ctx, harness.TurnInput{Skills: []domain.SkillReference{selected}}); err == nil {
		t.Fatal("an edited source silently replaced the selected instructions")
	}
}

func TestNativeSkillManifestProjectsInheritedAndFilteredEntries(t *testing.T) {
	profile, project, foreign := t.TempDir(), t.TempDir(), t.TempDir()
	writeMCPFixture(t, filepath.Join(project, ".agents", "skills.json"), `{"inherits":[{"path":"shared/manifest.json","include_only":["shared/skills/allowed"]}],"entries":[{"path":"`+foreign+`"}]}`)
	writeMCPFixture(t, filepath.Join(project, "shared", "manifest.json"), `{"entries":[{"path":"shared/skills"}]}`)
	for _, item := range []string{"allowed", "excluded"} {
		writeMCPFixture(t, filepath.Join(project, "shared", "skills", item, "SKILL.md"), "---\nname: "+item+"\n---\n"+item)
	}
	writeMCPFixture(t, filepath.Join(foreign, "foreign", "SKILL.md"), "---\nname: foreign\n---\nprivate")
	writeMCPFixture(t, filepath.Join(profile, "config", "plugins", "enabled", "skills", "plug", "SKILL.md"), "---\nname: plug\n---\nplugin")
	writeMCPFixture(t, filepath.Join(profile, "config", "plugins", "disabled", "skills", "hidden", "SKILL.md"), "---\nname: hidden\n---\ndisabled")
	writeMCPFixture(t, filepath.Join(profile, "config", "plugins", "enabled", "plugin.json"), `{"name":"enabled"}`)
	writeMCPFixture(t, filepath.Join(profile, "config", "plugins", "disabled", "plugin.json"), `{"name":"disabled"}`)
	writeMCPFixture(t, filepath.Join(profile, "config", "plugins", "stray", "skills", "stray", "SKILL.md"), "---\nname: stray\n---\nnot installed")
	writeMCPFixture(t, filepath.Join(profile, "config", "config.json"), `{"plugins":{"disabled":{"enabled":false}}}`)
	r := &runtime{skillProfile: profile}
	inv, err := r.SkillInventory(context.Background(), domain.LaunchContext{CWD: project})
	if err != nil || len(inv.Entries) != 2 || inv.Entries[0].Name != "allowed" || inv.Entries[1].Name != "plug" || len(inv.Warnings) == 0 {
		t.Fatalf("wrong native Skill projection: %+v %v", inv, err)
	}
}

func TestNativeSkillExclusionMatchesManifestRelativePath(t *testing.T) {
	project := t.TempDir()
	writeMCPFixture(t, filepath.Join(project, ".agents", "skills.json"),
		`{"entries":[{"path":"shared/skills"}],"exclude":["shared/skills/hidden"]}`)
	writeMCPFixture(t, filepath.Join(project, "shared", "skills", "hidden", "SKILL.md"), "---\nname: differently-named\n---\ninstructions")
	r := &runtime{}
	got, err := r.SkillInventory(context.Background(), domain.LaunchContext{CWD: project})
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Enabled {
		t.Fatalf("native configuration exclusion did not apply: %+v %v", got, err)
	}
}
