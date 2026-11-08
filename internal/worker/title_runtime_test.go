package worker_test

import (
	"shioji/internal/domain"
	"strings"
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

func TestRealWorkerRegeneratesTitleAfterEditingFirstMessage(t *testing.T) {
	_, c, p := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", map[string]any{"name": "Retitle", "folders": []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "retitle-source", WorkspaceID: w.ID, Text: "解释红黑树旋转", Config: cfg}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", map[string]any{"id": op.SessionID}, &d)
		return d.Session.Status == "completed" && d.Session.SourceTitle != ""
	})
	oldRevision := d.Session.TitleRevision
	var edited domain.HistoryOperation
	call(t, c, "session.restart", domain.HistoryChange{OperationID: "retitle-edit", SessionID: op.SessionID, Revision: d.Session.Revision, ItemID: "input:retitle-source", Restart: true, Text: "解释归并排序", Config: cfg}, &edited)
	wait(t, func() bool {
		call(t, c, "session", map[string]any{"id": op.SessionID}, &d)
		return d.Session.Status == "completed" && d.Session.SourceTitle != "" && d.Session.TitleRevision > oldRevision+1 && len(p.TitleInputs()) == 2
	})
	titles := p.TitleInputs()
	if len(titles) != 2 || !strings.Contains(titles[1], "解释归并排序") || strings.Contains(titles[1], "解释红黑树旋转") {
		t.Fatal("naming was not regenerated from the edited input", "title requests", len(titles))
	}
	if len(d.Items) != 2 || len(d.Turns) != 1 || len(p.Inputs()) != 2 {
		t.Fatal("renaming added a turn or replayed history", d)
	}
}
