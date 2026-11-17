package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/transport"
	"testing"
)

func TestHandoffKnownBackgroundPreflightFailureRestoresAdmission(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "sh-post-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	directory := filepath.Join(home, "Library", "Application Support", "Shioji", "worker")
	s, err := New(context.Background(), Config{Directory: directory, Harnesses: []harness.Adapter{idleHarness{}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.dispatchMu.Lock()
	s.pressureSample = func() uint32 { return 1 }
	s.dispatchMu.Unlock()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.store.CreateWorkspace("test", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	d := submitIdleFixture(t, s, w, "idle", "")
	scope, err := s.executions.Existing(d.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	// An invalid isolated application fails signature verification before any helper or SMAppService call.
	target := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", "Shioji Runtime.app")
	if err = os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	s.build.Managed = true
	calls := 0
	s.config.DisableBackground = func(ctx context.Context) (domain.BackgroundService, error) {
		calls++
		return transport.DisableRuntimeService(ctx, directory, "/nonexistent/test-worker")
	}
	first, firstErr := s.disableBackground()
	if firstErr == nil {
		t.Fatal("invalid bundle unexpectedly passed preflight", first)
	}
	op, submitErr := s.submit(domain.Submission{OperationID: "after-preflight", WorkspaceID: w.ID, Text: "continue", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if submitErr == nil {
		awaitReceipt(t, s, op.ID)
	}
	second, secondErr := s.disableBackground()
	t.Logf("known pre-helper failure: first=%+v err=%v stopping=%v scopeAdmitting=%v nextInput=%v retry=%+v retryErr=%v callbacks=%d", first, firstErr, s.backgroundStopping.Load(), scope.Admitting(), submitErr, second, secondErr, calls)
	if s.backgroundStopping.Load() || !scope.Admitting() || submitErr != nil || calls != 2 {
		t.Fatal("known non-operation permanently closed worker admission and prevented retry")
	}
}

func TestBackgroundDisableUnknownFailureKeepsAdmissionFenced(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "idle", "")
	scope, err := s.executions.Existing(d.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.build.Managed = true
	calls := 0
	s.config.DisableBackground = func(context.Context) (domain.BackgroundService, error) {
		calls++
		return domain.BackgroundService{}, errors.New("helper outcome unknown")
	}
	if _, err = s.disableBackground(); err == nil {
		t.Fatal("unknown helper result was accepted")
	}
	if !s.backgroundStopping.Load() || scope.Admitting() {
		t.Fatal("unknown unregister outcome reopened execution")
	}
	if _, err = s.disableBackground(); err == nil || calls != 1 {
		t.Fatal("unknown unregister was replayed", calls, err)
	}
}
