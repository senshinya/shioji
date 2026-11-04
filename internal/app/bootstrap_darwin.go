package app

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// EnsureWorker starts the bundled developer worker only when no local listener exists.
// Its process lifetime is independent of the GUI. Release builds use the runtime service host.
func EnsureWorker(ctx context.Context, directory, binary string) error {
	conn, err := net.DialTimeout("unix", filepath.Join(directory, "worker.sock"), 200*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}
	if binary == "" {
		return errors.New("local worker unavailable; launch shioji-worker")
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	args := []string{"--state-dir", directory}
	codexBinary, _ := exec.LookPath("codex")
	if codexBinary == "" {
		if info, e := os.Stat("/opt/homebrew/bin/codex"); e == nil && !info.IsDir() {
			codexBinary = "/opt/homebrew/bin/codex"
		}
	}
	if codexBinary != "" {
		args = append(args, "--codex", codexBinary)
	}
	cmd := exec.Command(binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err = cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(25 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errors.New("worker did not become reachable; check the execution service")
		case <-ticker.C:
			conn, err = net.DialTimeout("unix", filepath.Join(directory, "worker.sock"), 200*time.Millisecond)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}
