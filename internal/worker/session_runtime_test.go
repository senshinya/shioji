package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/testutil"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Two real pinned app-servers with a deterministic provider. A loss must neither
// stop B nor submit the interrupted prompt/paused queue again.
func TestSessionRuntimeCrashIsIsolatedAndRecoversManagedExecution(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-isolated-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}
	var ops []domain.Operation
	for _, name := range []string{"a", "b"} {
		folder, _ := filepath.EvalSymlinks(t.TempDir())
		w, e := s.store.CreateWorkspace(name, []string{folder})
		if e != nil {
			t.Fatal(e)
		}
		op, e := s.submit(domain.Submission{OperationID: name, WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg})
		if e != nil {
			t.Fatal(e)
		}
		ops = append(ops, op)
		select {
		case <-p.Started:
		case <-time.After(10 * time.Second):
			t.Fatal("session did not start")
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "runtimes", "*", "runtime-process.json"))
	if len(files) != 2 {
		t.Fatalf("want two launch-time session ownership records, got %d", len(files))
	}
	var a, b processRecord
	for i, op := range ops {
		raw, e := os.ReadFile(filepath.Join(dir, "runtimes", op.SessionID, "runtime-process.json"))
		if e != nil {
			t.Fatal(e)
		}
		var r processRecord
		if e = json.Unmarshal(raw, &r); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			a = r
		} else {
			b = r
		}
	}
	if a.PID == b.PID {
		t.Fatal("sessions share a runtime")
	}
	d, _ := s.store.Session(ops[0].SessionID, 0, 1)
	originalThread := d.Session.ThreadID
	_, err = s.store.Enqueue(domain.Submission{OperationID: "keep-queue", SessionID: ops[0].SessionID, WorkspaceID: d.Session.WorkspaceID, Text: "never replay", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(a.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		d, _ = s.store.Session(ops[0].SessionID, 0, 100)
		if d.Session.Status == "interrupted" && d.Session.Recovery != nil && d.Session.Recovery.CanContinue {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("managed history recovery did not finish: %+v", d.Session)
		}
		time.Sleep(20 * time.Millisecond)
	}
	other, _ := s.store.Session(ops[1].SessionID, 0, 100)
	if other.Session.Status != "running" || syscall.Kill(b.PID, 0) != nil {
		t.Fatal("A recovery affected B", other.Session)
	}
	if len(p.Inputs()) != 2 || !d.Queue.Paused || len(d.Queue.Items) != 1 || d.Session.ThreadID != originalThread {
		t.Fatal("recovery changed history or replayed queue")
	}
	close(p.Release)
	deadline = time.Now().Add(10 * time.Second)
	for {
		other, _ = s.store.Session(ops[1].SessionID, 0, 100)
		if other.Session.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("B stopped receiving events")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A hard worker crash closes transport pipes, but cannot be treated as evidence
// that a separately grouped shell stopped. Reopening preserves identity/queue.
func TestWorkerCrashKeepsBothSessionHistoriesAndDoesNotReplayTools(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-worker-crash-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cfg := Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}}
	raw, _ := json.Marshal(struct{ Directory, Home string }{Directory: dir, Home: p.Home})
	child := exec.Command(os.Args[0], "-test.run=^TestSessionWorkerCrashHelper$")
	child.Env = append(os.Environ(), "SHIOJI_RECOVERY_TEST_CONFIG="+string(raw))
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	var client *protocol.Client
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, e := net.Dial("unix", filepath.Join(dir, "worker.sock"))
		if e == nil {
			client, e = protocol.Connect(context.Background(), conn, "")
			if e == nil {
				break
			}
			conn.Close()
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated worker did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer client.Close()
	call := func(method string, in, out any) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if e := client.Call(ctx, method, in, out); e != nil {
			t.Fatal(e)
		}
	}
	var ready domain.Readiness
	call("readiness", nil, &ready)
	run := domain.RunConfig{Model: ready.Models[0].ID, Permission: "auto-approve"}
	var originals []domain.SessionDetail
	var toolPID int
	for i, text := range []string{"SHIOJI_RECOVERY_EXEC", "SHIOJI_SLOW", "ordinary completed turn"} {
		folder, _ := filepath.EvalSymlinks(t.TempDir())
		var w domain.Workspace
		call("workspace.create", protocol.WorkspaceInput{Name: fmt.Sprint(i), Folders: []string{folder}}, &w)
		var op domain.Operation
		call("session.submit", domain.Submission{OperationID: fmt.Sprint("crash-", i), WorkspaceID: w.ID, Text: text, Config: run}, &op)
		until := time.Now().Add(10 * time.Second)
		var d domain.SessionDetail
		for {
			call("session", protocol.SessionQuery{ID: op.SessionID}, &d)
			if d.Session.ThreadID != "" && (i < 2 && d.Session.Status == "running" || i == 2 && d.Session.Status == "completed") {
				break
			}
			if time.Now().After(until) {
				t.Fatal("turn not started")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if i == 0 {
			for {
				b, _ := os.ReadFile(filepath.Join(folder, ".shioji-recovery-tool-pid"))
				toolPID, _ = strconv.Atoi(string(b))
				if toolPID > 1 {
					break
				}
				if time.Now().After(until) {
					t.Fatal("tool did not start")
				}
				time.Sleep(20 * time.Millisecond)
			}

		} else if i == 1 {
			select {
			case <-p.Started:
			case <-time.After(10 * time.Second):
				t.Fatal("second model request did not start")
			}
		}
		var q domain.QueueState
		call("turn.enqueue", domain.Submission{OperationID: fmt.Sprint("queued-", i), SessionID: op.SessionID, WorkspaceID: w.ID, Text: "keep waiting", Config: run}, &q)
		originals = append(originals, d)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	client.Close()
	if os.Getenv("SHIOJI_LONG_RECOVERY_TEST") == "1" {
		time.Sleep(32 * time.Second)
	}
	s, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, original := range originals {
		if original.Session.Status == "completed" {
			scope, e := s.executions.Existing(original.Session.ID)
			if e != nil || scope == nil {
				t.Fatal(e)
			}
			if e = scope.VerifyStopped(); e != nil {
				t.Fatal("completed session ownership was not cleaned at worker startup", e)
			}
			d, e := s.store.Session(original.Session.ID, 0, 100)
			if e != nil || d.Session.Status != "completed" || d.Session.ThreadID != original.Session.ThreadID || !d.Queue.Paused || len(d.Queue.Items) != 1 {
				t.Fatal("idle recovery changed history or queue", d, e)
			}
			continue
		}
		deadline = time.Now().Add(15 * time.Second)
		for {
			d, e := s.store.Session(original.Session.ID, 0, 100)
			if e != nil {
				t.Fatal(e)
			}
			if d.Session.Recovery != nil && d.Session.Recovery.Phase == "ready" {
				if d.Session.Status != "interrupted" || d.Session.ThreadID != original.Session.ThreadID || d.Session.Context.CWD != original.Session.Context.CWD || !d.Queue.Paused || len(d.Queue.Items) != 1 {
					t.Fatal("worker restart changed context or dropped paused queue", d.Session, d.Queue)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("automatic worker restart check did not finish")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if len(p.Inputs()) != 3 {
		t.Fatal("worker restart replayed a prompt or queue", len(p.Inputs()))
	}
}

func TestSessionWorkerCrashHelper(t *testing.T) {
	raw := os.Getenv("SHIOJI_RECOVERY_TEST_CONFIG")
	if raw == "" {
		return
	}
	var launch struct{ Directory, Home string }
	if err := json.Unmarshal([]byte(raw), &launch); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), Config{Directory: launch.Directory, Harnesses: []harness.Adapter{codex.Adapter{Home: launch.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Serve(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryStopsOnlyItsOwnRuntimeWhenThreadBindingIsMissing(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-unbound-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}
	var ids []string
	for _, name := range []string{"lost-binding", "unrelated"} {
		folder, _ := filepath.EvalSymlinks(t.TempDir())
		w, e := s.store.CreateWorkspace(name, []string{folder})
		if e != nil {
			t.Fatal(e)
		}
		op, e := s.submit(domain.Submission{OperationID: name, WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg})
		if e != nil {
			t.Fatal(e)
		}
		select {
		case <-p.Started:
		case <-time.After(10 * time.Second):
			t.Fatal("provider not started")
		}
		ids = append(ids, op.SessionID)
	}
	original := s.currentRuntime(ids[0])
	other := s.currentRuntime(ids[1])
	catalog := s.currentRuntime()
	s.dispatchMu.Lock()
	d, _ := s.store.Session(ids[0], 0, 1)
	d.Session.ThreadID = ""
	d.Session.Status = "unknown"
	_, err = s.store.Apply(ids[0], domain.Event{Type: "session", Session: &d.Session})
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.reconcile(protocol.RecoveryInput{SessionID: ids[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !original.Stopped() || result.NeedsStop || !result.CanEnd {
		t.Fatal("lost binding hides the owned root or allows ending it while alive", result)
	}
	if other.Stopped() || catalog.Stopped() || result.Status != "unknown" || !result.CanEnd {
		t.Fatal("missing source binding released context or crossed session boundary", result)
	}
	close(p.Release)
}

func TestKnownSessionLaunchFailurePreservesInputAndAllowsExplicitRetry(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-start-failure-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	folder, _ := filepath.EvalSymlinks(t.TempDir())
	w, _ := s.store.CreateWorkspace("retry", []string{folder})
	cfg := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}
	s.dispatchMu.Lock()
	s.adapters[s.config.DefaultHarnessID] = codex.Adapter{Home: p.Home, Executable: filepath.Join(dir, "not-installed")}
	s.dispatchMu.Unlock()
	op, err := s.submit(domain.Submission{OperationID: "failed-launch", WorkspaceID: w.ID, Text: "preserve original text", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 100)
		if d.Session.Status == "failed" && len(d.Items) > 0 && d.Items[0].Status == "failed" {
			if len(d.Items) != 1 || d.Items[0].Text != "preserve original text" || d.Items[0].Status != "failed" || !d.Queue.Paused {
				t.Fatal("launch failure lost accepted input", d)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("launch failure did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	receipt, _ := s.store.Operation(op.ID)
	if receipt.State != "rejected" || len(p.Inputs()) != 0 {
		t.Fatal("known unsent input was not rejected", receipt)
	}
	s.dispatchMu.Lock()
	s.adapters[s.config.DefaultHarnessID] = codex.Adapter{Home: p.Home}
	s.dispatchMu.Unlock()
	_, err = s.submit(domain.Submission{OperationID: "explicit-retry", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "new explicit input", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 100)
		if d.Session.Status == "completed" {
			break
		}
		if d.Session.Status == "failed" || time.Now().After(deadline) {
			t.Fatal("known launch failure permanently poisoned the session", d.Session)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(p.Inputs()) != 1 {
		t.Fatal("retry replayed the old input")
	}
}

func TestParallelIdenticalToolsAreAssociatedWithTheirOwnSessionRoots(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-two-tools-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "auto-approve"}
	var ids []string
	for i, text := range []string{"SHIOJI_RECOVERY_EXEC", "SHIOJI_RECOVERY_EXEC_SECOND"} {
		folder, _ := filepath.EvalSymlinks(t.TempDir())
		w, _ := s.store.CreateWorkspace(fmt.Sprint(i), []string{folder})
		op, e := s.submit(domain.Submission{OperationID: fmt.Sprint("tool-", i), WorkspaceID: w.ID, Text: text, Config: cfg})
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, op.SessionID)
		until := time.Now().Add(10 * time.Second)
		for {
			raw, _ := os.ReadFile(filepath.Join(folder, ".shioji-recovery-tool-pid"))
			pid, _ := strconv.Atoi(string(raw))
			if pid > 1 {
				defer syscall.Kill(-pid, syscall.SIGTERM)
				break
			}
			if time.Now().After(until) {
				t.Fatal("tool did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	groups := map[int]bool{}
	for _, id := range ids {
		scope, e := s.executions.Existing(id)
		if e != nil || scope == nil {
			t.Fatal(e)
		}
		files, _ := filepath.Glob(filepath.Join(scope.Directory, scope.Generation, "launches", "*.json"))
		found := false
		for _, path := range files {
			var record managedexec.Ticket
			raw, _ := os.ReadFile(path)
			json.Unmarshal(raw, &record)
			if record.Kind == "executor" && record.State == "registered" {
				if groups[record.Group] {
					t.Fatal("parallel tools share an execution group")
				}
				groups[record.Group], found = true, true
			}
		}
		if !found {
			t.Fatal("tool execution had no pre-execution ownership record")
		}
	}
}
