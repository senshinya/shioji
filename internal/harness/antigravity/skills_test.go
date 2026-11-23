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
