package worker_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestRealWorkerGeneratesAutomaticTitleWithoutAddingATurn(t *testing.T) {
	_, c, p := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", map[string]any{"name": "Title", "folders": []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	in := domain.Submission{OperationID: "title-real", WorkspaceID: w.ID, Text: "解释红黑树旋转", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}
	var op domain.Operation
	call(t, c, "session.submit", in, &op)
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", map[string]any{"id": op.SessionID}, &detail)
		return detail.Session.Title == "夹具会话标题" && detail.Session.Status == "completed"
	})
	if len(detail.Items) != 2 || len(detail.Turns) != 1 || len(p.Inputs()) != 1 || len(p.TitleInputs()) != 1 {
		t.Fatal("title request changed normal conversation delivery", len(detail.Items), len(detail.Turns), len(p.Inputs()), len(p.TitleInputs()))
	}
}
