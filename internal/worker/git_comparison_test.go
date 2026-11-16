package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/protocol"
	"strings"
	"testing"
)

func TestNonRepositoryComparisonReturnsTypedError(t *testing.T) {
	s, w, dir := gitTestServer(t)
	if err := os.Rename(filepath.Join(dir, ".git"), filepath.Join(dir, "git-metadata")); err != nil {
		t.Fatal(err)
	}
	_, err := s.compareGit(protocol.GitQuery{WorkspaceID: w.ID})
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) || remote.Code != "notGitRepository" {
		t.Fatalf("non-Git comparison must identify unavailable review: %v", err)
	}
	gitTestRun(t, dir, "init", "-b", "main")
	result, err := s.compareGit(protocol.GitQuery{WorkspaceID: w.ID})
	if err != nil || len(result.Files) == 0 {
		t.Fatalf("initializing Git must restore comparison: %+v %v", result, err)
	}
}

func compareTest(t *testing.T, s *Server, workspace string, base, target map[string]string) map[string]any {
	t.Helper()
	out, err := gitTestCall(s, "git.compare", map[string]any{"workspaceId": workspace, "comparison": map[string]any{"base": base, "target": target}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEscapedDiffPreviewReturnsCompactLimitError(t *testing.T) {
	s, w, dir := gitTestServer(t)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(strings.Repeat("<", 200<<10)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"git.file", "git.compare"} {
		_, err := gitTestCall(s, method, map[string]any{"workspaceId": w.ID, "path": "file.txt", "reviewPath": "file.txt", "comparison": map[string]any{"base": map[string]string{"kind": "head"}, "target": map[string]string{"kind": "worktree"}}})
		if err == nil || !strings.Contains(err.Error(), "预览内容超过") {
			t.Fatalf("%s should return a compact preview limit error, got %v", method, err)
		}
	}
}
func TestComparisonPinsBranchesAndNeverChecksOut(t *testing.T) {
	s, w, dir := gitTestServer(t)
	old := gitTestRun(t, dir, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("branch\n"), 0600)
	gitTestRun(t, dir, "add", "file.txt")
	gitTestRun(t, dir, "commit", "-m", "branch")
	newer := gitTestRun(t, dir, "rev-parse", "HEAD")
	gitTestRun(t, dir, "branch", "feat/rotation")
	gitTestRun(t, dir, "reset", "--hard", old)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("working\n"), 0600)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked\n"), 0600)
	before := gitTestRun(t, dir, "status", "--porcelain=v1")
	result := compareTest(t, s, w.ID, map[string]string{"kind": "branch", "name": "main"}, map[string]string{"kind": "branch", "name": "feat/rotation"})
	cmp := result["comparison"].(map[string]any)
	if cmp["base"].(map[string]any)["commit"] != old || cmp["target"].(map[string]any)["commit"] != newer {
		t.Fatalf("unresolved pair %v", cmp)
	}
	gitTestRun(t, dir, "branch", "-f", "feat/rotation", old)
	file, err := gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "file.txt", "comparison": cmp})
	if err != nil || file["old"] != "original\n" || file["new"] != "branch\n" {
		t.Fatalf("fixed versions %v %v", file, err)
	}
	if gitTestRun(t, dir, "branch", "--show-current") != "main" || gitTestRun(t, dir, "status", "--porcelain=v1") != before {
		t.Fatal("comparison changed checkout or files")
	}
	reverse := map[string]any{"repository": cmp["repository"], "base": cmp["target"], "target": cmp["base"]}
	file, err = gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "file.txt", "comparison": reverse})
	if err != nil || file["old"] != "branch\n" || file["new"] != "original\n" {
		t.Fatalf("reverse %v %v", file, err)
	}
}
func TestComparisonWorktreeEmptyAndRemoteRefs(t *testing.T) {
	s, w, dir := gitTestServer(t)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("working\n"), 0600)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked\n"), 0600)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored\n"), 0600)
	os.WriteFile(filepath.Join(dir, "ignored"), []byte("private\n"), 0600)
	result := compareTest(t, s, w.ID, map[string]string{"kind": "head"}, map[string]string{"kind": "worktree"})
	encoded, _ := json.Marshal(result["files"])
	if !strings.Contains(string(encoded), "new.txt") || strings.Contains(string(encoded), "ignored") {
		t.Fatalf("files %s", encoded)
	}
	same := compareTest(t, s, w.ID, map[string]string{"kind": "branch", "name": "main"}, map[string]string{"kind": "head"})
	if len(same["files"].([]any)) != 0 {
		t.Fatalf("same versions %v", same)
	}
	gitTestRun(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	refs, err := gitTestCall(s, "git.refs", map[string]any{"workspaceId": w.ID})
	b, _ := json.Marshal(refs)
	if err != nil || !strings.Contains(string(b), "origin/main") {
		t.Fatalf("refs %s %v", b, err)
	}
	for _, path := range []string{".git/config", "../file.txt", ":(glob)*"} {
		if _, err = gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": path, "comparison": result["comparison"]}); err == nil {
			t.Fatalf("invalid path %s", path)
		}
	}
	foreign := result["comparison"].(map[string]any)
	foreign["repository"] = "/not-this-repository"
	if _, err = gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "file.txt", "comparison": foreign}); err == nil {
		t.Fatal("foreign comparison accepted")
	}
}

func TestComparisonUnbornAndLiteralPaths(t *testing.T) {
	s, w, dir := gitTestServer(t)
	gitTestRun(t, dir, "checkout", "--orphan", "unborn")
	gitTestRun(t, dir, "rm", "--cached", "file.txt")
	result := compareTest(t, s, w.ID, map[string]string{"kind": "head"}, map[string]string{"kind": "worktree"})
	if len(result["files"].([]any)) != 1 {
		t.Fatalf("unborn %v", result)
	}
	file, err := gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "file.txt", "comparison": result["comparison"]})
	if err != nil || file["old"] != "" || file["new"] != "original\n" {
		t.Fatalf("unborn contents %v %v", file, err)
	}
	os.Remove(filepath.Join(dir, "file.txt"))
	gitTestRun(t, dir, "checkout", "main")
	os.WriteFile(filepath.Join(dir, "[glob].go"), []byte("literal\n"), 0600)
	gitTestRun(t, dir, "add", ".")
	gitTestRun(t, dir, "commit", "-m", "literal")
	result = compareTest(t, s, w.ID, map[string]string{"kind": "branch", "name": "other"}, map[string]string{"kind": "head"})
	file, err = gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "[glob].go", "comparison": result["comparison"]})
	if err != nil || file["old"] != "" || file["new"] != "literal\n" {
		t.Fatalf("literal contents %v %v", file, err)
	}
}

func TestComparisonReturnsOpenFileWithItsAcceptedPair(t *testing.T) {
	s, w, dir := gitTestServer(t)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("working\n"), 0600)
	out, err := gitTestCall(s, "git.compare", map[string]any{"workspaceId": w.ID, "reviewPath": "file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	file, ok := out["versions"].(map[string]any)
	if !ok || file["old"] != "original\n" || file["new"] != "working\n" {
		t.Fatal("comparison did not atomically return the visible file", out)
	}
}

func TestComparisonKeepsOpenFileAsExplicitNoDiff(t *testing.T) {
	s, w, _ := gitTestServer(t)
	out, err := gitTestCall(s, "git.compare", map[string]any{"workspaceId": w.ID, "reviewPath": "file.txt", "comparison": map[string]any{"base": map[string]string{"kind": "branch", "name": "main"}, "target": map[string]string{"kind": "branch", "name": "other"}}})
	if err != nil {
		t.Fatal(err)
	}
	file, ok := out["versions"].(map[string]any)
	if !ok || file["path"] != "file.txt" || file["unchanged"] != true {
		t.Fatal("open file has no explicit no-diff result", out)
	}
}

func TestWorktreeComparisonReportsActualBranchAndRejectsStaleFileQuery(t *testing.T) {
	s, w, dir := gitTestServer(t)
	first := compareTest(t, s, w.ID, map[string]string{"kind": "head"}, map[string]string{"kind": "worktree"})
	gitTestRun(t, dir, "checkout", "other")
	if _, err := gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "file.txt", "comparison": first["comparison"]}); err == nil {
		t.Fatal("file read silently used another working branch")
	}
	refreshed, err := gitTestCall(s, "git.compare", map[string]any{"workspaceId": w.ID, "comparison": first["comparison"]})
	if err != nil {
		t.Fatal(err)
	}
	target := refreshed["comparison"].(map[string]any)["target"].(map[string]any)
	if target["branch"] != "other" {
		t.Fatal("working directory labeled with previous branch", target)
	}
}
