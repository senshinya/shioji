package transport_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/testutil"
	"shioji/internal/transport"
	"testing"
	"time"
)

func TestBootstrapLetsTheWorkerUseItsEffectiveCodexEnvironment(t *testing.T) {
	actual, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(root, "worker")
	for _, dir := range []string{parent, child} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(parent, "codex"), []byte("#!/bin/sh\nexit 88\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(actual, filepath.Join(child, "codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", parent+":/usr/bin:/bin")
	adapter := codex.Adapter{Executable: transport.ExistingCodex(), Home: testutil.NewProvider(t).Home}
	configured, err := adapter.WithRuntimeOptions(domain.RuntimeOptions{Environment: []domain.EnvironmentVariable{{Name: "PATH", Value: child + ":/usr/bin:/bin"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runtime, err := configured.Start(ctx, harness.StartOptions{})
	if err != nil {
		t.Fatal("bootstrap pinned a different Codex before worker environment discovery", err)
	}
	defer runtime.Close(context.Background())
}
