package worker_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/testutil"
	"shioji/internal/worker"
	"strings"
	"testing"
	"time"
)

// A real app-server owns the live thread. Its model is the local fixture; the
// only executable hook appends a marker in this test's disposable directory.
func TestWorkerHookChangePreservesActiveTurnAndNextInputUsesSavedVersion(t *testing.T) {
	p := testutil.NewProvider(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f, err := os.OpenFile(filepath.Join(p.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, "hooks=true")
	f.Close()
	marker := filepath.Join(root, "hook-invocations")
	command := "printf 'approved\\n' >> '" + strings.ReplaceAll(marker, "'", "'\"'\"'") + "'"
	writeWorkerHook(t, filepath.Join(p.Home, "hooks.json"), "UserPromptSubmit", command)
	dir, err := os.MkdirTemp("", "sh-hook-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	server, err := worker.New(context.Background(), worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go server.Serve()
	client := connect(t, server.Directory())
	var ready domain.Readiness
	call(t, client, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, client, "workspace.create", worker.WorkspaceInput{Name: "Live hooks", Folders: []string{root}}, &w)
	global := domain.HookRequest{HarnessID: "codex"}
	var catalog domain.HookCatalog
	call(t, client, "runtime.hooks.list", global, &catalog)
	h := findWorkerHook(t, catalog, "UserPromptSubmit")
	// Disable the untrusted fixture before the initial user input.
	disable := global
	disable.HookChange = domain.HookChange{OperationID: "disable", ID: h.ID, Revision: catalog.Revision, DefinitionRevision: h.DefinitionRevision, Action: domain.HookSetEnabled, Enabled: false}
	var receipt domain.HookOperation
	call(t, client, "runtime.hooks.change", disable, &receipt)
	if !receipt.Saved {
		t.Fatal(receipt)
	}
	var op domain.Operation
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	call(t, client, "session.submit", domain.Submission{OperationID: "active", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg}, &op)
	select {
	case <-p.Started:
	case <-time.After(15 * time.Second):
		t.Fatal("fixture did not start")
	}
	var before domain.SessionDetail
	call(t, client, "session", worker.SessionQuery{ID: op.SessionID}, &before)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("disabled untrusted hook executed", err)
	}
	// Trusting a disabled definition must not enable it or interrupt its owner.
	call(t, client, "runtime.hooks.list", global, &catalog)
	h = findWorkerHook(t, catalog, "UserPromptSubmit")
	trust := global
	trust.HookChange = domain.HookChange{OperationID: "trust", ID: h.ID, Revision: catalog.Revision, DefinitionRevision: h.DefinitionRevision, Action: domain.HookTrustDefinition}
	call(t, client, "runtime.hooks.change", trust, &receipt)
	if !receipt.Saved || receipt.AdoptionPending {
		t.Fatal("independent live process did not confirm adoption", receipt)
	}
	var loaded domain.HookCatalog
	call(t, client, "runtime.hooks.list", domain.HookRequest{HarnessID: "codex", WorkspaceID: w.ID, SessionID: op.SessionID}, &loaded)
	adopted := findWorkerHook(t, loaded, "UserPromptSubmit")
	if !loaded.RuntimeKnown || adopted.Trust != domain.HookTrusted || adopted.Enabled {
		t.Fatal("loaded thread did not adopt exact disabled definition", loaded)
	}
	call(t, client, "runtime.hooks.list", global, &catalog)
	h = findWorkerHook(t, catalog, "UserPromptSubmit")
	enable := global
	enable.HookChange = domain.HookChange{OperationID: "enable", ID: h.ID, Revision: catalog.Revision, DefinitionRevision: h.DefinitionRevision, Action: domain.HookSetEnabled, Enabled: true}
	call(t, client, "runtime.hooks.change", enable, &receipt)
	var during domain.SessionDetail
	call(t, client, "session", worker.SessionQuery{ID: op.SessionID}, &during)
	if during.Session.Status != "running" || during.Session.ThreadID != before.Session.ThreadID {
		t.Fatal("management interrupted or replaced active thread", before.Session, during.Session)
	}
	close(p.Release)
	wait(t, func() bool {
		call(t, client, "session", worker.SessionQuery{ID: op.SessionID}, &during)
		return during.Session.Status == "completed"
	})
	var next domain.Operation
	call(t, client, "session.submit", domain.Submission{OperationID: "next", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "SHIOJI_NEXT", Config: cfg}, &next)
	wait(t, func() bool {
		call(t, client, "session", worker.SessionQuery{ID: op.SessionID}, &during)
		return during.Session.Status == "completed" && len(during.Turns) == 2
	})
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "approved\n" {
		t.Fatal("next input did not execute saved hook exactly once", string(data), err)
	}
	if during.Session.ThreadID != before.Session.ThreadID {
		t.Fatal("next input created another thread")
	}
}
