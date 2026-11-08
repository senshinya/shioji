package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/harness"
	"shioji/internal/testutil"
	"testing"
	"time"
)

func TestCatalogIdentityTracksInstalledAliasAndRetainsRunningImage(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	next := filepath.Join(dir, "next")
	alias := filepath.Join(dir, "codex")
	if err = os.Symlink(binary, old); err != nil {
		t.Fatal(err)
	}
	// Copies of the small CLI wrapper have distinct installed identities while
	// executing the real installed CLI for initialization and protocol probing.
	for _, path := range []string{old, next} {
		os.Remove(path)
		if err = os.WriteFile(path, []byte("#!/bin/sh\nexec "+binary+" \"$@\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Symlink(old, alias); err != nil {
		t.Fatal(err)
	}
	a := Adapter{Executable: alias, Home: testutil.NewProvider(t).Home}
	source, ok := any(a).(interface{ CatalogVersion() (string, error) })
	if !ok {
		t.Fatal("adapter cannot report installed identity")
	}
	initial, err := source.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := a.Start(ctx, harness.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	running, ok := r.(interface{ CatalogVersion() (string, error) })
	if !ok {
		t.Fatal("runtime cannot identify its captured image")
	}
	captured, err := running.CatalogVersion()
	if err != nil || captured != initial {
		t.Fatal("source and running image differ", initial, captured, err)
	}
	os.Remove(alias)
	if err = os.Symlink(next, alias); err != nil {
		t.Fatal(err)
	}
	os.Remove(old)
	changed, err := source.CatalogVersion()
	if err != nil || changed == initial {
		t.Fatal("normal installation replacement went unnoticed", changed, err)
	}
	retained, err := running.CatalogVersion()
	if err != nil || retained != initial || r.Stopped() {
		t.Fatal("install replacement changed running runtime", retained, err)
	}
}
