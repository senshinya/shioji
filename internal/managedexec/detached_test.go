package managedexec

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDetachedGenerationCanBeReplacedAndRetriedIndependently(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, os.Args[0])
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, stdin, _, err := old.StartOwned(ctx, "resident", []string{"/bin/sleep", "60"}, os.Environ(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	defer old.Stop(context.Background())
	stopped, stop := context.WithCancel(ctx)
	stop()
	if err = old.Terminate(stopped, true); err == nil {
		t.Fatal("cancelled cleanup unexpectedly succeeded")
	}
	detacher, ok := any(old).(interface{ Detach() error })
	if !ok {
		t.Fatal("failed generations cannot be detached for independent recovery")
	}
	if err = detacher.Detach(); err != nil {
		t.Fatal(err)
	}
	next, err := m.Begin("session")
	if err != nil || next.Generation == old.Generation {
		t.Fatal("old cleanup blocks a fresh generation", next, err)
	}
	if err = next.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	newCmd, newStdin, _, err := next.StartOwned(ctx, "successor", []string{"/bin/sleep", "60"}, os.Environ(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer newCmd.Wait()
	defer newStdin.Close()
	defer next.Stop(context.Background())
	if !next.Admitting() || old.Admitting() {
		t.Fatal("retirement reopened the wrong generation")
	}
	reopened, err := New(dir, os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := any(reopened).(interface{ Detached() ([]*Scope, error) })
	if !ok {
		t.Fatal("detached cleanup is lost when the worker restarts")
	}
	pending, err := reader.Detached()
	if err != nil || len(pending) != 1 || pending[0].Generation != old.Generation {
		t.Fatal("detached ownership was not retained", pending, err)
	}
	if err = pending[0].Terminate(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = old.VerifyStopped(); err != nil || !next.Admitting() {
		t.Fatal("old retry altered the successor", err)
	}
	if next.VerifyStopped() == nil {
		t.Fatal("old cleanup stopped the successor's registered process")
	}
}
