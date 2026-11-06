package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
