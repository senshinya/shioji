package worker

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/testutil"
	"syscall"
	"testing"
	"time"
)

func TestNewConnectionChecksAgainAfterOldProcessExits(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-reconnect-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, _ := db.CreateWorkspace("Reconnect", []string{folder})
	op, _ := db.Accept(domain.Submission{OperationID: "lost", WorkspaceID: w.ID, Text: "do not replay"})
	db.Close()
	old := exec.Command("/bin/sleep", "60")
	old.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = old.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { old.Process.Kill(); old.Wait() }()
	if err = recordProcess(dir, old.Process.Pid); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), Config{Directory: dir, CodexHome: p.Home})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go s.Serve()
	until := time.Now().Add(10 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 1)
		if d.Session.Recovery != nil && d.Session.Recovery.NeedsStop {
			break
		}
		if time.Now().After(until) {
			t.Fatal("initial automatic check did not discover previous owner")
		}
		time.Sleep(10 * time.Millisecond)
	}
	old.Process.Kill()
	old.Wait()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var snapshot domain.Snapshot
	if err = c.Call(context.Background(), "snapshot", nil, &snapshot); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(5 * time.Second)
	for {
		d, _ := s.store.Session(op.SessionID, 0, 1)
		info := d.Session.Recovery
		if info != nil && info.Phase == "attention" && !info.NeedsStop && info.CanEnd {
			break
		}
		if time.Now().After(until) {
			t.Fatal("reconnection kept a stale previous-process result")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(p.Inputs()) != 0 {
		t.Fatal("reconnection replayed old user input")
	}
	// Repeated snapshots on one connection do not restart the same check.
	before, _ := s.store.Session(op.SessionID, 0, 1)
	for i := 0; i < 20; i++ {
		if err = c.Call(context.Background(), "snapshot", nil, &snapshot); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(30 * time.Millisecond)
	after, _ := s.store.Session(op.SessionID, 0, 1)
	if after.Session.Revision != before.Session.Revision {
		t.Fatal("snapshot polling repeatedly rewrote recovery progress")
	}
}

func TestExitedIdleRuntimeIsRestartedWithoutSendingAnyInput(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-idle-restart-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, CodexHome: p.Home})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := s.currentRuntime()
	old.Close()
	until := time.Now().Add(5 * time.Second)
	for {
		r := s.currentRuntime()
		if r != nil && r != old && r.Readiness().State == "ready" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("idle runtime exit left worker permanently unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(p.Inputs()) != 0 {
		t.Fatal("runtime restart sent a model prompt")
	}
}
