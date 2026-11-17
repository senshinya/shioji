package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func ownedWorktreeFixture(t *testing.T) (*Server, domain.Workspace, domain.SessionDetail) {
	t.Helper()
	s, w := memoryWorker(t, idleHarness{})
	dir := w.Folders[0]
	gitTestRun(t, dir, "init", "-b", "main")
	gitTestRun(t, dir, "config", "user.name", "Test")
	gitTestRun(t, dir, "config", "user.email", "test@example.invalid")
	if e := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original\n"), 0600); e != nil {
		t.Fatal(e)
	}
	gitTestRun(t, dir, "add", ".")
	gitTestRun(t, dir, "commit", "-m", "initial")
	op, e := s.submit(domain.Submission{OperationID: "owned-worktree", WorkspaceID: w.ID, Text: "work", Git: domain.GitChoice{NewWorktree: true}, Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, op.ID)
	d, e := s.store.Session(op.SessionID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	return s, w, d
}
func archiveOwnedFixture(t *testing.T, s *Server, id string) {
	t.Helper()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	d, e := s.store.Session(id, 0, 1)
	if e != nil {
		t.Fatal(e)
	}
	gen, e := s.archiveGeneration(id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.SetSessionArchived(id, d.Session.Revision, true, gen); e != nil {
		t.Fatal(e)
	}
}
func TestOwnedWorktreeArchiveBacksUpAndLazilyRestoresActualBranch(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	if e := os.WriteFile(filepath.Join(path, "file.txt"), []byte("edited\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(path, "new.txt"), []byte("new\n"), 0600); e != nil {
		t.Fatal(e)
	}
	gitTestRun(t, path, "branch", "-m", "renamed-session")
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("archived owned worktree directory retained", e)
	}
	if got := gitTestRun(t, w.Folders[0], "show", "renamed-session:file.txt"); got != "edited" {
		t.Fatal("dirty content not backed up", got)
	}
	if got := gitTestRun(t, w.Folders[0], "show", "renamed-session:new.txt"); got != "new" {
		t.Fatal(got)
	}
	if got := gitTestRun(t, w.Folders[0], "show", "HEAD:file.txt"); got != "original" {
		t.Fatal("original checkout changed", got)
	}
	after, _ := s.store.Session(d.Session.ID, 0, 100)
	if _, e := s.store.SetSessionArchived(d.Session.ID, after.Session.Revision, false); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("restore metadata eagerly recreated worktree")
	}
	resumed := submitIdleFixture(t, s, w, "resume-owned", d.Session.ID)
	if resumed.Session.ThreadID != d.Session.ThreadID || resumed.Session.Context.CWD != d.Session.Context.CWD {
		t.Fatal("restore changed conversation or execution path")
	}
	if got := gitTestRun(t, path, "branch", "--show-current"); got != "renamed-session" {
		t.Fatal("restored wrong branch", got)
	}
	if b, e := os.ReadFile(filepath.Join(path, "new.txt")); e != nil || string(b) != "new\n" {
		t.Fatal("restore lost new file", string(b), e)
	}
}
func TestOwnedWorktreeSharedBySessionsWaitsForLastArchive(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	op, e := s.store.AcceptForHarness(domain.Submission{OperationID: "independent-fork", WorkspaceID: w.ID, Text: "fork", Config: d.Session.Config}, "idle", d.Session.Context)
	if e != nil {
		t.Fatal(e)
	}
	s.store.SetOperation(op.ID, "confirmed", "")
	fork, _ := s.store.Session(op.SessionID, 0, 1)
	fork.Session.Status = "completed"
	if _, e = s.store.Apply(op.SessionID, domain.Event{Type: "session", Session: &fork.Session}); e != nil {
		t.Fatal(e)
	}
	archiveOwnedFixture(t, s, d.Session.ID)
	if e = s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("archive removed another live session's checkout", e)
	}
	archiveOwnedFixture(t, s, op.SessionID)
	if e = s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("last archive did not reclaim shared worktree", e)
	}
}
func TestOwnedWorktreeDeletionKeepsBackupBranchAndOriginalProject(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	os.WriteFile(filepath.Join(path, "new.txt"), []byte("keep"), 0600)
	archiveOwnedFixture(t, s, d.Session.ID)
	latest, _ := s.store.Session(d.Session.ID, 0, 1)
	s.dispatchMu.Lock()
	_, e := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: d.Session.ID, Revision: latest.Session.Revision})
	s.dispatchMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("deleted owned worktree retained", e)
	}
	if got := gitTestRun(t, w.Folders[0], "show", d.Session.GitBranch+":new.txt"); got != "keep" {
		t.Fatal("deletion lost backup", got)
	}
	if _, e = os.Stat(w.Folders[0]); e != nil {
		t.Fatal("original project removed", e)
	}
}
func TestOwnedWorktreeBackupFailureRetainsFilesAndRetries(t *testing.T) {
	s, _, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	os.WriteFile(filepath.Join(path, "new.txt"), []byte("keep"), 0600)
	index := gitTestRun(t, path, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if e := os.WriteFile(index+".lock", nil, 0600); e != nil {
		t.Fatal(e)
	}
	archiveOwnedFixture(t, s, d.Session.ID)
	now := time.Now()
	if e := s.cleanupArchives(now); e == nil {
		t.Fatal("backup lock failure was hidden")
	}
	if b, e := os.ReadFile(filepath.Join(path, "new.txt")); e != nil || string(b) != "keep" {
		t.Fatal("failed backup removed files", e)
	}
	// First retry after 30 seconds, then double the per-checkout delay.
	if e := s.cleanupArchives(now.Add(30 * time.Second)); e == nil {
		t.Fatal("second backup failure was hidden")
	}
	os.Remove(index + ".lock")
	if e := s.cleanupArchives(now.Add(89 * time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("worktree retry bypassed exponential backoff", e)
	}
	if e := s.cleanupArchives(now.Add(90 * time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("retry did not remove directory", e)
	}
}
func TestOwnedWorktreeDetachedHeadBackupRemainsReachable(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	gitTestRun(t, path, "checkout", "--detach")
	os.WriteFile(filepath.Join(path, "new.txt"), []byte("detached"), 0600)
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	refs := gitTestRun(t, w.Folders[0], "for-each-ref", "--format=%(refname)", "refs/heads/shioji/")
	found := false
	for _, ref := range strings.Fields(refs) {
		if out, e := gitRun(s.ctx, w.Folders[0], "show", ref+":new.txt"); e == nil && out == "detached" {
			found = true
		}
	}
	if !found {
		t.Fatal("detached backup is unreachable after removal")
	}
}

func TestOwnedWorktreeFailedRetirementDoesNotBlockOtherCheckout(t *testing.T) {
	s, w, first := ownedWorktreeFixture(t)
	op, e := s.submit(domain.Submission{OperationID: "second-owned", WorkspaceID: w.ID, Text: "second", Git: domain.GitChoice{NewWorktree: true}, Config: first.Session.Config})
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, op.ID)
	second, _ := s.store.Session(op.SessionID, 0, 1)
	bad := &archiveFailClose{idleTestRuntime: s.currentRuntime(first.Session.ID).(*idleTestRuntime)}
	bad.fail.Store(true)
	t.Cleanup(func() { bad.fail.Store(false) })
	s.runtimeMu.Lock()
	s.runtimes[first.Session.ID] = bad
	s.runtimeMu.Unlock()
	archiveOwnedFixture(t, s, first.Session.ID)
	archiveOwnedFixture(t, s, second.Session.ID)
	_ = s.cleanupArchives(time.Now())
	if _, e = os.Stat(first.Session.Context.Repository); e != nil {
		t.Fatal("unsettled producer lost checkout", e)
	}
	if _, e = os.Stat(second.Session.Context.Repository); !os.IsNotExist(e) {
		t.Fatal("unrelated detached producer blocked healthy checkout", e)
	}
}

func TestOwnedWorktreeEarlierCreationRecoveredFromAcceptedReceipt(t *testing.T) {
	s, _, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	if e := s.store.ForgetOwnedWorktree(path); e != nil {
		t.Fatal(e)
	}
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("earlier worker-created worktree was not discovered", e)
	}
}
func TestOwnedWorktreeRemovedStatusCanBeReadWithoutRecreation(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	status, e := s.gitStatus(protocol.GitQuery{SessionID: d.Session.ID, WorkspaceID: w.ID})
	if e != nil || !status.Applicable || status.Branch != d.Session.GitBranch {
		t.Fatal("removed checkout prevents opening history/composer", status, e)
	}
	if _, e = os.Stat(d.Session.Context.Repository); !os.IsNotExist(e) {
		t.Fatal("status lookup recreated checkout")
	}
}

func TestOwnedWorktreeLifecycleSurvivesWorkerRestart(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	os.WriteFile(filepath.Join(path, "new.txt"), []byte("restart"), 0600)
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	config := s.config
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	restarted, e := New(context.Background(), config)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { restarted.Close() })
	restarted.dispatchMu.Lock()
	restarted.pressureSample = func() uint32 { return 1 }
	restarted.dispatchMu.Unlock()
	archived, e := restarted.store.Session(d.Session.ID, 0, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.store.SetSessionArchived(d.Session.ID, archived.Session.Revision, false); e != nil {
		t.Fatal(e)
	}
	submitIdleFixture(t, restarted, w, "after-restart", d.Session.ID)
	if b, e := os.ReadFile(filepath.Join(path, "new.txt")); e != nil || string(b) != "restart" {
		t.Fatal("restart lost backup/ownership", string(b), e)
	}
}
func TestOwnedWorktreeUnregisteredDirectoryIsNeverAdopted(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	foreign := filepath.Join(filepath.Dir(d.Session.Context.Repository), "000000000000000000000000")
	gitTestRun(t, w.Folders[0], "worktree", "add", "-b", "manual-checkout", foreign, "main")
	op, e := s.store.AcceptForHarness(domain.Submission{OperationID: "manual-record", WorkspaceID: w.ID, Text: "manual", Config: d.Session.Config}, "idle", domain.LaunchContext{DeviceID: s.store.DeviceID(), CWD: foreign, Repository: foreign, Branch: "manual-checkout"})
	if e != nil {
		t.Fatal(e)
	}
	s.store.SetOperation(op.ID, "confirmed", "")
	manual, _ := s.store.Session(op.SessionID, 0, 1)
	manual.Session.Status = "completed"
	s.store.Apply(op.SessionID, domain.Event{Type: "session", Session: &manual.Session})
	archiveOwnedFixture(t, s, op.SessionID)
	if e = s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(foreign); e != nil {
		t.Fatal("adopted directory without worker creation receipt", e)
	}
}

func TestOwnedWorktreeBackupAllowsOtherSessionsAndSerializesRestoredInput(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	os.WriteFile(filepath.Join(path, "new.txt"), []byte("race-safe"), 0600)
	gate := t.TempDir()
	bin := t.TempDir()
	script := `#!/bin/sh
backup=false
for arg in "$@"; do
 if [ "$arg" = "add" ]; then backup=true; fi
done
if [ "$backup" = true ]; then
 touch "$SHIOJI_TEST_GIT_GATE/entered"
 while [ ! -f "$SHIOJI_TEST_GIT_GATE/release" ]; do /bin/sleep 0.01; done
fi
exec /usr/bin/git "$@"
`
	if e := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("SHIOJI_TEST_GIT_GATE", gate)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	archiveOwnedFixture(t, s, d.Session.ID)
	cleaned := make(chan error, 1)
	go func() { cleaned <- s.cleanupArchives(time.Now()) }()
	defer os.WriteFile(filepath.Join(gate, "release"), nil, 0600)
	for until := time.Now().Add(3 * time.Second); ; {
		if _, e := os.Stat(filepath.Join(gate, "entered")); e == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("backup never entered Git")
		}
		time.Sleep(time.Millisecond)
	}
	independent := make(chan error, 1)
	go func() {
		_, e := s.submit(domain.Submission{OperationID: "unrelated-backup", WorkspaceID: w.ID, Text: "other", Config: d.Session.Config})
		independent <- e
	}()
	select {
	case e := <-independent:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("Git backup held device dispatch")
	}
	latest, _ := s.store.Session(d.Session.ID, 0, 1)
	s.dispatchMu.Lock()
	_, e := s.store.SetSessionArchived(d.Session.ID, latest.Session.Revision, false)
	s.dispatchMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	resumed := make(chan error, 1)
	go func() {
		_, e := s.submit(domain.Submission{OperationID: "restored-during-backup", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "resume", Config: d.Session.Config})
		resumed <- e
	}()
	select {
	case e := <-resumed:
		t.Fatal("restored input bypassed checkout transition", e)
	case <-time.After(80 * time.Millisecond):
	}
	os.WriteFile(filepath.Join(gate, "release"), nil, 0600)
	if e := <-cleaned; e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-resumed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restored input deadlocked on backup transition")
	}
	if receipt := awaitReceipt(t, s, "restored-during-backup"); receipt.State != "confirmed" {
		t.Fatal(receipt)
	}
	if b, e := os.ReadFile(filepath.Join(path, "new.txt")); e != nil || string(b) != "race-safe" {
		t.Fatal("concurrent restore lost files", e)
	}
}

func TestOwnedWorktreeBrokenLegacyDiscoveryCannotBlockOtherSubmission(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	if e := s.store.ForgetOwnedWorktree(path); e != nil {
		t.Fatal(e)
	}
	gitfile := filepath.Join(path, ".git")
	original, e := os.ReadFile(gitfile)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(gitfile, []byte("invalid git metadata"), 0600); e != nil {
		t.Fatal(e)
	}
	defer os.WriteFile(gitfile, original, 0600)
	if _, e = s.submit(domain.Submission{OperationID: "independent-from-broken-discovery", WorkspaceID: w.ID, Text: "other", Config: d.Session.Config}); e != nil {
		t.Fatal("unrelated work depends on broken legacy checkout discovery", e)
	}
}
