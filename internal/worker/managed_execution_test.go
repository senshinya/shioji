package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/managedexec"
	"shioji/internal/testutil"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if deviceRecoveryFixture() {
		return
	}
	if workerMaintenanceFixture() {
		return
	}
	if handled, err := managedexec.ChildMain(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if legacyMigrationTestMain() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestManagedAgentCrashStopsOwnedToolAndRecoversWithoutTouchingOtherSession(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-managed-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	config := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "auto-approve"}
	folder, _ := filepath.EvalSymlinks(t.TempDir())
	w, _ := s.store.CreateWorkspace("A", []string{folder})
	op, err := s.submit(domain.Submission{OperationID: "managed-original", WorkspaceID: w.ID, Text: "SHIOJI_RECOVERY_EXEC", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	var tool unix.KinfoProc
	until := time.Now().Add(15 * time.Second)
	for {
		raw, _ := os.ReadFile(filepath.Join(folder, ".shioji-recovery-tool-pid"))
		pid, _ := strconv.Atoi(string(raw))
		if pid > 1 {
			info, e := unix.SysctlKinfoProc("kern.proc.pid", pid)
			if e == nil && info.Proc.P_stat != 5 {
				tool = *info
				break
			}
		}
		if time.Now().After(until) {
			t.Fatal("tool did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() {
		now, e := unix.SysctlKinfoProc("kern.proc.pid", int(tool.Proc.P_pid))
		if e == nil && now.Proc.P_starttime == tool.Proc.P_starttime {
			unix.Kill(-int(tool.Eproc.Pgid), unix.SIGKILL)
		}
	}()
	otherFolder, _ := filepath.EvalSymlinks(t.TempDir())
	otherWorkspace, _ := s.store.CreateWorkspace("B", []string{otherFolder})
	other, err := s.submit(domain.Submission{OperationID: "unrelated", WorkspaceID: otherWorkspace.ID, Text: "SHIOJI_SLOW", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Started:
	case <-time.After(15 * time.Second):
		t.Fatal("B did not start")
	}
	if _, err = s.store.Enqueue(domain.Submission{OperationID: "saved", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "keep waiting", Config: config}); err != nil {
		t.Fatal(err)
	}
	original, _ := s.store.Session(op.SessionID, 0, 100)
	var owner processRecord
	raw, _ := os.ReadFile(filepath.Join(dir, "runtimes", op.SessionID, "runtime-process.json"))
	if err = json.Unmarshal(raw, &owner); err != nil {
		t.Fatal(err)
	}
	if err = unix.Kill(owner.PID, unix.SIGKILL); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(15 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 100)
		if d.Session.Status == "interrupted" && d.Session.Recovery != nil && d.Session.Recovery.CanContinue {
			if d.Session.ThreadID != original.Session.ThreadID || !d.Queue.Paused || len(d.Queue.Items) != 1 {
				t.Fatal("recovery changed context or drained queue")
			}
			break
		}
		if time.Now().After(until) {
			t.Fatal("managed crash did not recover automatically", d.Session.Status, d.Session.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if now, e := unix.SysctlKinfoProc("kern.proc.pid", int(tool.Proc.P_pid)); e == nil && now.Proc.P_starttime == tool.Proc.P_starttime && now.Proc.P_stat != 5 {
		t.Fatal("owned tool survived successful recovery")
	}
	b, _ := s.store.Session(other.SessionID, 0, 1)
	if b.Session.Status != "running" || s.currentRuntime(other.SessionID).Stopped() {
		t.Fatal("A cleanup stopped B")
	}
	if len(p.Inputs()) != 2 {
		t.Fatal("recovery replayed a model input")
	}
	close(p.Release)
}

func TestManagedProofRequiresEveryInputOfTheSourceTurn(t *testing.T) {
	manager, _ := managedexec.New(t.TempDir(), os.Args[0])
	scope, _ := manager.Begin("session")
	scope.Cover("covered")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := scope.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Server{executions: manager}
	source := domain.RecoveryHistory{Inputs: map[string]string{"input:covered": "turn", "input:uncovered": "turn"}}
	if err := s.managedStopProof("session", &source); err != nil {
		t.Fatal(err)
	}
	if source.ManagedStoppedTurns["turn"] {
		t.Fatal("one covered steer acknowledged an uncovered source turn")
	}
}

func TestMissingLiveScopeManifestQuarantinesCompletedSession(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-record-loss-")
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
	if output, e := exec.Command("git", "-C", folder, "init").CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	w, _ := s.store.CreateWorkspace("record loss", []string{folder})
	config := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}
	op, err := s.submit(domain.Submission{OperationID: "completed", WorkspaceID: w.ID, Text: "complete normally", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(10 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 1)
		if d.Session.Status == "completed" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	scope, err := s.executions.Existing(op.SessionID)
	if err != nil || scope == nil {
		t.Fatal(err)
	}
	path := filepath.Join(scope.Directory, "scope.json")
	backup, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer os.WriteFile(path, backup, 0600)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(5 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 1)
		if d.Session.Status == "unknown" && d.Session.ExecutionGuard == scope.Generation {
			break
		}
		if time.Now().After(until) {
			t.Fatal("record loss left completed session unguarded", d.Session)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = s.checkoutGuard(domain.GitStatus{Checkout: folder}, ""); err == nil {
		t.Fatal("record loss released shared checkout")
	}
	if len(p.Inputs()) != 1 {
		t.Fatal("record loss replayed a prompt")
	}
}
