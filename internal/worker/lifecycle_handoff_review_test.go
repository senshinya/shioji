package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeChildCrashReturnsUncertainReceipt(t *testing.T) {
	s, w, _ := gitTestServer(t)
	sid := gitTestSession(t, s, w, "main", "running")
	d, _ := s.store.Session(sid, 0, 1)
	d.Session.ThreadID = "child-crash-source"
	s.store.Apply(sid, domain.Event{Type: "session", Session: &d.Session})
	child := domain.WorkActivity{ID: "child", Reference: "native-child", OriginRef: d.Session.ThreadID, Kind: "agent", TurnID: "t", State: "running", CanSteer: true}
	s.store.Apply(sid, domain.Event{Type: "work.upsert", Work: &child})
	base := &memoryRuntime{adapter: memoryHarness{id: "codex"}, done: make(chan struct{})}
	r := &heldChildRuntime{workMessageRuntime: &workMessageRuntime{Runtime: base}, entered: make(chan struct{}), release: make(chan struct{})}
	s.runtimes = map[string]harness.Runtime{sid: r}
	in := domain.WorkInput{OperationID: "input-before-native-crash", SessionID: sid, WorkID: "child", Mode: "steer", Text: "accepted once"}
	finished := make(chan domain.WorkInputOperation, 1)
	go func() { op, _ := s.sendWork(in); finished <- op }()
	<-r.entered
	base.Close(context.Background())
	var receipt domain.WorkInputOperation
	select {
	case receipt = <-finished:
	case <-time.After(time.Second):
		close(r.release)
		t.Fatal("child input did not release on process exit")
	}
	close(r.release)
	if receipt.State != "indeterminate" {
		t.Fatal("process exit left non-queryable receipt", receipt)
	}
	queried, e := s.queryWorkInput(in)
	if e != nil || queried.State != "indeterminate" {
		t.Fatal("receipt query lost native uncertainty", queried, e)
	}
}

func holdNthLifecycleGit(t *testing.T, needle string, n int) (string, func()) {
	t.Helper()
	gate, bin := t.TempDir(), t.TempDir()
	script := `#!/bin/sh
for arg in "$@"; do
 if [ "$arg" = "$SHIOJI_HANDOFF_NEEDLE" ]; then
  count=0
  if [ -f "$SHIOJI_HANDOFF_GATE/count" ]; then read count < "$SHIOJI_HANDOFF_GATE/count"; fi
  count=$((count+1))
  echo "$count" > "$SHIOJI_HANDOFF_GATE/count"
  if [ "$count" = "$SHIOJI_HANDOFF_NTH" ]; then
   touch "$SHIOJI_HANDOFF_GATE/entered"
   while [ ! -f "$SHIOJI_HANDOFF_GATE/release" ]; do /bin/sleep 0.01; done
  fi
  break
 fi
done
exec /usr/bin/git "$@"
`
	if e := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("SHIOJI_HANDOFF_GATE", gate)
	t.Setenv("SHIOJI_HANDOFF_NEEDLE", needle)
	t.Setenv("SHIOJI_HANDOFF_NTH", strconv.Itoa(n))
	release := func() { os.WriteFile(filepath.Join(gate, "release"), nil, 0600) }
	t.Cleanup(release)
	return gate, release
}

type handoffSteerRuntime struct {
	harness.Runtime
	calls atomic.Int32
}

func (r *handoffSteerRuntime) Readiness() domain.Readiness {
	x := r.Runtime.Readiness()
	x.Capabilities.Steer = true
	return x
}
func (r *handoffSteerRuntime) Steer(context.Context, domain.Session, string, string, harness.TurnInput) error {
	r.calls.Add(1)
	return nil
}
func handoffSteerFixture(t *testing.T) (*Server, protocol.SteerInput, *handoffSteerRuntime) {
	s, w, dir := gitTestServer(t)
	s.config.ServiceInstance = "handoff-instance"
	s.adapters = map[string]harness.Adapter{"codex": memoryHarness{id: "codex"}}
	sid := gitTestSession(t, s, w, "main", "running")
	d, _ := s.store.Session(sid, 0, 1)
	d.Session.ThreadID = "handoff-source"
	s.store.Apply(sid, domain.Event{Type: "session", Session: &d.Session})
	s.store.Apply(sid, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "active-turn", Status: "running"}})
	s.attachments = attachments.New(filepath.Join(t.TempDir(), "resources"))
	snapshot, e := s.storeDiffSnapshot(w.ID, domain.FileVersions{Path: "file.txt", New: "selected", Comparison: domain.DiffComparison{Repository: dir}})
	if e != nil {
		t.Fatal(e)
	}
	ref, e := s.captureDiffComment(protocol.DiffCommentInput{GitQuery: protocol.GitQuery{WorkspaceID: w.ID, SessionID: sid}, Snapshot: snapshot, Comment: "check selection", Fragments: []domain.CodeSelection{{Side: "target", Start: 0, End: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	r := &handoffSteerRuntime{Runtime: &memoryRuntime{adapter: memoryHarness{id: "codex"}, done: make(chan struct{})}}
	s.runtimes = map[string]harness.Runtime{sid: r}
	input := protocol.SteerInput{TurnID: "active-turn", Input: domain.Submission{OperationID: "review-steer", SessionID: sid, WorkspaceID: w.ID, Text: "steer selected source", Attachments: []domain.Attachment{ref}}}
	return s, input, r
}
func TestSteerPreparationRechecksDrainAfterAttachmentRead(t *testing.T) {
	s, in, r := handoffSteerFixture(t)
	gate, release := holdNthLifecycleGit(t, "status", 1)
	finished := make(chan error, 1)
	go func() { _, e := s.steer(in); finished <- e }()
	awaitLifecycleGit(t, gate)
	ready, e := s.DrainService(supervisor.Control{Instance: "handoff-instance", OperationID: "drain-before-steer", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
	if e != nil || !ready.Ready {
		release()
		t.Fatal(ready, e)
	}
	release()
	e = <-finished
	s.wg.Wait()
	if e == nil {
		t.Fatal("Steer accepted after drain closed admission")
	}
	if _, e = s.store.Operation(in.Input.OperationID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("post-drain Steer acquired operation", e)
	}
	if r.calls.Load() != 0 {
		t.Fatal("post-drain Steer reached native runtime")
	}
}
func TestSteerDispatchRechecksOwnerAfterAttachmentRead(t *testing.T) {
	s, in, r := handoffSteerFixture(t)
	gate, release := holdNthLifecycleGit(t, "status", 2)
	if _, e := s.steer(in); e != nil {
		release()
		t.Fatal(e)
	}
	awaitLifecycleGit(t, gate)
	s.dispatchMu.Lock()
	if e := s.store.ReleaseRuntime(in.Input.SessionID, "", "captured runtime retired", true); e != nil {
		s.dispatchMu.Unlock()
		release()
		t.Fatal(e)
	}
	successor := &handoffSteerRuntime{Runtime: &memoryRuntime{adapter: memoryHarness{id: "codex"}, done: make(chan struct{})}}
	s.runtimeMu.Lock()
	s.runtimes[in.Input.SessionID] = successor
	s.runtimeMu.Unlock()
	s.dispatchMu.Unlock()
	release()
	s.wg.Wait()
	if r.calls.Load() != 0 || successor.calls.Load() != 0 {
		t.Fatal("retired Steer reached a native runtime", r.calls.Load(), successor.calls.Load())
	}
	op, _ := s.store.Operation(in.Input.OperationID)
	if op.State != "abandoned" {
		t.Fatal("late preparation rewrote original receipt", op)
	}
}
func TestGitCheckoutRechecksDrainAfterGuardWait(t *testing.T) {
	s, w, dir := gitTestServer(t)
	s.config.ServiceInstance = "checkout-instance"
	sid := gitTestSession(t, s, w, "main", "completed")
	gate, release := holdNthLifecycleGit(t, "--show-toplevel", 3)
	finished := make(chan error, 1)
	go func() {
		_, e := s.checkout(protocol.CheckoutInput{GitQuery: protocol.GitQuery{WorkspaceID: w.ID, SessionID: sid}, Branch: "other"})
		finished <- e
	}()
	awaitLifecycleGit(t, gate)
	ready, e := s.DrainService(supervisor.Control{Instance: "checkout-instance", OperationID: "drain-before-checkout", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
	if e != nil || !ready.Ready {
		release()
		t.Fatal(ready, e)
	}
	release()
	e = <-finished
	if e == nil || gitTestRun(t, dir, "branch", "--show-current") != "main" {
		t.Fatal("checkout mutated after drain", e)
	}
}
func TestCollectedWorktreeReadDoesNotBlockDrain(t *testing.T) {
	s, w, d := ownedWorktreeFixture(t)
	archiveOwnedFixture(t, s, d.Session.ID)
	if e := cleanupWorktreesForTest(s, time.Now()); e != nil {
		t.Fatal(e)
	}
	s.dispatchMu.Lock()
	s.config.ServiceInstance = "collected-instance"
	s.dispatchMu.Unlock()
	gate, release := holdLifecycleGit(t, "for-each-ref")
	finished := make(chan error, 1)
	go func() {
		_, e := s.gitStatus(protocol.GitQuery{WorkspaceID: w.ID, SessionID: d.Session.ID})
		finished <- e
	}()
	awaitLifecycleGit(t, gate)
	ready := make(chan error, 1)
	go func() {
		b, _ := json.Marshal(supervisor.Control{Instance: "collected-instance", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
		_, e := s.serviceControl("worker.prepare-drain", b)
		ready <- e
	}()
	blocked := false
	select {
	case e := <-ready:
		if e != nil {
			release()
			t.Fatal(e)
		}
	case <-time.After(200 * time.Millisecond):
		blocked = true
	}
	release()
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if blocked {
		<-ready
		t.Fatal("collected worktree ref read held device dispatch")
	}
	if _, e := os.Stat(d.Session.Context.CWD); !os.IsNotExist(e) {
		t.Fatal("history read recreated execution directory", e)
	}
}
