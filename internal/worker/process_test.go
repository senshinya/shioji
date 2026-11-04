package worker

import (
	"context"
	"os"
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
