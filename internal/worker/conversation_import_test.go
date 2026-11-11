package worker_test

import (
	"context"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
)

func TestConversationImportRPCAndFirstInputUseIndependentSourcePrefix(t *testing.T) {
	_, client, provider := startServer(t)
	var w domain.Workspace
	call(t, client, "workspace.create", protocol.WorkspaceInput{Name: "Import", Folders: []string{t.TempDir()}}, &w)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	done := make(chan struct{}, 4)
	external, err := codex.Start(ctx, codex.Options{Home: provider.Home, OnEvent: func(e domain.Event) error {
		if e.Turn != nil && e.Turn.Status == "completed" {
			done <- struct{}{}
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	source := domain.Session{ID: "external", Context: domain.LaunchContext{CWD: w.Folders[0]}, Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"}}
	source.ThreadID, err = external.Create(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = external.Submit(ctx, source, "external-input", "Explain red-black tree rotations"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = external.Close(); err != nil {
		t.Fatal(err)
	}
	query := protocol.ConversationQuery{WorkspaceID: w.ID, Revision: w.Revision, HarnessID: "codex"}
	var list domain.ConversationCatalog
	call(t, client, "conversation.list", query, &list)
	if len(list.Items) != 1 || list.Items[0].ImportedSessionID != "" {
		t.Fatal("source discovery failed", list)
	}
	query.SourceRef, query.Directory = list.Items[0].SourceRef, list.Items[0].Directory
	var preview domain.ConversationPreview
	call(t, client, "conversation.preview", query, &preview)
	if len(provider.Inputs()) != 1 || len(preview.Items) != 2 {
		t.Fatal("preview executed source", preview)
	}
	var imported domain.Session
	call(t, client, "conversation.import", protocol.ConversationImportInput{ConversationQuery: query, Config: source.Config}, &imported)
	var retry domain.Session
	call(t, client, "conversation.import", protocol.ConversationImportInput{ConversationQuery: query, Config: source.Config}, &retry)
	if imported.ID == "" || imported.ID != retry.ID || imported.ThreadID != "" || len(provider.Inputs()) != 1 {
		t.Fatal("import duplicate or execution", imported, retry)
	}
	call(t, client, "conversation.list", protocol.ConversationQuery{WorkspaceID: w.ID, Revision: w.Revision, HarnessID: "codex"}, &list)
	if len(list.Items) != 1 || list.Items[0].ImportedSessionID != imported.ID {
		t.Fatal("catalog lacks durable dedup", list)
	}
	foreign := query
	foreign.Directory = t.TempDir()
	if err = client.Call(ctx, "conversation.preview", foreign, &preview); err == nil {
		t.Fatal("foreign directory read accepted")
	}
	stale := query
	stale.Revision++
	if err = client.Call(ctx, "conversation.import", protocol.ConversationImportInput{ConversationQuery: stale, Config: source.Config}, &retry); err == nil {
		t.Fatal("changed workspace admitted")
	}
	var op domain.Operation
	call(t, client, "session.submit", domain.Submission{OperationID: "continue-import", WorkspaceID: w.ID, SessionID: imported.ID, Text: "Now explain right rotation", Config: source.Config}, &op)
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, client, "session", protocol.SessionQuery{ID: imported.ID, Limit: 100}, &detail)
		return detail.Session.Status == "completed" || detail.Session.Status == "failed"
	})
	if detail.Session.Status != "completed" || detail.Session.ThreadID == "" || detail.Session.ThreadID == source.ThreadID || len(provider.Inputs()) != 2 || len(detail.Items) != 4 {
		t.Fatal("first input failed or took over source", detail.Session, detail.Items)
	}
	reader, err := codex.Start(ctx, codex.Options{Home: provider.Home})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	original, err := reader.ReadConversation(ctx, source.Context, source.ThreadID)
	if err != nil || len(original.Items) != 2 {
		t.Fatal("original CLI history changed", original.Items, err)
	}
}
