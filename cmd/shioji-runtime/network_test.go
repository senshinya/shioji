package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/tailnet"
	"syscall"
	"testing"
	"time"
)

func TestNetworkHostRunsWithoutWorkerAndOutlivesClient(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "shioji-runtime")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	out, err := exec.Command(bin, "--network-describe").Output()
	if err != nil {
		t.Fatal("runtime does not expose an independent network host", err)
	}
	var described struct {
		Version int
		Role    string
	}
	if json.Unmarshal(out, &described) != nil || described.Version != 1 || described.Role != "network" {
		t.Fatal("incorrect network host description", string(out))
	}
	dir := filepath.Join(root, "identity")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err = exec.Command(bin, "--network-preflight", "--network-dir", dir).CombinedOutput(); err != nil {
		t.Fatal("fresh preflight failed", err, string(out))
	}
	if _, err = os.Stat(filepath.Join(dir, "identity")); !os.IsNotExist(err) {
		t.Fatal("preflight created an identity", err)
	}
	if _, err = exec.Command(bin, "--network-prepare", "--network-dir", dir, "--network-operation", "../invalid").CombinedOutput(); err == nil {
		t.Fatal("invalid preparation operation accepted")
	}
	if _, err = os.Stat(filepath.Join(dir, "launch.lock")); !os.IsNotExist(err) {
		t.Fatal("invalid preparation mutated service state", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "intent.json"), []byte(`{"mode":"paused","hostname":"preserved"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--network-serve", "--network-dir", dir)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Signal(syscall.SIGTERM); cmd.Wait() }()
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan tailnet.Snapshot, 8)
	c := tailnet.NewClient(dir, nil, func(pid int) error {
		if pid != cmd.Process.Pid {
			t.Errorf("unexpected network owner %d", pid)
		}
		return nil
	})
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx, func(s tailnet.Snapshot) { updates <- s }) }()
	defer func() { cancel(); <-done }()
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	for {
		select {
		case s := <-updates:
			if s.State == "Stopped" {
				if _, err := c.Route("test-peer", "100.64.0.1", 22); err == nil {
					goto ready
				}
			}
		case <-timer.C:
			t.Fatal("network host never became available")
		}
	}
ready:
	cancel()
	<-done
	if err = cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("client cancellation terminated network host", err)
	}
	// No worker binary is present beside this host. Its private control socket
	// remains reachable after the GUI-equivalent subscription was closed.
	if _, err = os.Stat(tailnet.ServiceSocket(dir)); err != nil {
		t.Fatal("service socket lost after client exit", err)
	}
}
