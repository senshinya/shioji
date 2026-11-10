package worker_test

import (
	"reflect"
	"shioji/internal/domain"
	"testing"
)

func TestRealWorkerCompactionKeepsSessionAndInputHistory(t *testing.T) {
	_, c, _ := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", map[string]any{"name": "Compaction", "folders": []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	if !ready.Capabilities.Compact {
		t.Fatal("installed source did not expose compaction")
	}
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "before-compact", WorkspaceID: w.ID, Text: "keep this original input", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	var before domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", map[string]any{"id": op.SessionID}, &before)
		return before.Session.Status == "completed"
	})
	in := domain.ContextActionRequest{OperationID: "compact-real", SessionID: op.SessionID, Revision: before.Session.Revision}
	var result domain.ContextActionResult
	call(t, c, "session.context.compact", in, &result)
	if result.Operation.ID != in.OperationID || result.Operation.Kind != "compact" || result.Session.ID != op.SessionID || result.Session.ContextAction == nil {
		t.Fatal("missing authoritative acceptance", result)
	}
	var after domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", map[string]any{"id": op.SessionID}, &after)
		return after.Session.ContextAction != nil && after.Session.ContextAction.State == "completed"
	})
	if after.Session.Status != "completed" || after.Session.ThreadID != before.Session.ThreadID || !reflect.DeepEqual(after.Session.Config, before.Session.Config) || !reflect.DeepEqual(after.Session.Context, before.Session.Context) {
		t.Fatal("compaction changed execution context", before.Session, after.Session)
	}
	users, compactItems, commands := 0, 0, 0
	for _, item := range after.Items {
		if item.Kind == "command" {
			commands++
			if item.TurnID != after.Session.ContextAction.TurnID || item.Name != "压缩上下文" {
				t.Fatal("command lost its source boundary", item)
			}
		}
		if item.Kind == "user" {
			users++
			if item.Text != "keep this original input" {
				t.Fatal("compaction fabricated user input", item)
			}
		}
		if item.Presentation == "compaction" && item.Final {
			compactItems++
		}
	}
	if users != 1 || compactItems != 1 || commands != 1 || len(after.Turns) != len(before.Turns)+1 || after.Turns[len(after.Turns)-1].Presentation != "compaction" {
		t.Fatal("real source lifecycle was not preserved", after)
	}
	call(t, c, "session.context.compact", in, &result)
	call(t, c, "session", map[string]any{"id": op.SessionID}, &after)
	if result.Operation.State != "confirmed" || len(after.Turns) != len(before.Turns)+1 {
		t.Fatal("replayed operation created another source turn", result, after.Turns)
	}
}
