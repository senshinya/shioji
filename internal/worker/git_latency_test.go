package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func countGitCalls(t *testing.T) func() int {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(`#!/bin/sh
printf 'call\n' >> "$TEST_GIT_CALLS_FILE"
exec "$TEST_GIT_REAL" "$@"
`), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_GIT_REAL", realGit)
	t.Setenv("TEST_GIT_CALLS_FILE", log)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return func() int {
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "\n")
	}
}

func TestGitStatusCombinesBranchHeadAndDirtyRead(t *testing.T) {
	_, _, dir := gitTestServer(t)
	head := gitTestRun(t, dir, "rev-parse", "HEAD")
	calls := countGitCalls(t)
	status, err := queryGit(context.Background(), dir)
	if err != nil || status.Branch != "main" || status.Head != head || status.Dirty {
		t.Fatalf("combined checkout status: %+v, %v", status, err)
	}
	if n := calls(); n > 5 {
		t.Fatalf("checkout status launched %d Git processes; branch, HEAD and dirty state must share one read", n)
	}
}

func TestGitStatusDoesNotRepeatNonRepositoryProbe(t *testing.T) {
	dir := t.TempDir()
	calls := countGitCalls(t)
	status, err := queryGit(context.Background(), dir)
	if err != nil || status.Applicable {
		t.Fatalf("non-repository status: %+v, %v", status, err)
	}
	if n := calls(); n != 1 {
		t.Fatalf("non-repository detection launched %d Git processes", n)
	}
}

func TestGitStatusPreservesDetachedAndHeaderLikeFileNames(t *testing.T) {
	_, _, dir := gitTestServer(t)
	head := gitTestRun(t, dir, "rev-parse", "HEAD")
	gitTestRun(t, dir, "switch", "-c", "(detached)")
	status, err := queryGit(context.Background(), dir)
	if err != nil || status.Branch != "(detached)" || status.Head != head || status.Dirty {
		t.Fatalf("literal detached branch was confused with detached HEAD: %+v, %v", status, err)
	}
	gitTestRun(t, dir, "switch", "--detach")
	status, err = queryGit(context.Background(), dir)
	if err != nil || status.Branch != "" || status.Head != head || status.Dirty {
		t.Fatalf("detached HEAD status: %+v, %v", status, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "# branch.head false\nfile"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err = queryGit(context.Background(), dir)
	if err != nil || status.Branch != "" || status.Head != head || !status.Dirty {
		t.Fatalf("untracked file affected branch headers or disappeared: %+v, %v", status, err)
	}
}
