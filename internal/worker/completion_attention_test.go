package worker_test

import (
	"context"
	"encoding/json"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/worker"
)

func TestCompletionReadSynchronizesClientsAndCannotConsumeNewReply(t *testing.T) {
	server, a, _ := startServer(t)
	b := connect(t, server.Directory())
	var ready domain.Readiness
	call(t, a, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, a, "workspace.create", worker.WorkspaceInput{Name: "Attention", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, a, "session.submit", domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "reply", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, b, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return detail.Session.Status == "completed"
	})
	// Inspect the public projection before the new types exist (RED).
	completion := func(s domain.Session) map[string]any {
		raw, _ := json.Marshal(s)
		var body map[string]any
		json.Unmarshal(raw, &body)
		value, _ := body["completion"].(map[string]any)
		return value
	}
	first := completion(detail.Session)
	if first["id"] == nil || first["unread"] != true || first["completedAt"] == nil {
		t.Fatalf("worker did not persist a completion: %v", first)
	}
	var before domain.Snapshot
	call(t, b, "snapshot", nil, &before)
	input := map[string]any{"sessionId": op.SessionID, "completionId": first["id"]}
	var read domain.Session
	call(t, a, "session.completion.read", input, &read)
	if completion(read)["unread"] == true {
		t.Fatal("read acknowledgement stayed unread")
	}
	var events protocol.EventPage
	call(t, b, "events", protocol.EventsQuery{After: before.Sequence, Generation: before.Generation}, &events)
	found := false
	for _, event := range events.Events {
		if event.SessionID == op.SessionID && event.Session != nil && completion(*event.Session)["id"] == first["id"] && completion(*event.Session)["unread"] == false {
			found = true
		}
	}
	if !found {
		t.Fatal("other client received no durable read event")
	}
	call(t, a, "session.completion.read", input, &read) // idempotent
	var second domain.Operation
	call(t, a, "session.submit", domain.Submission{OperationID: "second", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "again", Config: detail.Session.Config}, &second)
	wait(t, func() bool {
		call(t, b, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return detail.Session.Status == "completed" && completion(detail.Session)["id"] != first["id"]
	})
	call(t, a, "session.completion.read", input, &read)
	if completion(read)["unread"] != true {
		t.Fatal("stale read consumed the newer completion")
	}
	var snapshot domain.Snapshot
	call(t, b, "snapshot", nil, &snapshot)
	if len(snapshot.Sessions) != 1 || completion(snapshot.Sessions[0])["unread"] != true {
		t.Fatal("snapshot lost latest completion")
	}
	if err := a.Call(context.Background(), "session.completion.read", map[string]any{"sessionId": op.SessionID}, nil); err == nil {
		t.Fatal("empty completion ID accepted")
	}
}
