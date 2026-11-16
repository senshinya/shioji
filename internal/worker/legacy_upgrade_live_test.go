package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/runtimebuild"
	"shioji/internal/supervisor"
	"shioji/internal/testutil"
	"shioji/internal/transport"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Uses a separately built, signed pre-supervisor baseline. No user state,
// system service registration, credentials or real model provider are touched.
func TestLegacySignedWorkerWithResidentToolsMigratesToFixedSupervisor(t *testing.T) {
	baseline := os.Getenv("SHIOJI_LEGACY_WORKER_BINARY")
	if baseline == "" {
		t.Skip("set a separately built pre-supervisor baseline binary")
	}
	p := testutil.NewProvider(t)
	t.Setenv("CODEX_HOME", p.Home)
	dir, err := os.MkdirTemp("/tmp", "sw-legacy-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	bins := t.TempDir()
	oldPath, newPath := filepath.Join(bins, "old-worker"), filepath.Join(bins, "new-worker")
	source, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(oldPath, source, 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-ldflags", "-X shioji/internal/runtimebuild.BuiltAt=200", "-o", newPath, "./cmd/shioji-worker")
	build.Dir = "../.."
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	for _, path := range []string{oldPath, newPath} {
		if out, e := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", path).CombinedOutput(); e != nil {
			t.Fatal(e, string(out))
		}
	}
	log, err := os.Create(filepath.Join(bins, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	old := exec.Command(oldPath, "--state-dir", dir, "--codex", transport.ExistingCodex())
	old.Env = append(os.Environ(), "CODEX_HOME="+p.Home)
	old.Stdout, old.Stderr = log, log
	if err = old.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Process.Signal(os.Interrupt); _ = old.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	connect := func(want string) *protocol.Client {
		t.Helper()
		for ctx.Err() == nil {
			conn, e := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
			if e == nil {
				client, e := protocol.Connect(ctx, conn, "")
				if e == nil && client.Worker != nil && (want == "" || client.Worker.BuildID == want) {
					return client
				}
				conn.Close()
			}
			time.Sleep(20 * time.Millisecond)
		}
		data, _ := os.ReadFile(filepath.Join(bins, "worker.log"))
		t.Fatal("worker did not connect", string(data))
		return nil
	}
	client := connect("")
	defer client.Close()
	if client.Worker.Lifecycle != 0 {
		t.Fatal("fixture is not a pre-supervisor worker", client.Worker)
	}
	device, previous := client.DeviceID, client.Worker.BuildID
	var w domain.Workspace
	if err = client.Call(ctx, "workspace.create", protocol.WorkspaceInput{Name: "legacy", Folders: []string{bins}}, &w); err != nil {
		t.Fatal(err)
	}
	var ready domain.Readiness
	for ctx.Err() == nil {
		if err = client.Call(ctx, "readiness", nil, &ready); err == nil && ready.State == domain.RuntimeReady {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var op domain.Operation
	if err = client.Call(ctx, "session.submit", domain.Submission{OperationID: "original-input", WorkspaceID: w.ID, Text: "Only reply OK", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op); err != nil {
		t.Fatal(err)
	}
	var before domain.SessionDetail
	for ctx.Err() == nil {
		if err = client.Call(ctx, "session", protocol.SessionQuery{ID: op.SessionID}, &before); err == nil && before.Session.Status == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	manager, err := managedexec.New(filepath.Join(dir, "executions"), oldPath)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := manager.Existing(op.SessionID)
	if err != nil || scope == nil {
		t.Fatal(err)
	}
	if err = scope.Bind("fixture", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ticket, err := scope.Prepare("fixture", "resident-node-equivalent")
	if err != nil {
		t.Fatal(err)
	}
	resident := exec.Command(oldPath, "--managed-exec", ticket.ID)
	resident.Env = managedexec.BootstrapEnv()
	resident.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, _ := resident.StdinPipe()
	output, _ := resident.StdoutPipe()
	if err = resident.Start(); err != nil {
		t.Fatal(err)
	}
	defer resident.Wait()
	defer input.Close()
	defer scope.Stop(context.Background())
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var packet managedexec.Registration
	if err = json.Unmarshal([]byte(strings.TrimPrefix(line, managedexec.RegistrationPrefix)), &packet); err != nil {
		t.Fatal(err)
	}
	if err = scope.Register(packet); err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(input).Encode(managedexec.Acknowledgement{ID: ticket.ID, Argv: []string{"/bin/sh", "-c", "trap '' TERM; sleep 60 & echo resident-ready; wait"}, Env: []string{"PATH=/usr/bin:/bin"}, PipeStdin: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	digest, err := runtimebuild.Digest(newPath)
	if err != nil {
		t.Fatal(err)
	}
	var refused domain.WorkerUpdateResult
	if err = client.Call(ctx, "worker.update", domain.WorkerReplacement{ExpectedBuildID: previous, BuildID: digest}, &refused); err != nil || refused.Ready {
		t.Fatal("baseline did not reproduce the resident-process upgrade block", refused, err)
	}
	controller := app.NewController(dir)
	controller.ExpectedDeviceID = device
	controller.WorkerBinary = newPath
	controller.WorkerAutoUpdate = true
	model := app.NewModel()
	model.WorkspaceID = w.ID
	model.Drafts["home"] = "keep my unsent draft"
	go controller.Run(ctx)
	var acceptedID string
	for ctx.Err() == nil {
		select {
		case update := <-controller.Updates:
			update(model)
			if model.WorkerService != nil {
				if model.WorkerService.OperationID != "" {
					acceptedID = model.WorkerService.OperationID
				}
				if model.WorkerService.Phase == app.WorkerCurrent && model.WorkerService.Build != nil && model.WorkerService.Build.BuildID == digest && model.Connected {
					goto migrated
				}
			}
		case <-ctx.Done():
			data, _ := os.ReadFile(filepath.Join(dir, "supervisor", "legacy-install.log"))
			t.Fatal("native controller did not complete legacy migration", model.WorkerService, model.Error, string(data))
		}
	}
migrated:
	if acceptedID == "" || model.Drafts["home"] != "keep my unsent draft" {
		t.Fatal("migration lost stable operation or draft", acceptedID, model.Drafts)
	}

	// Retire only this isolated helper after the test, by the actual private
	// supervisor endpoint's kernel peer identity; never by a process name.
	defer func() {
		conn, e := net.Dial("unix", filepath.Join(dir, "supervisor.sock"))
		if e != nil {
			return
		}
		raw, _ := conn.(*net.UnixConn).SyscallConn()
		var pid int
		_ = raw.Control(func(fd uintptr) { pid, _ = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) })
		conn.Close()
		list, _ := managedexec.Processes()
		for _, p := range list {
			if p.Identity.PID == pid {
				_ = managedexec.SignalExact(p, unix.SIGTERM)
				deadline := time.Now().Add(8 * time.Second)
				for managedexec.ProcessState(p) == "running" && time.Now().Before(deadline) {
					time.Sleep(20 * time.Millisecond)
				}
			}
		}
	}()
	var result supervisor.Operation
	for ctx.Err() == nil {
		result, err = supervisor.Query(ctx, dir, acceptedID)
		if err == nil && result.Terminal() {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil || result.Phase != "applied" {
		data, _ := os.ReadFile(filepath.Join(dir, "supervisor", "legacy-install.log"))
		t.Fatal("legacy migration failed", result, err, string(data))
	}
	current := connect(digest)
	defer current.Close()
	if current.DeviceID != device || current.Worker.Lifecycle != 1 {
		t.Fatal("migration changed device identity", current.DeviceID, current.Worker)
	}
	var after domain.SessionDetail
	if err = current.Call(ctx, "session", protocol.SessionQuery{ID: op.SessionID}, &after); err != nil {
		t.Fatal(err)
	}
	if after.Session.ID != before.Session.ID || after.Session.ThreadID != before.Session.ThreadID || len(after.Items) != len(before.Items) || len(p.Inputs()) != 1 {
		t.Fatal("migration lost/replayed history", after.Session, len(after.Items), len(p.Inputs()))
	}
	recovered, err := manager.Existing(op.SessionID)
	if err != nil || recovered.VerifyStopped() != nil {
		t.Fatal("registered old tool groups survived migration", err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "update-backup-*.sqlite"))
	if len(backups) != 1 {
		t.Fatal("missing consistent migration backup", backups)
	}
}
