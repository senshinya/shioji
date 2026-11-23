package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

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
