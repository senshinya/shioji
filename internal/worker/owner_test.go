package worker_test

import (
	"net"
	"os"
	"path/filepath"
	"shioji/internal/worker"
	"testing"
)

func TestSecondOwnerCannotReplaceLiveSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "shioji-owner-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	first, err := worker.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	listener, err := first.Listen()
	if err != nil {
		t.Fatal(err)
	}
	second, err := worker.Acquire(dir)
	if second != nil {
		second.Close()
	}
	if err == nil {
		t.Fatal("second owner acquired lock")
	}
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatalf("second owner removed socket: %v", err)
	}
	conn.Close()
	listener.Close()
}
func TestOwnerReclaimsOnlyItsOwnStaleSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "shioji-owner-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	owner, err := worker.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := owner.Listen()
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	owner.Close()
	owner, err = worker.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err = owner.Listen(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode: %v", info.Mode())
	}
}
