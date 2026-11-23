package systemexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestShellEnvironmentIsCachedAndDeduplicated(t *testing.T) {
	root := t.TempDir()
	counter := filepath.Join(root, "calls")
	rc := fmt.Sprintf("printf 'probe\\n' >> %q\nexport PATH=/fixture/node:/usr/bin:/bin\nprintf 'noisy rc output\\n'\n", counter)
	if err := os.WriteFile(filepath.Join(root, ".zshrc"), []byte(rc), 0600); err != nil {
		t.Fatal(err)
	}
	base := append([]string{"SHELL=/bin/zsh", "ZDOTDIR=" + root}, os.Environ()...)
	cache := &shellEnvironmentCache{}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env, err := cache.get(context.Background(), base)
			if err == nil && !strings.HasPrefix(EnvironmentValue(env, "PATH"), "/fixture/node:") {
				err = fmt.Errorf("missing discovered path")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cache.get(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.Count(string(data), "probe") != 1 {
		t.Fatal("concurrent discovery ran multiple login shells", err)
	}
	if err = os.WriteFile(filepath.Join(root, ".zshrc"), []byte(rc+"export PATH=/changed/node:/usr/bin:/bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env, err := cache.get(context.Background(), base)
	if err != nil || !strings.HasPrefix(EnvironmentValue(env, "PATH"), "/changed/node:") {
		t.Fatal("changed shell configuration retained a stale cache", err)
	}
}

func TestEnvironmentSurvivesTemporaryLoginShellTimeout(t *testing.T) {
	root := t.TempDir()
	shell := filepath.Join(root, "login-shell")
	fail := filepath.Join(root, "shell-unresponsive")
	script := fmt.Sprintf("#!/bin/sh\nif [ -f %q ]; then exec /bin/sleep 30; fi\nexport PATH=%q:/usr/bin:/bin\nexec /bin/sh -c \"$2\"\n", fail, root)
	if err := os.WriteFile(shell, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"SHELL=" + shell, "ZDOTDIR=" + root, "PATH=/usr/bin:/bin"}
	cache := &shellEnvironmentCache{}
	first, err := cache.get(context.Background(), base)
	if err != nil || !strings.HasPrefix(EnvironmentValue(first, "PATH"), root+":") {
		t.Fatalf("login PATH unavailable: %v", err)
	}
	if err := os.WriteFile(fail, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	cache.read.expires = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	observed, err := cache.get(context.Background(), base)
	if err != nil || EnvironmentValue(observed, "PATH") != EnvironmentValue(first, "PATH") {
		t.Fatalf("login shell timeout lost the last usable PATH: %v", err)
	}
}

func TestExpiredLoginEnvironmentStillResolvesNodeAfterShellTimeout(t *testing.T) {
	root := t.TempDir()
	shell := filepath.Join(root, "login-shell")
	fail := filepath.Join(root, "shell-unresponsive")
	node := filepath.Join(root, "node")
	script := fmt.Sprintf("#!/bin/sh\nif [ -f %q ]; then exec /bin/sleep 30; fi\nexport PATH=%q:/usr/bin:/bin\nexport LOGIN_FIXTURE_TOKEN=stale\nexec /bin/sh -c \"$2\"\n", fail, root)
	if err := os.WriteFile(shell, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	base := []string{"SHELL=" + shell, "ZDOTDIR=" + root, "PATH=/usr/bin:/bin"}
	cache := &shellEnvironmentCache{}
	if _, err := cache.get(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fail, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	cache.read.expires = time.Now().Add(-time.Second)
	cache.read.reusableUntil = cache.read.expires
	cache.mu.Unlock()

	observed, err := cache.get(context.Background(), base)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a login shell timeout, got %v", err)
	}
	env := MergeEnvironment(base, observed)
	if resolved, err := LookPath("node", env); err != nil || resolved != node {
		t.Fatalf("shell timeout lost the previously discovered Node.js: %q, %v", resolved, err)
	}
	if EnvironmentValue(env, "LOGIN_FIXTURE_TOKEN") != "" {
		t.Fatal("expired shell credentials were reused")
	}
}

func TestShellEnvironmentCancellationAndTimeoutReleaseItsProcessGroup(t *testing.T) {
	root := t.TempDir()
	shell := filepath.Join(root, "slow-shell")
	pidFile := filepath.Join(root, "child.pid")
	script := fmt.Sprintf("#!/bin/sh\n/bin/sleep 30 &\necho $! > %q\nwait\n", pidFile)
	if err := os.WriteFile(shell, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cache := &shellEnvironmentCache{}
	base := []string{"SHELL=" + shell, "PATH=/usr/bin:/bin"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := cache.get(ctx, base)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("caller cancellation waited for the shell", err)
	}
	cache.mu.Lock()
	read := cache.read
	cache.mu.Unlock()
	select {
	case <-read.done:
	case <-time.After(ShellEnvironmentTimeout + time.Second):
		t.Fatal("shared shell probe exceeded its total budget")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("shell timeout left its child process running", pid)
	}
}
