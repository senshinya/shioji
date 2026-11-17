package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"testing"
	"time"
)

func holdLifecycleGit(t *testing.T, needle string) (string, func()) {
	t.Helper()
	gate, bin := t.TempDir(), t.TempDir()
	script := `#!/bin/sh
for arg in "$@"; do
 if [ "$arg" = "$SHIOJI_AUDIT_GIT_NEEDLE" ]; then
  touch "$SHIOJI_AUDIT_GIT_GATE/entered"
  while [ ! -f "$SHIOJI_AUDIT_GIT_GATE/release" ]; do /bin/sleep 0.01; done
  break
 fi
done
exec /usr/bin/git "$@"
`
	if e := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("SHIOJI_AUDIT_GIT_GATE", gate)
	t.Setenv("SHIOJI_AUDIT_GIT_NEEDLE", needle)
	release := func() { _ = os.WriteFile(filepath.Join(gate, "release"), nil, 0600) }
	t.Cleanup(release)
	return gate, release
}
func awaitLifecycleGit(t *testing.T, gate string) {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		if _, e := os.Stat(filepath.Join(gate, "entered")); e == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("native Git probe did not enter")
}
func TestGitWaitDoesNotBlockPrivateDrain(t *testing.T) {
	for _, mode := range []string{"git.status", "git.changes", "git.file", "monitor", "existing-owned-restore"} {
		t.Run(mode, func(t *testing.T) {
			s, w, d := ownedWorktreeFixture(t)
			// Wait for acceptance bookkeeping before installing the native delay.
			s.dispatchMu.Lock()
			s.config.ServiceInstance = "audit-instance"
			s.dispatchMu.Unlock()
			gate, release := holdLifecycleGit(t, "status")
			finished := make(chan error, 1)
			go func() {
				if mode == "monitor" {
					finished <- s.checkGitContexts()
					return
				}
				if mode != "existing-owned-restore" {
					var in any = protocol.GitQuery{WorkspaceID: w.ID, SessionID: d.Session.ID}
					if mode == "git.file" {
						in = protocol.FileQuery{GitQuery: protocol.GitQuery{WorkspaceID: w.ID, SessionID: d.Session.ID}, Path: "file.txt"}
					}
					b, _ := json.Marshal(in)
					_, e := s.handle(mode, b)
					finished <- e
					return
				}
				s.dispatchMu.Lock()
				e := s.restoreOwnedWorktrees([]string{d.Session.Context.CWD})
				s.dispatchMu.Unlock()
				finished <- e
			}()
			awaitLifecycleGit(t, gate)
			admitted := make(chan struct{})
			go func() {
				b, _ := json.Marshal(supervisor.Control{Instance: "audit-instance", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
				_, _ = s.serviceControl("worker.prepare-drain", b)
				close(admitted)
			}()
			blocked := false
			select {
			case <-admitted:
			case <-time.After(200 * time.Millisecond):
				blocked = true
			}
			release()
			if e := <-finished; e != nil {
				t.Fatal(e)
			}
			select {
			case <-admitted:
			case <-time.After(time.Second):
				t.Fatal("dispatch did not recover")
			}
			t.Logf("OBSERVATION mode=%s actualPrepareDrainHandlerBlocked=%v recoveredAfterGitReply=true", mode, blocked)
			if blocked {
				t.Fatal("Git native wait held the device dispatch lock")
			}
		})
	}
}
func TestPreparingOwnedRestoreReservesBeforeAwait(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := cleanupWorktreesForTest(s, time.Now()); e != nil {
		t.Fatal(e)
	}
	latest, _ := s.store.Session(d.Session.ID, 0, 1)
	if _, e := s.store.SetSessionArchived(d.Session.ID, latest.Session.Revision, false); e != nil {
		t.Fatal(e)
	}
	s.dispatchMu.Lock()
	s.config.ServiceInstance = "audit-instance"
	s.dispatchMu.Unlock()
	gate, release := holdLifecycleGit(t, "add")
	type answer struct {
		op  domain.Operation
		err error
	}
	submitted := make(chan answer, 1)
	go func() {
		op, e := s.submit(domain.Submission{OperationID: "audit-post-drain", WorkspaceID: w.ID, SessionID: d.Session.ID, Text: "after restore", Config: d.Session.Config})
		submitted <- answer{op, e}
	}()
	awaitLifecycleGit(t, gate)
	drained, e := s.DrainService(supervisor.Control{Instance: "audit-instance", OperationID: "audit-drain"})
	if e != nil {
		release()
		t.Fatal(e)
	}
	if drained.Ready {
		release()
		<-submitted
		t.Fatal("idle drain bypassed an execution preparing its worktree")
	}
	release()
	got := <-submitted
	saved, se := s.store.Operation("audit-post-drain")
	t.Logf("OBSERVATION drainAccepted=%v updating=%v inputAcceptedAfterDrain=%v savedState=%s submitError=%v", drained.Ready, s.updating.Load(), se == nil, saved.State, got.err)
	if got.err != nil || se != nil || got.op.ID == "" {
		t.Fatal("restoring input did not continue after refused idle drain", got.err, se)
	}
}

func TestImmediateDrainRejectsLateRestoredSubmission(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := cleanupWorktreesForTest(s, time.Now()); e != nil {
		t.Fatal(e)
	}
	latest, _ := s.store.Session(d.Session.ID, 0, 1)
	if _, e := s.store.SetSessionArchived(d.Session.ID, latest.Session.Revision, false); e != nil {
		t.Fatal(e)
	}
	s.dispatchMu.Lock()
	s.config.ServiceInstance = "audit-instance"
	s.dispatchMu.Unlock()
	gate, release := holdLifecycleGit(t, "add")
	type answer struct {
		op  domain.Operation
		err error
	}
	submitted := make(chan answer, 1)
	go func() {
		op, e := s.submit(domain.Submission{OperationID: "audit-post-drain", WorkspaceID: w.ID, SessionID: d.Session.ID, Text: "after restore", Config: d.Session.Config})
		submitted <- answer{op, e}
	}()
	awaitLifecycleGit(t, gate)
	drained, e := s.DrainService(supervisor.Control{Instance: "audit-instance", OperationID: "audit-drain", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
	if e != nil || !drained.Ready {
		release()
		t.Fatalf("drain not accepted: %+v %v", drained, e)
	}
	release()
	got := <-submitted
	saved, se := s.store.Operation("audit-post-drain")
	t.Logf("OBSERVATION drainAccepted=%v updating=%v inputAcceptedAfterDrain=%v savedState=%s submitError=%v", drained.Ready, s.updating.Load(), se == nil, saved.State, got.err)
	if got.err == nil || !errors.Is(se, sql.ErrNoRows) || got.op.ID != "" {
		t.Fatal("restoring input was accepted after immediate drain", got.err, se)
	}
}

func TestGitSwitchSerializesPreparingInput(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	primary := submitIdleFixture(t, s, w, "primary-before-switch", "")
	gitTestRun(t, w.Folders[0], "branch", "other")
	gate, release := holdLifecycleGit(t, "switch")
	switching := make(chan error, 1)
	go func() {
		_, e := s.checkout(protocol.CheckoutInput{GitQuery: protocol.GitQuery{WorkspaceID: w.ID, SessionID: primary.Session.ID}, Branch: "other"})
		switching <- e
	}()
	awaitLifecycleGit(t, gate)
	submitted := make(chan error, 1)
	go func() {
		_, e := s.submit(domain.Submission{OperationID: "input-during-checkout", WorkspaceID: w.ID, SessionID: primary.Session.ID, Text: "do not run on stale branch", Config: d.Session.Config})
		submitted <- e
	}()
	select {
	case e := <-submitted:
		release()
		<-switching
		t.Fatal("input crossed an unfinished checkout", e)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if e := <-switching; e != nil {
		t.Fatal(e)
	}
	if e := <-submitted; e != nil {
		t.Fatal("input did not follow explicit completed checkout", e)
	}
	if op := awaitReceipt(t, s, "input-during-checkout"); op.State != "confirmed" {
		t.Fatal(op)
	}
	after, _ := s.store.Session(primary.Session.ID, 0, 1)
	if after.Session.GitBranch != "other" {
		t.Fatal("input raced the checkout", after.Session)
	}
}

func TestLateGitStatusCannotMarkSuccessorContextChanged(t *testing.T) {
	s, w, dir := gitTestServer(t)
	sid := gitTestSession(t, s, w, "main", "completed")
	d, _ := s.store.Session(sid, 0, 1)
	d.Session.ThreadID = "original-source"
	s.store.Apply(sid, domain.Event{Type: "session", Session: &d.Session})
	gate, release := holdLifecycleGit(t, "status")
	finished := make(chan error, 1)
	go func() {
		_, e := gitTestCall(s, "git.status", protocol.GitQuery{WorkspaceID: w.ID, SessionID: sid})
		finished <- e
	}()
	awaitLifecycleGit(t, gate)
	// An external checkout/history handoff while the old native read waits.
	gitTestRun(t, dir, "switch", "other")
	d, _ = s.store.Session(sid, 0, 1)
	d.Session.ThreadID = "successor-source"
	d.Session.GitBranch = "other"
	d.Session.ContextChanged = false
	if _, e := s.store.Apply(sid, domain.Event{Type: "session", Session: &d.Session}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.store.Apply(sid, domain.Event{Type: "session.git", Session: &d.Session}); e != nil {
		t.Fatal(e)
	}
	release()
	<-finished
	after, _ := s.store.Session(sid, 0, 1)
	if after.Session.ThreadID != "successor-source" || after.Session.GitBranch != "other" || after.Session.ContextChanged {
		t.Fatalf("old Git read modified successor: %+v", after.Session)
	}
}

func TestPreparingOwnedRestoreReservesBeforeBackgroundStop(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := cleanupWorktreesForTest(s, time.Now()); e != nil {
		t.Fatal(e)
	}
	latest, _ := s.store.Session(d.Session.ID, 0, 1)
	if _, e := s.store.SetSessionArchived(d.Session.ID, latest.Session.Revision, false); e != nil {
		t.Fatal(e)
	}
	s.dispatchMu.Lock()
	s.build.Managed = true
	s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
		return domain.BackgroundService{State: "notRegistered"}, nil
	}
	s.dispatchMu.Unlock()
	gate, release := holdLifecycleGit(t, "add")
	submitted := make(chan error, 1)
	go func() {
		_, e := s.submit(domain.Submission{OperationID: "preparing-background-stop", WorkspaceID: w.ID, SessionID: d.Session.ID, Text: "resume", Config: d.Session.Config})
		submitted <- e
	}()
	awaitLifecycleGit(t, gate)
	result, e := s.prepareBackgroundStop("background-while-preparing")
	release()
	submissionErr := <-submitted
	if e != nil || result.State != "enabled" || s.backgroundStopping.Load() {
		t.Fatal("background stop bypassed preparing execution", result, e)
	}
	if submissionErr != nil {
		t.Fatal("refused background stop cancelled preparation", submissionErr)
	}
}
