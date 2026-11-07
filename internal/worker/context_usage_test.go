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
