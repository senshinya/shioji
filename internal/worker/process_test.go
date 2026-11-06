package worker

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestExistingProcessOwnershipRecordBlocksNewHarnessWithoutKillingIt(t *testing.T) {
	dir, err := os.MkdirTemp("", "shioji-process-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err = recordProcess(dir, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err = checkPreviousProcess(dir); err == nil {
		t.Fatal("live recorded process was silently replaced")
	}
	s, err := New(context.Background(), Config{Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.runtime != nil {
		t.Fatal("new runtime started over an existing owner")
	}
	if s.readiness.State != "unavailable" {
		t.Fatalf("readiness=%+v", s.readiness)
	}
}

func TestRecoveryDoesNotDeclareAnEscapedMemberStopped(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	p, err := unix.SysctlKinfoProc("kern.proc.pid", cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	r := recoveryToolProcess{Group: cmd.Process.Pid + 1000000, Members: []processRecord{processIdentity(*p)}}
	if recoveryToolState(r) != "unknown" {
		t.Fatal("a live recorded member outside its former group was called stopped")
	}
	if err = stopRecoveryTool(context.Background(), r); err == nil {
		t.Fatal("escaped membership granted a successful stop")
	}
}

func TestOwnedToolMembersRetainsIdentitiesAfterGroupChanges(t *testing.T) {
	a := unix.KinfoProc{}
	a.Proc.P_pid = 101
	a.Proc.P_starttime.Sec = 100
	a.Eproc.Pgid = 101
	b := unix.KinfoProc{}
	b.Proc.P_pid = 102
	b.Proc.P_starttime.Sec = 100
	b.Eproc.Pgid = 102
	r := recoveryToolProcess{Group: 101, Members: []processRecord{processIdentity(a), processIdentity(b)}}
	members, ok := ownedToolMembers(r, []unix.KinfoProc{a, b})
	if !ok || len(members) != 2 {
		t.Fatal("watcher forgot a previously recorded escaped member", members)
	}
}

func TestExitedRuntimeOwnershipRecordDoesNotBlockRestart(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	if err := recordProcess(dir, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	if err := checkPreviousProcess(dir); err != nil {
		t.Fatal("a confirmed dead runtime prevents worker recovery", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runtime-process.json")); !os.IsNotExist(err) {
		t.Fatal("dead runtime ownership record was not cleared", err)
	}
}

func TestExplicitRecoveryStopOnlySignalsVerifiedDedicatedProcessGroup(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	if err := recordProcess(dir, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { cmd.Wait(); close(reaped) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := stopPreviousProcess(ctx, dir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reaped:
	case <-ctx.Done():
		t.Fatal("verified predecessor was not stopped")
	}
	if err := recordProcess(dir, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := stopPreviousProcess(ctx, dir); err == nil {
		t.Fatal("stop authorized a process outside the owned app-server group")
	}
}
