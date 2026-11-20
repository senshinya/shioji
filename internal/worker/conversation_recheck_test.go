package worker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
)

func TestWorkerRechecksInterruptedImportAfterSourceAdvances(t *testing.T) {
	_, client, provider := startServer(t)
	var workspace domain.Workspace
	call(t, client, "workspace.create", protocol.WorkspaceInput{Name: "Import", Folders: []string{t.TempDir()}}, &workspace)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	terminal := make(chan domain.Turn, 4)
	opts := codex.Options{Home: provider.Home, OnEvent: func(e domain.Event) error {
		if e.Turn != nil && (e.Turn.Status == "completed" || e.Turn.Status == "interrupted") {
			terminal <- *e.Turn
		}
		return nil
	}}
	await := func(id, status string) {
		t.Helper()
		select {
		case turn := <-terminal:
			if turn.ID != id || string(turn.Status) != status {
				t.Fatal("unexpected terminal turn", turn)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	outside, err := codex.Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer outside.Close()
	source := domain.Session{ID: "outside", Context: domain.LaunchContext{CWD: workspace.Folders[0]}, Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"}}
	source.ThreadID, err = outside.Create(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	id, err := outside.Submit(ctx, source, "original", "SHIOJI_SLOW Interrupted original")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.Started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = outside.Interrupt(ctx, source, id); err != nil {
		t.Fatal(err)
	}
	await(id, "interrupted")
	if err = outside.Close(); err != nil {
		t.Fatal(err)
	}
	close(provider.Release)

	query := protocol.ConversationQuery{WorkspaceID: workspace.ID, Revision: workspace.Revision, HarnessID: "codex", Directory: source.Context.CWD, SourceRef: source.ThreadID}
	var imported domain.Session
	call(t, client, "conversation.import", protocol.ConversationImportInput{ConversationQuery: query, Config: source.Config}, &imported)
	if imported.ReadOnlyHistory() {
		t.Fatal("interrupted import became read-only", imported.Origin)
	}
	outside, err = codex.Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer outside.Close()
	if err = outside.Resume(ctx, source); err != nil {
		t.Fatal(err)
	}
	id, err = outside.Submit(ctx, source, "later", "Fresh source input after import")
	if err != nil {
		t.Fatal(err)
	}
	await(id, "completed")
	if err = outside.Close(); err != nil {
		t.Fatal(err)
	}
	draftSent := func() bool {
		for _, input := range provider.Inputs() {
			if strings.Contains(input, "New draft") {
				return true
			}
		}
		return false
	}

	input := domain.Submission{OperationID: "stale-send", WorkspaceID: workspace.ID, SessionID: imported.ID, Text: "New draft", Config: source.Config}
	var op domain.Operation
	call(t, client, "session.submit", input, &op)
	var detail domain.SessionDetail
	wait(t, func() bool {
		detail = domain.SessionDetail{}
		call(t, client, "session", protocol.SessionQuery{ID: imported.ID, Limit: 100}, &detail)
		return detail.Session.Status == "failed"
	})
	if !detail.Session.ReadOnlyHistory() || detail.Session.ThreadID != "" || draftSent() {
		t.Fatal("stale import was not fenced before sending", detail.Session)
	}
	call(t, client, "conversation.import", protocol.ConversationImportInput{ConversationQuery: query, SessionID: imported.ID, SessionRevision: detail.Session.Revision}, &imported)
	if imported.ID != input.SessionID || imported.ReadOnlyHistory() || draftSent() {
		t.Fatal("recheck did not update the same session without sending", imported)
	}
	input.OperationID = "fresh-send"
	call(t, client, "session.submit", input, &op)
	wait(t, func() bool {
		detail = domain.SessionDetail{}
		call(t, client, "session", protocol.SessionQuery{ID: imported.ID, Limit: 100}, &detail)
		return detail.Session.Status == "completed" || detail.Session.Status == "failed"
	})
	inputs := provider.Inputs()
	if detail.Session.Status != "completed" || detail.Session.ThreadID == source.ThreadID || len(inputs) == 0 ||
		!strings.Contains(inputs[len(inputs)-1], "Fresh source input after import") || !strings.Contains(inputs[len(inputs)-1], "New draft") {
		t.Fatal("rechecked session did not continue with the updated context", detail.Session)
	}
}
