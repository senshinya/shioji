package worker_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/supervisor"
	"shioji/internal/testutil"
	"shioji/internal/transport"
)

func TestSignedSchema15WorkerMigratesThroughOldFixedService(t *testing.T) {
	baseline, candidate := os.Getenv("SHIOJI_SCHEMA15_WORKER"), os.Getenv("SHIOJI_MIGRATION_WORKER")
	if baseline == "" || candidate == "" {
		t.Skip("set the separate signed schema 15 and migration worker binaries")
	}
	dir, err := os.MkdirTemp("/tmp", "shioji-schema-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	home := t.TempDir()
	provider := testutil.NewProvider(t)
	t.Setenv("CODEX_HOME", provider.Home)
	release := sync.OnceFunc(func() { close(provider.Release) })
	defer release()
	log, err := os.Create(filepath.Join(t.TempDir(), "old-supervisor.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	oldHost := os.Getenv("SHIOJI_SCHEMA15_SUPERVISOR")
	if oldHost == "" {
		oldHost = baseline
	} else {
		build, err := transport.StageWorker(t.Context(), dir, baseline)
		if err != nil {
			t.Fatal(err)
		}
		if err := transport.SaveActiveWorker(dir, build.BuildID); err != nil {
			t.Fatal(err)
		}
	}
	old := exec.Command(oldHost, "--state-dir", dir, "--codex", transport.ExistingCodex())
	old.Stdout, old.Stderr = log, log
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	oldExited := make(chan error, 1)
	go func() { oldExited <- old.Wait() }()
	defer func() {
		_ = old.Process.Signal(syscall.SIGTERM)
		select {
		case <-oldExited:
		case <-time.After(10 * time.Second):
			_ = old.Process.Kill()
			<-oldExited
		}
		if cmd := migrationSupervisorPID(dir); cmd > 1 && cmd != old.Process.Pid {
			_ = syscall.Kill(cmd, syscall.SIGTERM)
			for i := 0; i < 500 && migrationSupervisorPID(dir) == cmd; i++ {
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	connect := func(device, build string) *protocol.Client {
		t.Helper()
		for ctx.Err() == nil {
			call, done := context.WithTimeout(ctx, time.Second)
			client, err := protocol.Dial(call, func(ctx context.Context) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
			}, device)
			done()
			if err == nil {
				if client.Worker != nil && (build == "" || build == client.Worker.BuildID) {
					return client
				}
				client.Close()
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("no handshake from expected worker")
		return nil
	}
	client := connect("", "")
	device, previous := client.DeviceID, client.Worker.BuildID
	if client.Worker.Schema != 15 || client.Worker.Lifecycle != 1 {
		t.Fatal("baseline must be a schema 15 fixed worker", client.Worker)
	}
	var workspace domain.Workspace
	if err := client.Call(ctx, "workspace.create", protocol.WorkspaceInput{Name: "schema-preservation", Folders: []string{home}}, &workspace); err != nil {
		t.Fatal(err)
	}
	var ready domain.Readiness
	for ctx.Err() == nil && len(ready.Models) == 0 {
		if err := client.Call(ctx, "readiness", nil, &ready); err != nil {
			t.Fatal(err)
		}
		if len(ready.Models) == 0 {
			time.Sleep(30 * time.Millisecond)
		}
	}
	if len(ready.Models) == 0 {
		t.Fatal("old worker did not discover the isolated fixture provider")
	}
	var accepted domain.Operation
	if err := client.Call(ctx, "session.submit", domain.Submission{OperationID: "old-input", WorkspaceID: workspace.ID,
		Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &accepted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.Started:
	case <-ctx.Done():
		t.Fatal("old worker did not start the fixture input")
	}
	client.Close()
	build, err := transport.StageWorker(ctx, dir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	in := domain.WorkerReplacement{ID: "signed-schema-migration", BuildID: build.BuildID, ExpectedBuildID: previous,
		Explicit: true, Policy: "when-idle", SchemaMigration: true}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, candidate, "--state-dir", dir, "--service-schema-upgrade", "start", "--service-device", device)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var reply struct {
		Operation supervisor.Operation
		Error     string
	}
	if err := json.Unmarshal(out, &reply); err != nil || reply.Error != "" || reply.Operation.Input != in {
		t.Fatal("candidate failed to accept the original migration", string(out), err)
	}
	sawWaiting := false
	for {
		var op supervisor.Operation
		call, stop := context.WithTimeout(ctx, time.Second)
		op, err = supervisor.Query(call, dir, in.ID)
		stop()
		if !sawWaiting && err == nil && op.Phase == "waiting" {
			if op.Input != in {
				t.Fatal("old controller lost the authorized waiting migration identity", op)
			}
			sawWaiting = true
			release()
		}
		if err == nil && op.Terminal() {
			if op.Phase != "applied" {
				log, _ := os.ReadFile(filepath.Join(dir, "supervisor", "schema-bootstrap.log"))
				t.Fatalf("migration did not apply: %+v (%s)", op, log)
			}
			break
		}
		if ctx.Err() != nil {
			log, _ := os.ReadFile(filepath.Join(dir, "supervisor", "schema-bootstrap.log"))
			t.Fatalf("migration receipt did not settle: %+v (%v) (%s)", op, err, log)
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !sawWaiting {
		t.Fatal("migration did not wait for the active turn")
	}
	next := connect(device, in.BuildID)
	var snap domain.Snapshot
	if err := next.Call(ctx, "snapshot", nil, &snap); err != nil || next.Worker.Schema != 16 || len(snap.Workspaces) != 1 || snap.Workspaces[0].ID != workspace.ID {
		t.Fatal("migrated worker did not preserve workspace identity", next.Worker, snap.Workspaces, err)
	}
	var conversation domain.SessionDetail
	if err := next.Call(ctx, "session", protocol.SessionQuery{ID: accepted.SessionID}, &conversation); err != nil ||
		conversation.Session.ID != accepted.SessionID || conversation.Session.Status != "completed" {
		t.Fatal("migration lost the completed old conversation", conversation.Session, err)
	}
	foundReply := false
	for _, item := range conversation.Items {
		if item.Kind == "assistant" && item.Text == "SHIOJI_SLOW_DONE" {
			foundReply = true
		}
	}
	if !foundReply {
		t.Fatal("migration lost the reply committed immediately before drain")
	}
	if _, schema, err := store.ReadWorkerIdentity(ctx, filepath.Join(dir, "schema-migration-"+in.ID)); err != nil || schema != 15 {
		t.Fatal("original SQLite database not retained", schema, err)
	}
	schedule := domain.Schedule{ID: "new-schedule", WorkspaceID: workspace.ID, Name: "New schema write", Text: "Only reply OK",
		Rule: domain.ScheduleRule{Kind: "daily", Time: "12:00"}}
	var saved domain.Schedule
	if err := next.Call(ctx, "schedule.save", schedule, &saved); err != nil || saved.Revision != 1 {
		t.Fatal("candidate did not open schema 16 business admission", saved, err)
	}
	next.Close()
	pid := migrationSupervisorPID(dir)
	if pid <= 1 {
		t.Fatal("could not confirm isolated migrated supervisor")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for migrationSupervisorPID(dir) == pid && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	restarted := exec.Command(candidate, "--state-dir", dir, "--codex", filepath.Join(home, "missing-codex"))
	restarted.Stdout, restarted.Stderr = log, log
	if err := restarted.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = restarted.Process.Signal(syscall.SIGTERM)
		_ = restarted.Wait()
	}()
	next = connect(device, in.BuildID)
	defer next.Close()
	var detail domain.ScheduleDetail
	for ctx.Err() == nil {
		err = next.Call(ctx, "schedule.get", protocol.ScheduleCommand{ID: schedule.ID}, &detail)
		var remote *protocol.RemoteError
		if err == nil || !errors.As(err, &remote) || remote.Code != "notSent" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil || detail.Schedule.ID != schedule.ID || detail.Schedule.Revision != 1 {
		t.Fatal("restart did not preserve the write accepted after migration", detail.Schedule, err)
	}
}

func migrationSupervisorPID(dir string) int {
	conn, err := net.DialTimeout("unix", filepath.Join(dir, "supervisor.sock"), 100*time.Millisecond)
	if err != nil {
		return 0
	}
	defer conn.Close()
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return 0
	}
	var pid int
	var inner error
	err = raw.Control(func(fd uintptr) {
		pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	})
	if errors.Join(err, inner) != nil {
		return 0
	}
	return pid
}

func TestSignedInterruptedMigrationCanRecoverThroughOldFixedService(t *testing.T) {
	baseline, candidate := os.Getenv("SHIOJI_SCHEMA15_WORKER"), os.Getenv("SHIOJI_MIGRATION_WORKER")
	if baseline == "" || candidate == "" {
		t.Skip("set the separate signed schema 15 and migration worker binaries")
	}
	dir, err := os.MkdirTemp("/tmp", "shioji-schema-restore-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("CODEX_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	oldBuild, err := transport.StageWorker(ctx, dir, baseline)
	if err != nil {
		t.Fatal(err)
	}
	nextBuild, err := transport.StageWorker(ctx, dir, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.SaveActiveWorker(dir, oldBuild.BuildID); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	device := db.DeviceID()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	op := supervisor.Operation{DeviceID: device, Legacy: true, Phase: "migrating", PreviousBuild: oldBuild.BuildID,
		SourceSchema: 15, TargetSchema: 16, SourceInstance: "released-source", SourcePID: 42, Input: domain.WorkerReplacement{
			ID: "interrupted-migration", BuildID: nextBuild.BuildID, ExpectedBuildID: oldBuild.BuildID,
			Explicit: true, Policy: "when-idle", SchemaMigration: true}}
	if err := supervisor.SaveDrainIntent(dir, supervisor.DrainIntent{
		ID: op.Input.ID, Instance: op.SourceInstance, DeviceID: device, BuildID: oldBuild.BuildID,
	}); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "schema-migration-"+op.Input.ID)
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	originalDB := filepath.Join(original, "state.sqlite")
	if err := store.CopyWorkerDatabase(ctx, dir, originalDB); err != nil {
		t.Fatal(err)
	}
	oldDB, err := sql.Open("sqlite", originalDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldDB.Exec("DROP TABLE schedule_runs; DROP TABLE schedules; PRAGMA user_version=15"); err != nil {
		t.Fatal(err)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatal(err)
	}
	operations := filepath.Join(dir, "supervisor", "operations")
	if err := os.MkdirAll(operations, 0700); err != nil {
		t.Fatal(err)
	}
	plans := filepath.Join(dir, "supervisor", "schema-plans")
	if err := os.MkdirAll(plans, 0700); err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]any{
		filepath.Join(operations, op.Input.ID+".json"):   op,
		filepath.Join(plans, op.Input.ID+".json"):        op,
		filepath.Join(dir, "supervisor", "current.json"): struct{ ID string }{op.Input.ID},
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := exec.Command(baseline, "--state-dir", dir)
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = old.Process.Signal(syscall.SIGTERM)
		_ = old.Wait()
		if pid := migrationSupervisorPID(dir); pid > 1 && pid != old.Process.Pid {
			_ = syscall.Kill(pid, syscall.SIGTERM)
			for i := 0; i < 500 && migrationSupervisorPID(dir) == pid; i++ {
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()
	for ctx.Err() == nil {
		call, stop := context.WithTimeout(ctx, time.Second)
		status, e := supervisor.Call(call, dir, supervisor.Request{Action: "status"})
		stop()
		if e == nil && status.Input.ID == op.Input.ID && status.Phase == "blocked" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("old fixed service did not expose its blocked operation")
	}
	input := domain.WorkerReplacement{ID: "restore-original", BuildID: nextBuild.BuildID, Explicit: true, Policy: "when-idle"}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, candidate, "--state-dir", dir, "--service-control", "repair", "--service-device", device)
	command.Stdin = bytes.NewReader(data)
	output, err := command.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("已恢复原数据库")) {
		log, _ := os.ReadFile(filepath.Join(dir, "supervisor", "repair-host.log"))
		health, _ := os.ReadFile(filepath.Join(dir, "supervisor", "health.json"))
		t.Fatal("signed recovery did not restore the original database", string(output), err, string(log), string(health))
	}
	actual, schema, err := store.ReadWorkerIdentity(ctx, dir)
	if err != nil || schema != 15 || actual != device {
		t.Fatal("wrong device/schema after recovery", actual, schema, err)
	}
	status, err := supervisor.Call(ctx, dir, supervisor.Request{Action: "status", ID: op.Input.ID})
	if err != nil || status.Phase != "rolled-back" {
		t.Fatal("recovery did not publish the actual receipt", status, err)
	}
}
