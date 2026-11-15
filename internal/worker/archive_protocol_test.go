package worker_test

import (
	"context"
	"shioji/internal/domain"
	"shioji/internal/worker"
	"testing"
)

func TestArchivePolicyAndPermanentDeletion(t *testing.T) {
	_, c, _ := startServer(t)
	var policy domain.ArchivePolicy
	call(t, c, "archive.policy", nil, &policy)
	if policy.Days != 30 {
		t.Fatal("default retention", policy)
	}
	for _, days := range []int{7, 14, 30, 0} {
		call(t, c, "archive.configure", map[string]any{"days": days, "revision": policy.Revision}, &policy)
		if policy.Days != days {
			t.Fatal(policy)
		}
	}
	if err := c.Call(context.Background(), "archive.configure", map[string]int{"days": 1}, nil); err == nil {
		t.Fatal("unsupported retention accepted")
	}
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Archive", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	input := domain.Submission{OperationID: "delete-receipt", WorkspaceID: w.ID, Text: "private deletion transcript", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}
	call(t, c, "session.submit", input, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed" && d.Session.SourceTitle != ""
	})
	if err := c.Call(context.Background(), "session.delete", map[string]any{"sessionId": op.SessionID, "revision": d.Session.Revision}, nil); err == nil {
		t.Fatal("unarchived session deleted")
	}
	var archived domain.Session
	call(t, c, "session.archive", map[string]any{"sessionId": op.SessionID, "revision": d.Session.Revision, "archived": true}, &archived)
	if err := c.Call(context.Background(), "session.delete", map[string]any{"sessionId": op.SessionID, "revision": archived.Revision - 1}, nil); err == nil {
		t.Fatal("stale deletion accepted")
	}
	var deleted domain.ArchiveDeletion
	call(t, c, "session.delete", map[string]any{"sessionId": op.SessionID, "revision": archived.Revision}, &deleted)
	if deleted.DeviceID == "" || len(deleted.SessionIDs) != 1 || deleted.SessionIDs[0] != op.SessionID {
		t.Fatal("invalid deletion receipt", deleted)
	}
	var snap domain.Snapshot
	call(t, c, "snapshot", nil, &snap)
	if len(snap.Sessions) != 0 || len(snap.Operations) != 0 {
		t.Fatal("deleted catalog retained", snap)
	}
	if err := c.Call(context.Background(), "session", worker.SessionQuery{ID: op.SessionID}, nil); err == nil {
		t.Fatal("deleted history readable")
	}
	var results []domain.SearchResult
	call(t, c, "session.search", map[string]string{"query": "private deletion transcript"}, &results)
	if len(results) != 0 {
		t.Fatal("deleted history searchable", results)
	}
	if err := c.Call(context.Background(), "session.submit", input, nil); err == nil {
		t.Fatal("replayed deleted operation created session")
	}
	var aw domain.Workspace
	call(t, c, "workspace.archive", map[string]any{"workspaceId": w.ID, "revision": w.Revision, "archived": true}, &aw)
	call(t, c, "workspace.delete", map[string]any{"workspaceId": w.ID, "revision": aw.Revision}, &deleted)
	if deleted.WorkspaceID != w.ID {
		t.Fatal("wrong workspace deletion", deleted)
	}
}
