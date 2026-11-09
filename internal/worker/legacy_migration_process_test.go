package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"shioji/internal/runtimebuild"
	"shioji/internal/store"
	"shioji/internal/transport"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Isolated native subprocesses exercise real kernel ownership, signals and
// leases. The production-code historical worker is verified separately.
func legacyMigrationTestMain() bool {
	if os.Getenv("SHIOJI_LEGACY_UNIT_HELPER") == "" {
		return false
	}
	arg := func(name string) string {
		for i, x := range os.Args {
			if x == name && i+1 < len(os.Args) {
				return os.Args[i+1]
			}
		}
		return ""
	}
	if slices.Contains(os.Args, "--describe") {
		b := runtimebuild.Current()
		b.SafeUpdate = true
		b.Schema = store.SchemaVersion
		json.NewEncoder(os.Stdout).Encode(protocol.Frame{Type: "worker-build", Major: 2, Minor: 20, Worker: &b})
		return true
	}
	dir := arg("--state-dir")
	if dir == "" {
		return false
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	if os.Getenv("SHIOJI_LEGACY_IGNORE_TERM") == "1" && arg("--legacy-migration") == "" {
		signal.Ignore(syscall.SIGTERM)
	}
	if arg("--legacy-migration") == "run" {
		config := Config{Directory: dir, Harnesses: []harness.Adapter{memoryHarness{id: "memory"}}}
		if os.Getenv("SHIOJI_LEGACY_FAIL_START") == "1" {
			marker := filepath.Join(dir, ".test-failed-once")
			if _, e := os.Stat(marker); os.IsNotExist(e) {
				os.WriteFile(marker, []byte("1"), 0600)
				config.CheckStartupBuild = func() error { return errors.New("fixture start failed") }
			}
		}
		s, err := RunLegacyMigration(ctx, config, arg("--migration-id"))
		if err != nil {
			os.Exit(1)
		}
		if s == nil {
			return true
		}
		defer s.Close()
		os.WriteFile(filepath.Join(dir, ".test-environment"), []byte(os.Getenv("SHIOJI_MIGRATION_ENV_VALUE")), 0600)
		go func() { <-ctx.Done(); s.Close() }()
		s.Serve()
		return true
	}
	owner, err := Acquire(dir)
	if err != nil {
		os.Exit(2)
	}
	defer owner.Close()
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		os.Exit(3)
	}
	defer db.Close()
	listener, err := owner.Listen()
	if err != nil {
		os.Exit(4)
	}
	go func() { <-ctx.Done(); listener.Close() }()
	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		go func(c net.Conn) {
			defer c.Close()
			codec := protocol.NewCodec(c, c)
			var hello protocol.Frame
			if codec.Read(&hello) != nil || hello.Major != 1 {
				return
			}
			epoch := domain.NewID()
			codec.Write(protocol.Frame{Type: "welcome", Major: 1, Minor: 3, DeviceID: db.DeviceID(), Nonce: hello.Nonce, Epoch: epoch})
			for {
				var f protocol.Frame
				if codec.Read(&f) != nil {
					return
				}
				var body any
				var e error
				switch f.Method {
				case "snapshot":
					body, e = db.Snapshot()
				case "session":
					var in protocol.SessionQuery
					json.Unmarshal(f.Body, &in)
					body, e = db.Session(in.ID, 0, 1)
				default:
					os.Exit(5)
				}
				b, _ := json.Marshal(body)
				reply := protocol.Frame{Type: "result", ID: f.ID, DeviceID: db.DeviceID(), Epoch: epoch, Body: b}
				if e != nil {
					reply.Error = e.Error()
				}
				if codec.Write(reply) != nil {
					return
				}
			}
		}(conn)
	}
	return true
}
func legacyProcessFixture(t *testing.T, status domain.ExecutionState) (string, string, string, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "shioji-legacy-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.CreateWorkspace("preserved", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "do not resend"})
	if err != nil {
		t.Fatal(err)
	}
	db.SetOperation(op.ID, domain.OperationConfirmed, "")
	detail, _ := db.Session(op.SessionID, 0, 1)
	detail.Session.Status = status
	db.Apply(op.SessionID, domain.Event{Type: "session", Session: &detail.Session})
	db.Enqueue(domain.Submission{OperationID: "queued", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "keep queued"})
	db.PauseQueue(op.SessionID, "test")
	device := db.DeviceID()
	db.Close()
	source := filepath.Join(dir, "old", "shioji-worker")
	os.MkdirAll(filepath.Dir(source), 0700)
	in, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	in.Close()
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(source, "--state-dir", dir)
	cmd.Env = append(os.Environ(), "SHIOJI_LEGACY_UNIT_HELPER=source", "SHIOJI_MIGRATION_ENV_VALUE=source-environment-value")
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				cmd.Process.Kill()
				<-done
			}
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, e := os.Stat(filepath.Join(dir, "worker.sock")); e == nil && info.Mode().Perm() == 0600 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("legacy fixture did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return dir, source, device, op.SessionID
}
func TestLegacyMigrationStopsOnlyConfirmedOwnerAndPreservesQueue(t *testing.T) {
	t.Setenv("SHIOJI_LEGACY_UNIT_HELPER", "candidate")
	dir, binary, device, sid := legacyProcessFixture(t, domain.ExecutionCompleted)
	inspection, err := transport.InspectLegacyWorker(context.Background(), dir, device)
	if err != nil || !inspection.Info.Supported || inspection.Info.Busy {
		t.Fatal(inspection, err)
	}
	in := domain.WorkerMigrationInput{ID: "migration", DeviceID: device, OwnerRef: inspection.Info.OwnerRef}
	first, err := StartLegacyMigration(context.Background(), dir, binary, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := StartLegacyMigration(context.Background(), dir, binary, in)
	if err != nil || again.ID != first.ID {
		t.Fatal("duplicate start changed operation", again, err)
	}
	t.Cleanup(func() {
		r, e := readMigration(dir, in.ID)
		if e == nil && r.Runner.PID > 1 {
			if actual, e := transport.WorkerProcess(r.Runner.PID); e == nil && sameWorkerProcess(actual, r.Runner) {
				syscall.Kill(r.Runner.PID, syscall.SIGTERM)
			}
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		state, e := LegacyMigrationStatus(dir, in.ID)
		if e != nil {
			t.Fatal(e)
		}
		if state.Phase == domain.MigrationFailed {
			t.Fatal("migration failed", state.Message)
		}
		if state.Phase == domain.MigrationCompleted {
			if state.Backup == "" {
				t.Fatal("missing backup")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("migration stalled", state)
		}
		time.Sleep(20 * time.Millisecond)
	}
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, device)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if b, e := os.ReadFile(filepath.Join(dir, ".test-environment")); e != nil || string(b) != "source-environment-value" {
		t.Fatal("source startup environment changed", e)
	}
	record, e := os.ReadFile(filepath.Join(dir, "legacy-migrations", in.ID+".json"))
	if e != nil || strings.Contains(string(record), "source-environment-value") {
		t.Fatal("environment leaked into public progress")
	}
	if p, e := migrationEnvironmentPath(dir, in.ID); e != nil {
		t.Fatal(e)
	} else if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("temporary environment snapshot retained after success", e)
	}
	var d domain.SessionDetail
	if err = client.Call(context.Background(), "session", protocol.SessionQuery{ID: sid, Limit: 100}, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 1 || d.Items[0].Text != "do not resend" || len(d.Queue.Items) != 1 || !d.Queue.Paused {
		t.Fatal("migration changed transcript or dispatched queue", d)
	}
}
func TestLegacyMigrationRefusesBusyAndChangedOwnerWithoutStopping(t *testing.T) {
	t.Setenv("SHIOJI_LEGACY_UNIT_HELPER", "candidate")
	dir, binary, device, _ := legacyProcessFixture(t, domain.ExecutionRunning)
	inspection, err := transport.InspectLegacyWorker(context.Background(), dir, device)
	if err != nil {
		t.Fatal(err)
	}
	in := domain.WorkerMigrationInput{ID: "busy", DeviceID: device, OwnerRef: inspection.Info.OwnerRef}
	state, err := StartLegacyMigration(context.Background(), dir, binary, in)
	if err != nil || state.Phase != domain.MigrationBlocked {
		t.Fatal("busy worker was accepted for stopping", state, err)
	}
	if _, err = transport.WorkerProcess(inspection.Owner.PID); err != nil {
		t.Fatal("busy worker was stopped", err)
	}
	in.ID = "changed"
	in.OwnerRef = "changed"
	if _, err = StartLegacyMigration(context.Background(), dir, binary, in); err == nil {
		t.Fatal("accepted stale owner")
	}
}

func TestLegacyMigrationFailureFencesBootstrapAndResumesSameOperation(t *testing.T) {
	t.Setenv("SHIOJI_LEGACY_UNIT_HELPER", "candidate")
	t.Setenv("SHIOJI_LEGACY_FAIL_START", "1")
	dir, binary, device, _ := legacyProcessFixture(t, domain.ExecutionCompleted)
	source, e := transport.InspectLegacyWorker(context.Background(), dir, device)
	if e != nil {
		t.Fatal(e)
	}
	in := domain.WorkerMigrationInput{ID: "resume", DeviceID: device, OwnerRef: source.Info.OwnerRef}
	if _, e = StartLegacyMigration(context.Background(), dir, binary, in); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if r, e := readMigration(dir, in.ID); e == nil && migrationRunnerPresent(r) {
			syscall.Kill(r.Runner.PID, syscall.SIGTERM)
		}
	})
	waitPhase := func(want domain.WorkerMigrationPhase) domain.WorkerMigrationStatus {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			state, e := LegacyMigrationStatus(dir, in.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.Phase == want {
				return state
			}
			if time.Now().After(deadline) {
				t.Fatal("migration did not reach expected phase", want, state)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	failed := waitPhase(domain.MigrationFailed)
	if failed.Backup == "" || !failed.CanResume {
		t.Fatal("failure lost recovery context", failed)
	}
	info, e := transport.EnsureLocalWorker(context.Background(), dir, binary, "")
	if e != nil || info.Migration == nil || info.Migration.ID != in.ID {
		t.Fatal("bootstrap bypassed pending migration", info, e)
	}
	if _, e = os.Stat(filepath.Join(dir, "worker.sock")); !os.IsNotExist(e) {
		t.Fatal("ordinary bootstrap started a replacement", e)
	}
	before, e := os.ReadFile(failed.Backup)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ResumeLegacyMigration(context.Background(), dir, in); e != nil {
		t.Fatal(e)
	}
	done := waitPhase(domain.MigrationCompleted)
	after, e := os.ReadFile(done.Backup)
	if e != nil || string(before) != string(after) {
		t.Fatal("resume overwrote original backup")
	}
	r, e := readMigration(dir, in.ID)
	if e == nil && migrationRunnerPresent(r) {
		syscall.Kill(r.Runner.PID, syscall.SIGTERM)
	}
}

func TestLegacyMigrationStopTimeoutNeverForcesOwner(t *testing.T) {
	t.Setenv("SHIOJI_LEGACY_UNIT_HELPER", "candidate")
	t.Setenv("SHIOJI_LEGACY_IGNORE_TERM", "1")
	dir, binary, device, _ := legacyProcessFixture(t, domain.ExecutionCompleted)
	source, e := transport.InspectLegacyWorker(context.Background(), dir, device)
	if e != nil {
		t.Fatal(e)
	}
	in := domain.WorkerMigrationInput{ID: "timeout", DeviceID: device, OwnerRef: source.Info.OwnerRef}
	if _, e = StartLegacyMigration(context.Background(), dir, binary, in); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		state, e := LegacyMigrationStatus(dir, in.ID)
		if e != nil {
			t.Fatal(e)
		}
		if state.Phase == domain.MigrationFailed {
			if state.CanResume || !strings.Contains(state.Message, "未强制结束") {
				t.Fatal(state)
			}
			current, e := transport.WorkerProcess(source.Owner.PID)
			if e != nil || !sameWorkerProcess(current, source.Owner) {
				t.Fatal("timed-out owner was forced", e)
			}
			if _, e = os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(e) {
				t.Fatal("backed up while old owner still live", e)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stop timeout did not resolve", state)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
