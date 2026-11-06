package worker

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRecoveryDisplaysTheLiveMemberAfterItsGroupLeaderExits(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child")
	cmd := exec.Command("/bin/sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "recovery-test", pidFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); cmd.Wait() }()
	var child int
	deadline := time.Now().Add(time.Second)
	for {
		b, _ := os.ReadFile(pidFile)
		child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		if child > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child not started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	parent, e := unix.SysctlKinfoProc("kern.proc.pid", cmd.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	childInfo, e := unix.SysctlKinfoProc("kern.proc.pid", child)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(dir, "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	w, e := db.CreateWorkspace("Member", []string{dir})
	if e != nil {
		t.Fatal(e)
	}
	op, e := db.Accept(domain.Submission{OperationID: "member", WorkspaceID: w.ID, Text: "wait"})
	if e != nil {
		t.Fatal(e)
	}
	turn := domain.Turn{ID: "turn", Status: "unknown"}
	db.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &turn})
	r := recoveryToolProcess{Session: op.SessionID, Turn: "turn", Item: "item", Command: "source command", Group: cmd.Process.Pid, Root: processIdentity(*parent), Members: []processRecord{processIdentity(*parent), processIdentity(*childInfo)}}
	if e = saveToolRecord(dir, r); e != nil {
		t.Fatal(e)
	}
	cmd.Process.Kill()
	cmd.Wait()
	d, e := db.Session(op.SessionID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	s := Server{store: db, config: Config{Directory: dir}}
	tasks, canStop := s.recoveryTasks(d, domain.RecoveryHistory{Available: true})
	if len(tasks) != 1 || !canStop || tasks[0].PID != child || tasks[0].ProcessGroup != cmd.Process.Pid || tasks[0].ProcessCommand != "sleep 30" {
		t.Fatal("confirmation did not display the actual living member", tasks)
	}
}

func TestRecoverySamplerRetiresFinishedGroupsAndKeepsDiskEvidence(t *testing.T) {
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	r := recoveryToolProcess{Session: "s", Item: "tool", Command: "old", Group: 999999, Root: processRecord{PID: 999999, StartedSec: 1}, Members: []processRecord{{PID: 999999, StartedSec: 1}}}
	if e = saveToolRecord(dir, r); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := Server{ctx: ctx, store: db, config: Config{Directory: dir}, tools: map[string]recoveryToolProcess{"job": r}}
	s.wg.Add(1)
	go s.watchRecoveryTools()
	defer func() { cancel(); s.wg.Wait() }()
	deadline := time.Now().Add(time.Second)
	for {
		s.toolMu.Lock()
		remaining := len(s.tools)
		s.toolMu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finished task is sampled indefinitely")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, e = readRecoveryTool(dir, r.Session, r.Item, r.Command); e != nil {
		t.Fatal("stopping sampling discarded recovery evidence", e)
	}
}

func TestRecoveryTasksIncludesUnknownTurnsBeyondTheVisibleHistoryPage(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := db.CreateWorkspace("History", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "history", WorkspaceID: w.ID, Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 110; i++ {
		item := domain.Item{ID: fmt.Sprint("old-", i), TurnID: "old", Kind: "assistant", Text: "old", Final: true}
		if _, err = db.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
			t.Fatal(err)
		}
	}
	late := domain.Turn{ID: "late", Status: "unknown"}
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &late}); err != nil {
		t.Fatal(err)
	}
	item := domain.Item{ID: "late-tool", TurnID: "late", Kind: "tool", Presentation: "command", Name: "sleep 60", Status: "running"}
	if _, err = db.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &item}); err != nil {
		t.Fatal(err)
	}
	d, err := db.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	s := Server{store: db, config: Config{Directory: dir}}
	tasks, _ := s.recoveryTasks(d, domain.RecoveryHistory{Available: true, Items: []domain.Item{item}})
	if len(tasks) != 1 || tasks[0].Command != "sleep 60" {
		t.Fatal("late unknown turn's task was lost behind history pagination", tasks)
	}
}
