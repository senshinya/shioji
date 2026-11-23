package antigravity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestDescriptorPublishesAntigravityIcon(t *testing.T) {
	if got := (Adapter{}).Descriptor().Icon; got != "antigravity" {
		t.Fatalf("descriptor icon = %q, want antigravity", got)
	}
}

func TestInstallationFindsExecutableFromOwningLoginShell(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(bin, "agy")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".zshrc"), []byte("export PATH="+bin+":/usr/bin:/bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("ZDOTDIR", root)
	t.Setenv("PATH", "/usr/bin:/bin")
	a := Adapter{}
	if got := a.Installation(); got.Executable != binary || !got.Configurable || got.Override != "" {
		t.Fatalf("installation = %+v, want %s from login shell", got, binary)
	}
	before, err := a.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 23\n"), 0700); err != nil {
		t.Fatal(err)
	}
	after, err := a.CatalogVersion()
	if err != nil || before == after {
		t.Fatalf("changed installation identity: %q -> %q, %v", before, after, err)
	}
}

func TestMissingOverrideDoesNotFallBackToSystemInstallation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	a, err := (Adapter{}).WithExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = a.Start(ctx, harness.StartOptions{CatalogOnly: true})
	var setup *harness.SetupError
	if !errors.As(err, &setup) || setup.Issue != domain.RuntimeMissing {
		t.Fatalf("expected missing executable, got %v", err)
	}
	if got := a.(harness.InstallationProvider).Installation(); got.Override != path {
		t.Fatalf("override = %q, want %q", got.Override, path)
	}
}
