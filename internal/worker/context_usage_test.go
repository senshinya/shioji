package worker_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestRealWorkerPersistsLiveContextApartFromTurnBilling(t *testing.T) {
	_, c, _ := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", map[string]any{"name": "Context", "folders": []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "context-real", WorkspaceID: w.ID, Text: "reply briefly", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", map[string]any{"id": op.SessionID}, &detail)
		return detail.Session.Status == "completed" || detail.Session.Status == "failed" || detail.Session.Status == "unknown"
	})
	usage := detail.Session.ContextUsage
	if usage == nil || usage.UsedTokens == nil || *usage.UsedTokens != 16 || usage.LimitTokens == nil || *usage.LimitTokens <= 0 || detail.Session.Status != "completed" {
		t.Fatal("real adapter report lost or changed execution", detail.Session)
	}
	if len(detail.Turns) != 1 || len(detail.Items) != 2 {
		t.Fatal("context update created conversation content", detail)
	}
}

func TestRealWorkerReportsContextAgainAfterHistoryEdit(t *testing.T) {
	_, c, _ := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", map[string]any{"name": "Edited context", "folders": []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "before-edit", WorkspaceID: w.ID, Text: "reply briefly", Config: cfg}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", map[string]any{"id": op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	oldThread := d.Session.ThreadID
	oldTurn := d.Turns[0].ID
	var revised domain.HistoryOperation
	call(t, c, "session.restart", domain.HistoryChange{OperationID: "context-edit", SessionID: op.SessionID, Revision: d.Session.Revision, ItemID: "input:before-edit", Restart: true, Text: "revised reply", Config: cfg}, &revised)
	wait(t, func() bool {
		call(t, c, "session", map[string]any{"id": op.SessionID}, &d)
		return d.Session.ThreadID != oldThread && d.Session.Status == "completed"
	})
	usage := d.Session.ContextUsage
	if usage == nil || usage.TurnID == oldTurn || usage.UsedTokens == nil || *usage.UsedTokens != 16 || usage.LimitTokens == nil || *usage.LimitTokens <= 0 {
		t.Fatal("edited session did not receive a fresh context report", d.Session)
	}
	if d.Session.ID != op.SessionID || len(d.Turns) != 1 || usage.TurnID != d.Turns[0].ID {
		t.Fatal("context report belongs to the wrong execution", d)
	}
}
