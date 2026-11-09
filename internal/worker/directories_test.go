package worker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type directoryPageFixture struct {
	Path, Parent, Home string
	Entries            []struct{ Name, Path string }
	Next               int
	More               bool
}

func directoryPage(t *testing.T, s *Server, in any) directoryPageFixture {
	t.Helper()
	b, err := fileCommand(t, s, "device.directories", in)
	if err != nil {
		t.Fatal(err)
	}
	var out directoryPageFixture
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestDirectoryBrowserReadsOwningDeviceFolders(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "alpha"})
	root := t.TempDir()
	for _, name := range []string{"tree", ".hidden", "Archive"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "file.go"), []byte("package main"), 0600)
	os.Symlink(filepath.Join(root, "tree"), filepath.Join(root, "linked"))
	page := directoryPage(t, s, map[string]any{"path": root})
	if page.Path != root || page.Parent != filepath.Dir(root) || page.Home == "" || len(page.Entries) != 3 || page.Entries[0].Name != "Archive" || page.Entries[1].Name != "linked" {
		t.Fatal(page)
	}
	for _, entry := range page.Entries {
		if entry.Path != filepath.Join(root, entry.Name) {
			t.Fatal("returned a client-relative path", entry)
		}
	}
	hidden := directoryPage(t, s, map[string]any{"path": root, "hidden": true})
	if len(hidden.Entries) != 4 || hidden.Entries[0].Name != ".hidden" {
		t.Fatal(hidden)
	}
	empty := directoryPage(t, s, map[string]any{"path": filepath.Join(root, "tree")})
	if len(empty.Entries) != 0 || empty.More {
		t.Fatal(empty)
	}
	home := directoryPage(t, s, map[string]any{})
	if home.Path != home.Home {
		t.Fatal("default directory does not belong to the worker", home)
	}
	for _, in := range []map[string]any{{"path": "relative"}, {"path": filepath.Join(root, "file.go")}, {"path": filepath.Join(root, "missing")}, {"path": root, "offset": -1}} {
		if _, err := fileCommand(t, s, "device.directories", in); err == nil {
			t.Fatal("invalid directory accepted", in)
		}
	}
}
func TestDirectoryBrowserPaginatesWithoutLosingFolders(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "alpha"})
	root := t.TempDir()
	for i := 0; i < 215; i++ {
		os.Mkdir(filepath.Join(root, fmt.Sprintf("folder-%03d", i)), 0700)
	}
	first := directoryPage(t, s, map[string]any{"path": root})
	if !first.More || len(first.Entries) != 200 || first.Next != 200 {
		t.Fatal(first)
	}
	last := directoryPage(t, s, map[string]any{"path": root, "offset": first.Next})
	if last.More || len(last.Entries) != 15 || last.Entries[0].Name != "folder-200" {
		t.Fatal(last)
	}
}
