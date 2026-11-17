package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/store"
	"strings"
	"syscall"
	"testing"
	"time"
)

func gitTestRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}
func gitTestServer(t *testing.T) (*Server, domain.Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	gitTestRun(t, dir, "init", "-b", "main")
	gitTestRun(t, dir, "config", "user.name", "Test")
	gitTestRun(t, dir, "config", "user.email", "test@example.invalid")
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original\n"), 0600)
	gitTestRun(t, dir, "add", ".")
	gitTestRun(t, dir, "commit", "-m", "initial")
	gitTestRun(t, dir, "branch", "other")
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w, _ := db.CreateWorkspace("Git", []string{dir})
	stateDir := t.TempDir()
	manager, err := managedexec.New(filepath.Join(stateDir, "executions"), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return &Server{store: db, ctx: context.Background(), executions: manager, config: Config{Directory: stateDir}}, w, dir
}
func gitTestCall(s *Server, method string, in any) (map[string]any, error) {
	body, _ := json.Marshal(in)
	out, err := s.handle(method, body)
	b, _ := json.Marshal(out)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m, err
}
func gitTestSession(t *testing.T, s *Server, w domain.Workspace, branch, status string) string {
	t.Helper()
	op, err := s.store.Accept(domain.Submission{HarnessID: "codex", OperationID: domain.NewID(), WorkspaceID: w.ID, Text: "test"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.store.Session(op.SessionID, 0, 1)
	b, _ := json.Marshal(d.Session)
	var m map[string]any
	json.Unmarshal(b, &m)
	m["status"] = status
	m["gitBranch"] = branch
	m["context"].(map[string]any)["branch"] = branch
	m["context"].(map[string]any)["repository"] = w.Folders[0]
	b, _ = json.Marshal(m)
	var sess domain.Session
	json.Unmarshal(b, &sess)
	if _, err = s.store.Apply(sess.ID, domain.Event{Type: "session", Session: &sess}); err != nil {
		t.Fatal(err)
	}
	s.store.Apply(sess.ID, domain.Event{Type: "session.git", Session: &sess})
	s.store.SetOperation(op.ID, "confirmed", "")
	return sess.ID
}
func TestGitStatusAndExplicitCheckoutProtectsSharedCheckout(t *testing.T) {
	s, w, dir := gitTestServer(t)
	out, err := gitTestCall(s, "git.status", map[string]any{"workspaceId": w.ID})
	if err != nil || out["branch"] != "main" {
		t.Fatalf("real Git status: %v %v", out, err)
	}
	a := gitTestSession(t, s, w, "main", "completed")
	b := gitTestSession(t, s, w, "main", "running")
	in := map[string]any{"workspaceId": w.ID, "sessionId": a, "branch": "other"}
	if _, err = gitTestCall(s, "git.checkout", in); err == nil {
		t.Fatal("checkout while another agent runs")
	}
	if gitTestRun(t, dir, "branch", "--show-current") != "main" {
		t.Fatal("busy checkout mutated")
	}
	d, _ := s.store.Session(b, 0, 1)
	d.Session.Status = "completed"
	s.store.Apply(b, domain.Event{Type: "session", Session: &d.Session})
	os.WriteFile(filepath.Join(dir, "untracked"), []byte("keep"), 0600)
	if _, err = gitTestCall(s, "git.checkout", in); err == nil {
		t.Fatal("dirty checkout allowed")
	}
	os.Remove(filepath.Join(dir, "untracked"))
	if _, err = gitTestCall(s, "git.checkout", in); err != nil {
		t.Fatal(err)
	}
	d, _ = s.store.Session(b, 0, 1)
	data, _ := json.Marshal(d.Session)
	if !strings.Contains(string(data), `"contextChanged":true`) {
		t.Fatalf("other idle session did not require confirmation: %s", data)
	}
	d, _ = s.store.Session(a, 0, 1)
	if d.Session.Context.CWD != dir {
		t.Fatal("existing session relocated")
	}
	data, _ = json.Marshal(d.Session.Context)
	if !strings.Contains(string(data), `"branch":"main"`) {
		t.Fatal("original launch context was rewritten")
	}
}
func TestGitFileRejectsEscapingSymlinkAndReturnsRealChanges(t *testing.T) {
	s, w, dir := gitTestServer(t)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("changed\n"), 0600)
	out, err := gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "file.txt"})
	if err != nil || out["old"] != "original\n" || out["new"] != "changed\n" {
		t.Fatalf("diff contents %v %v", out, err)
	}
	outside := filepath.Join(t.TempDir(), "private")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(dir, "escape"))
	for _, p := range []string{"escape", "../private", outside, ".git/config"} {
		if _, err = gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": p}); err == nil {
			t.Fatalf("read escaped scope: %s", p)
		}
	}
}

func TestCheckoutRejectsPendingAndUnsettledOperationsEvenWhenIdle(t *testing.T) {
	s, w, _ := gitTestServer(t)
	id := gitTestSession(t, s, w, "main", "completed")
	query := map[string]any{"workspaceId": w.ID, "sessionId": id, "branch": "other"}
	req := domain.PendingRequest{ID: "pending", State: "pending", Kind: "questions"}
	s.store.Apply(id, domain.Event{Type: "request.open", Request: &req})
	if _, err := gitTestCall(s, "git.checkout", query); err == nil {
		t.Fatal("pending question allowed checkout")
	}
	req.State = "resolved"
	s.store.Apply(id, domain.Event{Type: "request.resolve", Request: &req})
	snap, _ := s.store.Snapshot()
	s.store.SetOperation(snap.Operations[0].ID, "indeterminate", "")
	query["branch"] = "main"
	if _, err := gitTestCall(s, "git.checkout", query); err == nil {
		t.Fatal("indeterminate operation allowed context confirmation")
	}
}

func TestGitMonitorMarksExternalChangesWithoutSubmitting(t *testing.T) {
	s, w, dir := gitTestServer(t)
	id := gitTestSession(t, s, w, "main", "completed")
	gitTestRun(t, dir, "switch", "other")
	if err := s.checkGitContexts(); err != nil {
		t.Fatal(err)
	}
	d, _ := s.store.Session(id, 0, 1)
	if !d.Session.ContextChanged {
		t.Fatal("external branch change was invisible before next submission")
	}
}

func TestFileReviewRejectsFIFOBeforeItCanBlockDispatch(t *testing.T) {
	s, w, dir := gitTestServer(t)
	path := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := gitTestCall(s, "git.file", map[string]any{"workspaceId": w.ID, "path": "pipe"})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as a regular file")
		}
	case <-time.After(2 * time.Second):
		f, _ := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
		if f != nil {
			f.Close()
		}
		<-done
		t.Fatal("FIFO review blocked waiting for a writer")
	}
	if _, err := gitTestCall(s, "git.status", map[string]any{"workspaceId": w.ID}); err != nil {
		t.Fatalf("dispatch stayed blocked: %v", err)
	}
}

func TestCheckoutWaitsForActiveChildOfCompletedParent(t *testing.T) {
	s, w, dir := gitTestServer(t)
	id := gitTestSession(t, s, w, "main", "completed")
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "t", Title: "child", State: "running"}
	if _, err := s.store.Apply(id, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"workspaceId": w.ID, "sessionId": id, "branch": "other"}
	if _, err := gitTestCall(s, "git.checkout", in); err == nil {
		t.Fatal("active child lost checkout ownership")
	}
	if gitTestRun(t, dir, "branch", "--show-current") != "main" {
		t.Fatal("busy child checkout mutated")
	}
	work.State = "completed"
	s.store.Apply(id, domain.Event{Type: "work.upsert", Work: &work})
	if _, err := gitTestCall(s, "git.checkout", in); err != nil {
		t.Fatal("completed work still blocks checkout", err)
	}
}

func TestDirtyBranchSwitchExplainsCheckoutAndPreservesFiles(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint("existing=", existing), func(t *testing.T) {
			s, w, dir := gitTestServer(t)
			if err := os.WriteFile(filepath.Join(dir, "untracked"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			if existing {
				id := gitTestSession(t, s, w, "main", "completed")
				_, err = gitTestCall(s, "git.checkout", map[string]any{"workspaceId": w.ID, "sessionId": id, "branch": "other"})
			} else {
				_, err = prepareGitForTest(s, domain.Submission{Git: domain.GitChoice{Branch: "other"}}, domain.LaunchContext{CWD: dir})
			}
			if err == nil {
				t.Fatal("dirty branch switch was allowed")
			}
			for _, detail := range []string{"main", "other", "未提交", "当前分支", "worktree"} {
				if !strings.Contains(err.Error(), detail) {
					t.Fatalf("checkout error omitted %q: %v", detail, err)
				}
			}
			if gitTestRun(t, dir, "branch", "--show-current") != "main" {
				t.Fatal("rejection switched the checkout")
			}
			if b, e := os.ReadFile(filepath.Join(dir, "untracked")); e != nil || string(b) != "keep" {
				t.Fatal("rejection changed untracked files", e)
			}
			if !existing {
				if _, err = prepareGitForTest(s, domain.Submission{Git: domain.GitChoice{Branch: "main"}}, domain.LaunchContext{CWD: dir}); err != nil {
					t.Fatal("same-branch session requires a clean checkout", err)
				}
			}
		})
	}
}
