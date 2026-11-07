package worker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
)

func TestSkillCatalogUsesOwningContextAndRejectsForgedReference(t *testing.T) {
	_, client, _ := startServer(t)
	root := t.TempDir()
	skill := filepath.Join(root, ".agents", "skills", "review-tree")
	if err := os.MkdirAll(skill, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: review-tree\ndescription: Review tree rotations\n---\n\nCheck local tree invariants.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var workspace struct{ ID string }
	call(t, client, "workspace.create", map[string]any{"name": "Skills", "folders": []string{root}}, &workspace)
	var catalog struct {
		Skills []struct{ ID, Name, Description string }
	}
	if err := client.Call(context.Background(), "skills.list", map[string]any{"workspaceId": workspace.ID, "harnessId": "codex"}, &catalog); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range catalog.Skills {
		if s.Name == "review-tree" {
			found = s.ID != "" && s.Description != ""
		}
	}
	if !found {
		t.Fatal("local skill was not discovered by owning harness", catalog)
	}
	var ready struct{ Models []struct{ ID string } }
	call(t, client, "readiness", nil, &ready)
	input := map[string]any{"workspaceId": workspace.ID, "operationId": "forged-skill", "text": "review", "config": map[string]any{"model": ready.Models[0].ID, "permission": "read-only"}, "skills": []map[string]string{{"id": "/unoffered/secret", "name": "review-tree"}}}
	if err := client.Call(context.Background(), "session.submit", input, new(json.RawMessage)); err == nil {
		t.Fatal("forged skill reference was accepted")
	}
}

func TestRealCodexReceivesStructuredSkillAndKeepsReferenceInHistory(t *testing.T) {
	_, client, provider := startServer(t)
	root := t.TempDir()
	path := filepath.Join(root, ".agents", "skills", "review-tree")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: review-tree\ndescription: Review tree rotations\n---\n\nCheck ROTATION_SKILL_8C2 invariants.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var w domain.Workspace
	call(t, client, "workspace.create", protocol.WorkspaceInput{Name: "Skills", Folders: []string{root}}, &w)
	var catalog domain.SkillCatalog
	call(t, client, "skills.list", protocol.SkillQuery{GitQuery: protocol.GitQuery{WorkspaceID: w.ID}, HarnessID: "codex"}, &catalog)
	var ref domain.SkillReference
	for _, skill := range catalog.Skills {
		if skill.Name == "review-tree" {
			ref = skill.SkillReference
		}
	}
	if ref.ID == "" {
		t.Fatal("skill missing")
	}
	var ready domain.Readiness
	call(t, client, "readiness", nil, &ready)
	var op domain.Operation
	call(t, client, "session.submit", domain.Submission{OperationID: "skill-input", WorkspaceID: w.ID, Skills: []domain.SkillReference{ref}, Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, client, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed" || d.Session.Status == "failed"
	})
	if d.Session.Status != "completed" {
		t.Fatal("skill turn failed", d)
	}
	if len(d.Items) == 0 || len(d.Items[0].Skills) != 1 || d.Items[0].Skills[0] != ref {
		t.Fatal("accepted structured skill was lost", d.Items)
	}
	if !strings.Contains(strings.Join(provider.Inputs(), "\n"), "ROTATION_SKILL_8C2") {
		t.Fatal("actual model input did not include selected Skill instructions", provider.Inputs())
	}
}
