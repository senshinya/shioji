package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestPackageDirsPreservesSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "Shioji")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"go.mod":  "module example.com/test\ngo 1.23\n",
		"main.go": "package main\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := &Config{root: dir, Main: "."}
	dirs, err := c.packageDirs(runtime.GOOS, runtime.GOARCH)
	if err != nil || !slices.Contains(dirs, dir) {
		t.Fatalf("packageDirs = %q, %v, want %q", dirs, err, dir)
	}
}

func TestPackageDirFindsIOSOnlyMain(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"go.mod":      "module example.com/ios-app\ngo 1.23\n",
		"main_ios.go": "package main\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := &Config{root: dir, Main: "."}
	got, err := packageDir(c, listEnv("ios", "arm64")...)
	if err != nil || got != dir {
		t.Fatalf("packageDir = %q, %v, want %q", got, err, dir)
	}
}
