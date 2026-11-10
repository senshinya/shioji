package worker_test

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
	"testing"
)

type managedSkillResult struct {
	Revision string
	Entries  []struct {
		ID, Name, Scope, Directory, Path, Description string
		Enabled, CanEnable                            bool
	}
}

func writeManagedSkill(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: Review tree\n---\n\n"+body), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
func TestSkillManagementSeparatesDeviceAndProjectAndGuardsChanges(t *testing.T) {
	_, client, p := startServer(t)
	root := t.TempDir()
	projectPath := writeManagedSkill(t, filepath.Join(root, ".agents", "skills"), "tree-review", "PROJECT_SKILL_BODY")
	userPath := writeManagedSkill(t, filepath.Join(p.Home, "skills"), "global-review", "USER_SKILL_BODY")
	var w domain.Workspace
	call(t, client, "workspace.create", map[string]any{"name": "Skill project", "folders": []string{root}}, &w)
	configPath := filepath.Join(p.Home, "config.toml")
	config, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = config.WriteString("\n[[skills.config]]\nname=\"unrelated-skill\"\nenabled=false\n")
	config.Close()
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"harnessId": "codex", "workspaceId": w.ID}
	var catalog managedSkillResult
	call(t, client, "runtime.skills.list", input, &catalog)
	var projectID, userID string
	for _, entry := range catalog.Entries {
		if entry.Path == projectPath {
			projectID = entry.ID
			if entry.Scope != "project" || !entry.Enabled || !entry.CanEnable {
				t.Fatal("project skill not editable", entry)
			}
		}
		if entry.Path == userPath {
			userID = entry.ID
			if entry.Scope != "user" || entry.CanEnable {
				t.Fatal("inherited entry can mutate global configuration", entry)
			}
		}
	}
	if projectID == "" || userID == "" || catalog.Revision == "" {
		t.Fatal("missing actual offered entries", catalog)
	}
	var doc struct{ ID, Text string }
	call(t, client, "runtime.skills.read", map[string]any{"harnessId": "codex", "workspaceId": w.ID, "id": projectID}, &doc)
	if doc.ID != projectID || !strings.Contains(doc.Text, "PROJECT_SKILL_BODY") {
		t.Fatal("incorrect owning file", doc)
	}
	for _, bad := range []string{userID, "/unoffered/file"} {
		if err := client.Call(context.Background(), "runtime.skills.change", map[string]any{"harnessId": "codex", "workspaceId": w.ID, "id": bad, "revision": catalog.Revision, "enabled": false}, new(any)); err == nil {
			t.Fatal("forged/inherited mutation accepted", bad)
		}
	}
	if err := client.Call(context.Background(), "runtime.skills.read", map[string]any{"harnessId": "codex", "workspaceId": w.ID, "id": "/etc/passwd"}, new(any)); err == nil {
		t.Fatal("arbitrary file read accepted")
	}
	var device managedSkillResult
	call(t, client, "runtime.skills.list", map[string]any{"harnessId": "codex"}, &device)
	for _, entry := range device.Entries {
		if entry.Scope == "project" {
			t.Fatal("project leaked into global page", entry)
		}
	}
	call(t, client, "runtime.skills.change", map[string]any{"harnessId": "codex", "workspaceId": w.ID, "id": projectID, "revision": catalog.Revision, "enabled": false}, new(any))
	if err := client.Call(context.Background(), "runtime.skills.change", map[string]any{"harnessId": "codex", "workspaceId": w.ID, "id": projectID, "revision": catalog.Revision, "enabled": true}, new(any)); err == nil {
		t.Fatal("stale revision overwrote changed configuration")
	}
	call(t, client, "runtime.skills.list", input, &catalog)
	found := false
	for _, entry := range catalog.Entries {
		if entry.ID == projectID {
			found = true
			if entry.Enabled {
				t.Fatal("disabled native entry remained enabled")
			}
		}
	}
	if !found {
		t.Fatal("disabled entry vanished from management list")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(raw), "unrelated-skill") {
		t.Fatal("Skill change lost another native setting", err)
	}
	if err := client.Call(context.Background(), "runtime.skills.list", map[string]any{"harnessId": "codex", "workspaceId": "missing-workspace"}, new(any)); err == nil {
		t.Fatal("unknown workspace inherited device scope")
	}

	var picker struct{ Skills []struct{ Name string } }
	call(t, client, "skills.list", input, &picker)
	for _, entry := range picker.Skills {
		if entry.Name == "tree-review" {
			t.Fatal("disabled skill still offered for sending")
		}
	}
}
