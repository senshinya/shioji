package worker

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/managedexec"
	"testing"
	"time"
)

func TestShutdownDrainsRuntimeProducersConcurrently(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	release := make(chan struct{})
	defer close(release)
	closing := make([]chan struct{}, 3)
	for i := range 3 {
		d := submitIdleFixture(t, s, w, string(rune('a'+i)), "")
		r := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
		r.closing = make(chan struct{}, 1)
		r.release = release
		closing[i] = r.closing
	}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	deadline := time.After(time.Second)
	for _, started := range closing {
		select {
		case <-started:
		case <-deadline:
			t.Fatal("worker shutdown serializes per-session close budgets")
		}
	}
}

func TestShutdownRetiresRegisteredAuxiliaryProcesses(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	submitIdleFixture(t, s, w, "auxiliary", "")
	manager, err := managedexec.New(filepath.Join(s.config.Directory, "title-executions"), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	var scope *managedexec.Scope
	var cmd *exec.Cmd
	var stdin io.WriteCloser
	var stdout io.Reader
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		scope, err = manager.Begin("auxiliary-orphan")
		if err != nil {
			t.Fatal(err)
		}
		if err = scope.Bind("worker", os.Getpid()); err != nil {
			t.Fatal(err)
		}
		cmd, stdin, stdout, err = scope.StartOwned(ctx, "auxiliary", []string{"/bin/sh", "-c", "sleep 60 & echo ready; wait"}, []string{"PATH=/usr/bin:/bin"}, nil)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal("auxiliary fixture", err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	defer scope.Stop(context.Background())
	if _, err = bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = scope.VerifyStopped(); err != nil {
		t.Fatal("worker drained while auxiliary registered processes remained", err)
	}
}
