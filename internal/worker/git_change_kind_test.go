package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/protocol"
	"testing"
)

func TestChangedFilesKeepIdentitySeparateFromLineCounts(t *testing.T) {
	s, w, dir := gitTestServer(t)
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"deleted-empty", "deleted-binary", "modified-empty"} {
		write(path, nil)
	}
	write("deleted-binary", []byte{0, 1, 2})
	gitTestRun(t, dir, "add", ".")
	gitTestRun(t, dir, "commit", "-m", "base")
	base := gitTestRun(t, dir, "rev-parse", "HEAD")
	gitTestRun(t, dir, "branch", "before-kinds", base)
	for _, path := range []string{"deleted-empty", "deleted-binary"} {
		if err := os.Remove(filepath.Join(dir, path)); err != nil {
			t.Fatal(err)
		}
	}
	write("modified-empty", []byte("only added lines\n"))
	write("added-empty", nil)
	write("added-binary", []byte{0, 3, 4})
	write("untracked-empty", nil)
	write("tab\tname\n.txt", []byte("new\n"))
	gitTestRun(t, dir, "add", "--", "added-empty", "added-binary")
	want := map[string]string{"deleted-empty": "delete", "deleted-binary": "delete", "modified-empty": "update", "added-empty": "add", "added-binary": "add", "untracked-empty": "add", "tab\tname\n.txt": "add"}
	check := func(result map[string]any, reverse bool) {
		t.Helper()
		got := map[string]string{}
		for _, value := range result["files"].([]any) {
			file := value.(map[string]any)
			kind, _ := file["kind"].(string)
			got[file["path"].(string)] = kind
		}
		for path, expected := range want {
			if reverse {
				if expected == "add" {
					expected = "delete"
				} else if expected == "delete" {
					expected = "add"
				}
			}
			if got[path] != expected {
				t.Errorf("file kind must come from Git identity: %q got=%q want=%q (all=%v)", path, got[path], expected, got)
			}
		}
	}
	for _, reverse := range []bool{false, true} {
		from, to := map[string]string{"kind": "head"}, map[string]string{"kind": "worktree"}
		if reverse {
			from, to = to, from
		}
		check(compareTest(t, s, w.ID, from, to), reverse)
	}
	// Legacy callers get the same classification.
	files, err := s.changedFiles(protocol.GitQuery{WorkspaceID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(files)
	var encoded []any
	json.Unmarshal(b, &encoded)
	check(map[string]any{"files": encoded}, false)
	gitTestRun(t, dir, "add", ".")
	gitTestRun(t, dir, "commit", "-m", "target")
	check(compareTest(t, s, w.ID, map[string]string{"kind": "branch", "name": "before-kinds"}, map[string]string{"kind": "head"}), false)
	gitTestRun(t, dir, "checkout", "--orphan", "fresh-kinds")
	for _, value := range compareTest(t, s, w.ID, map[string]string{"kind": "head"}, map[string]string{"kind": "worktree"})["files"].([]any) {
		if file := value.(map[string]any); file["kind"] != "add" {
			t.Error("unborn comparison did not classify a new file", file)
		}
	}
}

func TestUntrackedPathInComparedTreeIsNotMistakenForANewFile(t *testing.T) {
	s, w, dir := gitTestServer(t)
	gitTestRun(t, dir, "rm", "--cached", "file.txt")
	base, target := map[string]string{"kind": "head"}, map[string]string{"kind": "worktree"}
	if files := compareTest(t, s, w.ID, base, target)["files"].([]any); len(files) != 0 {
		t.Fatal("unchanged untracked contents were reported as a new file", files)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("different\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, reverse := range []bool{false, true} {
		from, to := base, target
		if reverse {
			from, to = to, from
		}
		files := compareTest(t, s, w.ID, from, to)["files"].([]any)
		if len(files) != 1 || files[0].(map[string]any)["kind"] != "update" {
			t.Fatal("existing path must remain a modification in either direction", files)
		}
	}
}
