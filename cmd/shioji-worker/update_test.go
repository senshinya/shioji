package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/testutil"
	"shioji/internal/transport"
	"testing"
	"time"
)

func TestSignedWorkerUpdateKeepsIdentityAndCapturesRunningBuild(t *testing.T) {
	p := testutil.NewProvider(t)
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old-worker")
	newPath := filepath.Join(dir, "new-worker")
	for i, path := range []string{oldPath, newPath} {
		stamp := []string{"100", "200"}[i]
		if output, err := exec.Command("go", "build", "-ldflags", "-X shioji/internal/runtimebuild.BuiltAt="+stamp, "-o", path, ".").CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
		if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", path).CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
	}
	state, err := os.MkdirTemp("/tmp", "sh-up-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	log, err := os.Create(filepath.Join(dir, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(oldPath, "--state-dir", state, "--codex", transport.ExistingCodex())
	cmd.Env = append(os.Environ(), "CODEX_HOME="+p.Home)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Signal(os.Interrupt); cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	connect := func(want string) *protocol.Client {
		t.Helper()
		for ctx.Err() == nil {
			conn, err := net.DialTimeout("unix", filepath.Join(state, "worker.sock"), 100*time.Millisecond)
			if err == nil {
				client, e := protocol.Connect(ctx, conn, "")
				if e == nil && client.Worker != nil && (want == "" || client.Worker.BuildID == want) {
					return client
				}
				conn.Close()
			}
			time.Sleep(20 * time.Millisecond)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "worker.log"))
		t.Fatal("worker did not become ready", string(data))
		return nil
	}
	old := connect("")
	defer old.Close()
	if old.Worker.BuiltAt != 100 || !old.Worker.SafeUpdate {
		t.Fatal("old service metadata missing", old.Worker)
	}
	device, build := old.DeviceID, old.Worker.BuildID
	var workspace domain.Workspace
	if err = old.Call(ctx, "workspace.create", protocol.WorkspaceInput{Name: "Kept", Folders: []string{t.TempDir()}}, &workspace); err != nil {
		t.Fatal(err)
	}
	var ready domain.Readiness
	for ctx.Err() == nil {
		if err = old.Call(ctx, "readiness", nil, &ready); err != nil {
			t.Fatal(err)
		}
		if ready.State == domain.RuntimeReady && len(ready.Models) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("harness not ready", ready)
	}
	var input domain.Operation
	if err = old.Call(ctx, "session.submit", domain.Submission{OperationID: "kept-input", WorkspaceID: workspace.ID, Text: "reply briefly", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &input); err != nil {
		t.Fatal(err)
	}
	var before domain.SessionDetail
	for ctx.Err() == nil {
		if err = old.Call(ctx, "session", protocol.SessionQuery{ID: input.SessionID}, &before); err != nil {
			t.Fatal(err)
		}
		if before.Session.Status == "completed" && before.Session.SourceTitle != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("initial turn did not complete", before.Session)
	}
	// The file path is replaced while the old image remains mapped and running.
	if err = os.Rename(newPath, oldPath); err != nil {
		t.Fatal(err)
	}
	var running domain.WorkerBuild
	if err = old.Call(ctx, "worker.info", nil, &running); err != nil || running.BuildID != build || running.BuiltAt != 100 {
		t.Fatal("disk replacement changed reported running build", running, err)
	}
	candidate, err := transport.StageWorker(ctx, state, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.BuildID == build || candidate.BuiltAt != 200 {
		t.Fatal("staging did not preserve new binary", candidate)
	}
	controller := app.NewController(state)
	controller.ExpectedDeviceID = device
	controller.WorkerBinary = oldPath
	controller.WorkerAutoUpdate = true
	model := app.NewModel()
	model.WorkspaceID = workspace.ID
	model.Drafts["home"] = "keep unsent draft"
	go controller.Run(ctx)
	seenUpdating := false
	for model.WorkerService == nil || model.WorkerService.Build == nil || model.WorkerService.Build.BuildID != candidate.BuildID || model.WorkerService.Phase != app.WorkerCurrent || !model.Connected {
		select {
		case update := <-controller.Updates:
			update(model)
			if model.WorkerService != nil && model.WorkerService.Phase == app.WorkerUpdating {
				seenUpdating = true
			}
		case <-ctx.Done():
			t.Fatal("automatic update did not reconnect", model.WorkerService, model.Error)
		}
	}
	if !seenUpdating || model.Drafts["home"] != "keep unsent draft" || model.WorkspaceID != workspace.ID {
		t.Fatal("update skipped progress or lost client state", model.WorkerService, model.Drafts)
	}
	backups, _ := filepath.Glob(filepath.Join(state, "update-backup-*.sqlite"))
	if len(backups) != 1 {
		t.Fatal("consistent backup missing or update repeated", backups)
	}
	current := connect(candidate.BuildID)
	defer current.Close()
	var snapshot domain.Snapshot
	if err = current.Call(ctx, "snapshot", nil, &snapshot); err != nil {
		t.Fatal(err)
	}
	var history domain.SessionDetail
	if err = current.Call(ctx, "session", protocol.SessionQuery{ID: input.SessionID}, &history); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 1 || history.Session.ID != input.SessionID || history.Session.Title != before.Session.Title || len(history.Turns) != 1 || len(history.Items) != 2 || len(p.Inputs()) != 1 {
		t.Fatal("update lost or replayed conversation", history.Session, len(history.Turns), len(history.Items), len(p.Inputs()))
	}
	if current.DeviceID != device || snapshot.DeviceID != device || len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].ID != workspace.ID {
		t.Fatal("update changed worker ownership or data", snapshot)
	}
}
