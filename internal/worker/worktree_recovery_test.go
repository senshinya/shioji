package worker

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestWorktreeRestoreRepairsStaleRegistration(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	path := d.Session.Context.Repository
	// Simulate a crash/manual cache loss between removing the directory and
	// pruning Git's registration. All native work here is the memory harness.
	if e := os.RemoveAll(path); e != nil {
		t.Fatal(e)
	}
	s.dispatchMu.Lock()
	e := s.restoreOwnedWorktrees([]string{d.Session.Context.CWD})
	s.dispatchMu.Unlock()
	t.Logf("stale registered checkout restore: %v", e)
	if e != nil {
		t.Error("owned missing checkout cannot recover a stale Git registration")
	}
	// Positive control uses the same exact path and preserved branch, without
	// changing application metadata, just Lody's local prune-and-retry sequence.
	gitTestRun(t, w.Folders[0], "worktree", "prune")
	s.dispatchMu.Lock()
	control := s.restoreOwnedWorktrees([]string{d.Session.Context.CWD})
	s.dispatchMu.Unlock()
	t.Logf("local prune + same owning restore control: %v", control)
	if control != nil {
		t.Fatal("control failed", control)
	}
}

func TestWorktreeCreationRetriesItsOwnPartialResult(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	dir := w.Folders[0]
	gitTestRun(t, dir, "init", "-b", "main")
	gitTestRun(t, dir, "config", "user.name", "Test")
	gitTestRun(t, dir, "config", "user.email", "test@example.invalid")
	if e := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	gitTestRun(t, dir, "add", ".")
	gitTestRun(t, dir, "commit", "-m", "initial")
	bin := t.TempDir()
	gate := filepath.Join(t.TempDir(), "once")
	script := `#!/bin/sh
case " $* " in
 *" worktree add "*)
  if [ ! -f "$SHIOJI_COMPLETE_PARTIAL" ]; then
   touch "$SHIOJI_COMPLETE_PARTIAL"
   /usr/bin/git "$@" || exit $?
   echo 'injected lost completion after Git created the worktree' >&2
   exit 1
  fi;;
esac
exec /usr/bin/git "$@"
`
	if e := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("SHIOJI_COMPLETE_PARTIAL", gate)
	in := domain.Submission{OperationID: "partial-create", WorkspaceID: w.ID, Text: "retry once", Git: domain.GitChoice{NewWorktree: true}, Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	if _, e := s.submit(in); e == nil {
		t.Fatal("fixture did not inject failure")
	}
	hash := sha256.Sum256([]byte(in.OperationID))
	dest := filepath.Join(s.config.Directory, "worktrees", fmt.Sprintf("%x", hash[:12]))
	_, exists := os.Stat(dest)
	records, _ := s.store.OwnedWorktrees()
	t.Logf("partial create: directoryExists=%v owningRecords=%d", exists == nil, len(records))
	if exists != nil {
		t.Fatal("real Git did not create checkout", exists)
	}
	// A failed creation has no archived/deleted owner. An independent sweep
	// must not discard its only retryable checkout before acceptance.
	if e := s.collectOwnedWorktrees(time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(dest); e != nil {
		t.Fatal("collector removed unaccepted preparation", e)
	}
	op, e := s.submit(in)
	t.Logf("same-operation retry: error=%v session=%s", e, op.SessionID)
	if e != nil {
		t.Fatal("partial successful native mutation has no retry/adoption path", e)
	}
}

func TestWorktreeCreationCanOutlastGitQueryDeadline(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	dir := w.Folders[0]
	gitTestRun(t, dir, "init", "-b", "main")
	gitTestRun(t, dir, "config", "user.name", "Test")
	gitTestRun(t, dir, "config", "user.email", "test@example.invalid")
	for name, content := range map[string]string{".gitattributes": "file.txt filter=slow\n", "file.txt": "original\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gitTestRun(t, dir, "add", ".")
	gitTestRun(t, dir, "commit", "-m", "initial")
	gitTestRun(t, dir, "config", "filter.slow.clean", "cat")
	gitTestRun(t, dir, "config", "filter.slow.smudge", "/bin/sleep 11 && cat")
	gitTestRun(t, dir, "config", "filter.slow.required", "true")

	in := domain.Submission{OperationID: "slow-worktree", WorkspaceID: w.ID, Text: "new worktree",
		Git: domain.GitChoice{NewWorktree: true}, Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	op, err := s.submit(in)
	if err != nil {
		t.Fatalf("a slow Git checkout should finish before the input is accepted: %v", err)
	}
	session, err := s.store.SessionRecord(op.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(session.Context.CWD, "file.txt"))
	if session.Context.CWD == dir || err != nil || string(data) != "original\n" {
		t.Fatalf("expected a complete independent checkout at %q, contents=%q, err=%v", session.Context.CWD, data, err)
	}
}

func TestWorktreeCreationPreservesForeignDirectory(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "registered"} {
		t.Run(kind, func(t *testing.T) {
			s, w, dir := gitTestServer(t)
			in := domain.Submission{OperationID: "foreign-path", WorkspaceID: w.ID, Text: "keep", Git: domain.GitChoice{NewWorktree: true}}
			sum := sha256.Sum256([]byte(in.OperationID))
			dest := filepath.Join(s.config.Directory, "worktrees", fmt.Sprintf("%x", sum[:12]))
			if e := os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
				t.Fatal(e)
			}
			target := dest
			if kind == "symlink" {
				target = t.TempDir()
				if e := os.Symlink(target, dest); e != nil {
					t.Fatal(e)
				}
			} else if kind == "registered" {
				gitTestRun(t, dir, "worktree", "add", "-b", "foreign-owned-elsewhere", dest, "main")
			} else if e := os.MkdirAll(dest, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("foreign data"), 0600); e != nil {
				t.Fatal(e)
			}
			s.dispatchMu.Lock()
			_, e := s.prepareGitLocked(in, domain.LaunchContext{CWD: dir})
			s.dispatchMu.Unlock()
			if e == nil {
				t.Fatal("adopted unrelated existing path")
			}
			b, e := os.ReadFile(filepath.Join(target, "keep.txt"))
			if e != nil || string(b) != "foreign data" {
				t.Fatal("foreign data changed", e, string(b))
			}
			records, e := s.store.OwnedWorktrees()
			if e != nil || len(records) != 0 {
				t.Fatal("foreign path acquired owning record", records, e)
			}
		})
	}
}
func TestWorktreeCreationPreservesConflictingBranch(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	// This operation's reserved name already belongs to an independent Git user.
	in := domain.Submission{OperationID: "branch-collision", WorkspaceID: w.ID, Text: "new", Git: domain.GitChoice{NewWorktree: true}, Config: d.Session.Config}
	sum := sha256.Sum256([]byte(in.OperationID))
	foreign := "shioji/" + fmt.Sprintf("%x", sum[:12])
	gitTestRun(t, w.Folders[0], "branch", foreign, "main")
	before := gitTestRun(t, w.Folders[0], "rev-parse", foreign)
	op, e := s.submit(in)
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, op.ID)
	after, e := s.store.Session(op.SessionID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if after.Session.GitBranch == foreign || gitTestRun(t, w.Folders[0], "rev-parse", foreign) != before {
		t.Fatal("existing branch adopted or rewritten")
	}
}

func TestWorktreeCreationPreservesAmbiguousBranchRef(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	in := domain.Submission{OperationID: "ambiguous-owned-ref", WorkspaceID: w.ID, Text: "new", Git: domain.GitChoice{NewWorktree: true}, Config: d.Session.Config}
	sum := sha256.Sum256([]byte(in.OperationID))
	foreign := "shioji/" + fmt.Sprintf("%x", sum[:12])
	gitTestRun(t, w.Folders[0], "branch", foreign, "main")
	gitTestRun(t, w.Folders[0], "-c", "tag.gpgsign=false", "tag", foreign, "main")
	head := gitTestRun(t, w.Folders[0], "rev-parse", "refs/heads/"+foreign)
	op, err := s.submit(in)
	if err != nil {
		t.Fatal("ambiguous existing ref prevented fresh branch allocation", err)
	}
	awaitReceipt(t, s, op.ID)
	created, err := s.store.Session(op.SessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if created.Session.GitBranch != foreign+"-1" {
		t.Fatal("wrong new branch", created.Session.GitBranch)
	}
	for _, ref := range []string{"refs/heads/" + foreign, "refs/tags/" + foreign} {
		if got := gitTestRun(t, w.Folders[0], "rev-parse", ref); got != head {
			t.Fatal("foreign ref changed", ref, got)
		}
	}
}

func TestGitBranchNamesRemainExactWhenTagsShareNames(t *testing.T) {
	s, _, dir := gitTestServer(t)
	gitTestRun(t, dir, "-c", "tag.gpgsign=false", "tag", "main", "HEAD")
	status, err := queryGit(s.ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "main" || status.DefaultBranch != "main" {
		t.Fatalf("branch/tag ambiguity changed checkout identity: %+v", status)
	}
	if len(status.Branches) != 2 || status.Branches[0] != "main" || status.Branches[1] != "other" {
		t.Fatal("heads namespace was not stripped exactly", status.Branches)
	}
}

func TestWorktreeRejectedCreationCleanupPreservesForeignBranch(t *testing.T) {
	s, w, dir := gitTestServer(t)
	in := domain.Submission{OperationID: "cleanup-collision", WorkspaceID: w.ID, Git: domain.GitChoice{NewWorktree: true}}
	sum := sha256.Sum256([]byte(in.OperationID))
	foreign := "shioji/" + fmt.Sprintf("%x", sum[:12])
	gitTestRun(t, dir, "branch", foreign, "main")
	s.dispatchMu.Lock()
	launch, e := s.prepareGitLocked(in, domain.LaunchContext{CWD: dir})
	if e == nil {
		s.removeUnacceptedWorktree(in)
	}
	s.dispatchMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if got := gitTestRun(t, dir, "branch", "--list", foreign); got == "" {
		t.Fatal("cleanup deleted pre-existing independent branch")
	}
	if _, e := os.Stat(launch.CWD); !os.IsNotExist(e) {
		t.Fatal("known rejected checkout was not cleaned", e)
	}
}
