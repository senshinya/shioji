package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/worker"
)

func writeWorkerHook(t *testing.T, path, event, command string) {
	t.Helper()
	writeWorkerHooks(t, path, map[string]string{event: command})
}

func writeWorkerHooks(t *testing.T, path string, commands map[string]string) {
	t.Helper()
	hooks := make(map[string]any, len(commands))
	for event, command := range commands {
		hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}
	}
	b, _ := json.Marshal(map[string]any{"hooks": hooks})
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerHookBatchRejectsStaleSelectionAndReturnsSavedCatalog(t *testing.T) {
	server, client, p := startServer(t)
	f, err := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\nhooks=true\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	writeWorkerHooks(t, filepath.Join(p.Home, "hooks.json"), map[string]string{
		"PreToolUse": "true # pre", "PostToolUse": "true # post", "Stop": "true # unselected",
	})
	dir := server.Directory()
	client.Close()
	server.Close()
	server, err = worker.New(context.Background(), worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	go server.Serve()
	client = connect(t, dir)
	scope := domain.HookRequest{HarnessID: "codex"}
	var catalog domain.HookCatalog
	call(t, client, "runtime.hooks.list", scope, &catalog)
	pre, post := findWorkerHook(t, catalog, "PreToolUse"), findWorkerHook(t, catalog, "PostToolUse")
	targets := []map[string]string{
		{"id": pre.ID, "definitionRevision": pre.DefinitionRevision},
		{"id": post.ID, "definitionRevision": "old-definition"},
	}
	request := map[string]any{"harnessId": "codex", "operationId": "invalid-batch", "revision": catalog.Revision, "action": "trustDefinitions", "definitions": targets}
	var result struct {
		domain.HookOperation
		Catalog domain.HookCatalog `json:"catalog"`
	}
	if err := client.Call(context.Background(), "runtime.hooks.change", request, &result); err == nil && result.Saved {
		t.Fatal("stale batch was saved")
	}
	var afterRejected domain.HookCatalog
	call(t, client, "runtime.hooks.list", scope, &afterRejected)
	if findWorkerHook(t, afterRejected, "PreToolUse").Trust != domain.HookUntrusted {
		t.Fatal("stale batch partially trusted a valid definition")
	}
	targets[1]["definitionRevision"] = post.DefinitionRevision
	request["operationId"] = "approved-batch"
	call(t, client, "runtime.hooks.change", request, &result)
	if !result.Saved || result.State != domain.OperationConfirmed || result.Catalog.Revision == "" || result.Catalog.Revision == catalog.Revision {
		t.Fatal("missing updated catalog in save response", result)
	}
	for _, entry := range []domain.HookEntry{pre, post} {
		h := findWorkerHook(t, result.Catalog, entry.Event)
		if h.Trust != domain.HookTrusted || h.Enabled != entry.Enabled || h.DefinitionRevision != entry.DefinitionRevision {
			t.Fatal("wrong state in saved catalog", h)
		}
	}
	if findWorkerHook(t, result.Catalog, "Stop").Trust != domain.HookUntrusted {
		t.Fatal("unselected hook was trusted")
	}
	var receipt domain.HookOperation
	call(t, client, "runtime.hooks.operation", request, &receipt)
	if !receipt.Saved || receipt.State != domain.OperationConfirmed {
		t.Fatal("batch receipt was not preserved", receipt)
	}
	unselected := findWorkerHook(t, result.Catalog, "Stop")
	targets[1] = map[string]string{"id": unselected.ID, "definitionRevision": unselected.DefinitionRevision}
	if err := client.Call(context.Background(), "runtime.hooks.operation", request, &receipt); err == nil {
		t.Fatal("receipt accepted a different selection under the same operation ID")
	}
}

func findWorkerHook(t *testing.T, c domain.HookCatalog, event string) domain.HookEntry {
	t.Helper()
	for _, h := range c.Entries {
		if h.Event == event {
			return h
		}
	}
	t.Fatalf("missing %s: %+v", event, c)
	return domain.HookEntry{}
}

func TestWorkerHookOwnershipAndDurableReceiptDoNotApproveRevisedDefinition(t *testing.T) {
	server, client, p := startServer(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(f, "\nhooks=true\n[projects.%q]\ntrust_level=\"trusted\"\n", root)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	writeWorkerHook(t, filepath.Join(p.Home, "hooks.json"), "Stop", "true # global")
	writeWorkerHook(t, filepath.Join(root, ".codex/hooks.json"), "PreToolUse", "true # project")
	dir := server.Directory()
	client.Close()
	server.Close()
	server, err = worker.New(context.Background(), worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	go server.Serve()
	client = connect(t, dir)
	var w domain.Workspace
	call(t, client, "workspace.create", map[string]any{"name": "Hooks", "folders": []string{root}}, &w)
	var catalog domain.HookCatalog
	call(t, client, "runtime.hooks.list", domain.HookRequest{HarnessID: "codex", WorkspaceID: w.ID}, &catalog)
	project := findWorkerHook(t, catalog, "PreToolUse")
	global := findWorkerHook(t, catalog, "Stop")
	if !project.CanTrust || global.CanTrust || global.CanEnable {
		t.Fatal("inherited entry can mutate global settings", catalog)
	}
	for _, bad := range []domain.HookRequest{
		{HarnessID: "codex", WorkspaceID: "missing"},
		{HarnessID: "codex", SessionID: "unknown"},
	} {
		if err := client.Call(context.Background(), "runtime.hooks.list", bad, new(any)); err == nil {
			t.Fatal("forged scope accepted", bad)
		}
	}
	bad := domain.HookRequest{HarnessID: "codex", WorkspaceID: w.ID, HookChange: domain.HookChange{OperationID: "inherited", ID: global.ID, Revision: catalog.Revision, DefinitionRevision: global.DefinitionRevision, Action: domain.HookTrustDefinition}}
	if err := client.Call(context.Background(), "runtime.hooks.change", bad, new(any)); err == nil {
		t.Fatal("workspace trusted inherited global hook")
	}
	in := domain.HookRequest{HarnessID: "codex", WorkspaceID: w.ID, HookChange: domain.HookChange{OperationID: "approve-project", ID: project.ID, Revision: catalog.Revision, DefinitionRevision: project.DefinitionRevision, Action: domain.HookTrustDefinition}}
	var receipt domain.HookOperation
	call(t, client, "runtime.hooks.change", in, &receipt)
	if receipt.State != domain.OperationConfirmed || !receipt.Saved {
		t.Fatal("missing saved receipt", receipt)
	}
	call(t, client, "runtime.hooks.change", in, &receipt)
	changed := in
	changed.Enabled = true
	if err := client.Call(context.Background(), "runtime.hooks.change", changed, new(any)); err == nil {
		t.Fatal("operation identity accepted changed payload")
	}
	writeWorkerHook(t, filepath.Join(root, ".codex/hooks.json"), "PreToolUse", "true # revised project")
	stale := in
	stale.OperationID = "stale"
	if err := client.Call(context.Background(), "runtime.hooks.change", stale, new(any)); err == nil {
		t.Fatal("stale revision approved changed definition")
	}
	dir = server.Directory()
	client.Close()
	server.Close()
	restarted, err := worker.New(context.Background(), worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	go restarted.Serve()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	reconnected, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reconnected.Close()
	call(t, reconnected, "runtime.hooks.operation", in, &receipt)
	if !receipt.Saved || receipt.State != domain.OperationConfirmed || receipt.Input.DefinitionRevision != project.DefinitionRevision {
		t.Fatal("receipt lost exact approved version", receipt)
	}
	call(t, reconnected, "runtime.hooks.change", in, &receipt)
	call(t, reconnected, "runtime.hooks.list", domain.HookRequest{HarnessID: "codex", WorkspaceID: w.ID}, &catalog)
	current := findWorkerHook(t, catalog, "PreToolUse")
	if current.Trust != domain.HookModified || current.DefinitionRevision == project.DefinitionRevision {
		t.Fatal("restart replayed approval onto newer definition", current)
	}
}
