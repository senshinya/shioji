package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/managedexec"
	"shioji/internal/store"
	"testing"
)

func TestHostDoesNotTreatLifetimeCrashCountAsFailureWindow(t *testing.T) {
	dir, o := regressionOptions(t)
	// A previous process's lifetime count is not evidence of ten failures in
	// this supervisor's five-minute window.
	if err := atomicJSON(filepath.Join(dir, "supervisor", "health.json"), map[string]any{"Phase": "ready", "DeviceID": "fixture-device", "BuildID": "old", "Schema": store.SchemaVersion, "Attempts": 3}); err != nil {
		t.Fatal(err)
	}
	regressionHost(t, o, "ready")
	pid := fixtureReadyPID(t, dir, 0)
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Kill(); err != nil {
		t.Fatal(err)
	}
	fixtureReadyPID(t, dir, pid)
}

func TestColdRepairChecksDetachedGenerations(t *testing.T) {
	dir := t.TempDir()
	m, err := managedexec.New(filepath.Join(dir, "executions"), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	old, err := m.Begin("session")
	if err != nil {
		t.Fatal(err)
	}
	if err = old.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ticket, err := old.Prepare("worker", "old-process")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(old.Directory, old.Generation, "launches", ticket.ID+".json"), []byte("unreadable ownership"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = old.Detach(); err != nil {
		t.Fatal(err)
	}
	current, err := m.Begin("session")
	if err != nil {
		t.Fatal(err)
	}
	if err = current.Fence(); err != nil {
		t.Fatal(err)
	}
	h := &host{options: Options{Directory: dir}}
	if err = h.verifyRepairScopes(context.Background()); err == nil {
		t.Fatal("cold repair ignored an unconfirmed detached generation")
	}
}
