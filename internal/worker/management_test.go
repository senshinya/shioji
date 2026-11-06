package worker_test

import (
	"context"
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/worker"
	"testing"
)

func TestSessionManagementRenamesArchivesAndRestoresWithoutLosingHistory(t *testing.T) {
	server, c, _ := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Manage", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "manage", WorkspaceID: w.ID, Text: "管理验收", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	count := len(d.Items)
	var renamed domain.Session
	call(t, c, "session.rename", map[string]any{"sessionId": op.SessionID, "revision": d.Session.Revision, "title": "手动会话名称"}, &renamed)
	if renamed.Title != "手动会话名称" {
		t.Fatal("rename not applied", renamed)
	}
	if err := c.Call(context.Background(), "session.rename", map[string]any{"sessionId": op.SessionID, "revision": d.Session.Revision, "title": "stale"}, nil); err == nil {
		t.Fatal("stale rename overwrote latest title")
	}
	var archived domain.Session
	call(t, c, "session.archive", map[string]any{"sessionId": op.SessionID, "revision": renamed.Revision, "archived": true}, &archived)
	raw, _ := json.Marshal(archived)
	if !jsonHasTrue(raw, "archived") {
		t.Fatal("session was not archived", string(raw))
	}
	if err := c.Call(context.Background(), "session.submit", domain.Submission{OperationID: "archived-send", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "must not run", Config: renamed.Config}, nil); err == nil {
		t.Fatal("archived session accepted input")
	}
	var restored domain.Session
	call(t, c, "session.archive", map[string]any{"sessionId": op.SessionID, "revision": archived.Revision, "archived": false}, &restored)
	call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
	if len(d.Items) != count || d.Session.Title != "手动会话名称" {
		t.Fatal("management changed conversation", d.Session, len(d.Items), count)
	}
	_ = server
}
func jsonHasTrue(raw []byte, key string) bool {
	var object map[string]any
	json.Unmarshal(raw, &object)
	return object[key] == true
}

func TestWorkspaceArchiveRejectsActiveExecutionAndBlocksNewSessions(t *testing.T) {
	_, c, p := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Archive", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "active-archive", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	<-p.Started
	if err := c.Call(context.Background(), "workspace.archive", map[string]any{"workspaceId": w.ID, "revision": w.Revision, "archived": true}, nil); err == nil {
		t.Fatal("active workspace archived")
	}
	close(p.Release)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	var archived domain.Workspace
	call(t, c, "workspace.archive", map[string]any{"workspaceId": w.ID, "revision": w.Revision, "archived": true}, &archived)
	if err := c.Call(context.Background(), "session.submit", domain.Submission{OperationID: "new-in-archive", WorkspaceID: w.ID, Text: "must not create", Config: d.Session.Config}, nil); err == nil {
		t.Fatal("archived workspace accepted new session")
	}
	var restored domain.Workspace
	call(t, c, "workspace.archive", map[string]any{"workspaceId": w.ID, "revision": archived.Revision, "archived": false}, &restored)
	if restored.ID != w.ID {
		t.Fatal("restore changed identity")
	}
}
