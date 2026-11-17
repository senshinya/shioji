package worker

import (
	"context"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// Cleanup may stop the registered process tree but fail to close the adapter.
// That old adapter must not retain the logical slot after disconnect.
func TestDisconnectedRecoveryReleasesFailedRuntime(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "before-disconnect", "")
	s.dispatchMu.Lock()
	old := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	scope, err := s.executions.Existing(d.Session.ID)
	if err != nil {
		s.dispatchMu.Unlock()
		t.Fatal(err)
	}
	cmd, input, _, err := scope.StartOwned(s.ctx, "worker", []string{"/bin/cat"}, []string{"PATH=/usr/bin:/bin"}, nil)
	if err != nil {
		s.dispatchMu.Unlock()
		t.Fatal(err)
	}
	defer func() { input.Close(); scope.Stop(context.Background()); cmd.Wait() }()
	bad := &failedNativeClose{Runtime: old}
	bad.fail.Store(true)
	defer bad.fail.Store(false)
	old.memoryRuntime.Close(context.Background())
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = bad
	s.runtimeMu.Unlock()
	d.Session.Status = "unknown"
	if _, err = s.store.Apply(d.Session.ID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
		s.dispatchMu.Unlock()
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Second)
	result, err := s.reconcileLocked(ctx, protocol.RecoveryInput{SessionID: d.Session.ID, Action: "check"})
	cancel()
	retained := s.currentRuntime(d.Session.ID) == bad
	detached, detachErr := scope.IsDetached()
	stopped := scope.VerifyStopped()
	s.dispatchMu.Unlock()
	if err != nil || detachErr != nil || stopped != nil || retained || !detached {
		t.Fatal("disconnected cleanup retained the failed Agent slot", result, err, detachErr, stopped, retained, detached)
	}
	resumed := submitIdleFixture(t, s, w, "after-disconnect", d.Session.ID)
	if resumed.Session.ThreadID != d.Session.ThreadID || s.currentRuntime(d.Session.ID) == bad {
		t.Fatal("continuation reused the failed Agent or lost its original history")
	}
	counts := map[string]int{}
	for _, item := range resumed.Items {
		if item.Kind == "user" {
			counts[item.Text]++
		}
	}
	if counts["before-disconnect"] != 1 || counts["after-disconnect"] != 1 {
		t.Fatal("recovery replayed or lost accepted input", counts)
	}
}

func TestRecoveryRejectsMissingSessionBeforeNativeWork(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "pending-retirement", "")
	s.dispatchMu.Lock()
	old := s.currentRuntime(d.Session.ID)
	gen, err := s.archiveGeneration(d.Session.ID)
	if err == nil {
		_, err = s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true, gen)
	}
	if err != nil {
		s.dispatchMu.Unlock()
		t.Fatal(err)
	}
	_, err = s.reconcileLocked(s.ctx, protocol.RecoveryInput{})
	stopped := old.Stopped()
	s.dispatchMu.Unlock()
	if err == nil || stopped {
		t.Fatal("empty recovery target performed unrelated retirement", err, stopped)
	}
}
