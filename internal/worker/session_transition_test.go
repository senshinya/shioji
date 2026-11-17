package worker

import (
	"context"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"sync"
	"testing"
	"time"
)

func assertOtherSessionAdmitted(t *testing.T, s *Server, w domain.Workspace, model string, release chan struct{}) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := s.submit(domain.Submission{OperationID: "unrelated-transition", WorkspaceID: w.ID, Text: "independent", Config: domain.RunConfig{Model: model, Permission: "limited"}})
		done <- err
	}()
	select {
	case err := <-done:
		close(release)
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		close(release)
		<-done
		t.Error("native session transition blocked unrelated admission")
	}
}

func TestCompactionDoesNotOwnDeviceAdmissionWhileAwaitingNativeReply(t *testing.T) {
	s, sid, r := compactWorker(t)
	d, _ := s.store.Session(sid, 0, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	r.before = func(domain.Session) { close(entered); <-release }
	done := make(chan error, 1)
	go func() {
		_, err := compactCall(s, "session.context.compact", domain.ContextActionRequest{OperationID: "blocked-compact", SessionID: sid, Revision: d.Session.Revision})
		done <- err
	}()
	<-entered
	w, _ := s.store.Workspace(d.Session.WorkspaceID)
	assertOtherSessionAdmitted(t, s, w, "alpha-model", release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type blockingUnloadRuntime struct {
	harness.Runtime
	entered, release chan struct{}
}

func (r *blockingUnloadRuntime) Unload(ctx context.Context, _ domain.Session) error {
	close(r.entered)
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestReloadDoesNotOwnDeviceAdmissionWhileUnloading(t *testing.T) {
	s, w := memoryWorker(t, reloadHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	entered, release := make(chan struct{}), make(chan struct{})
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = &blockingUnloadRuntime{Runtime: s.runtimes[d.Session.ID], entered: entered, release: release}
	s.runtimeMu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := s.reloadSession(domain.SessionReloadRequest{OperationID: "blocked-unload", SessionID: d.Session.ID, Revision: d.Session.Revision}, false)
		done <- err
	}()
	<-entered
	assertOtherSessionAdmitted(t, s, w, "idle-model", release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type blockingInspectRuntime struct {
	enteredOnce sync.Once
	harness.Runtime
	entered, release chan struct{}
}

func (r *blockingInspectRuntime) Inspect(ctx context.Context, s domain.Session) (domain.RecoveryHistory, error) {
	r.enteredOnce.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return r.Runtime.Inspect(ctx, s)
	case <-ctx.Done():
		return domain.RecoveryHistory{}, ctx.Err()
	}
}
func TestRecoveryDoesNotOwnDeviceAdmissionWhileInspecting(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	entered, release := make(chan struct{}), make(chan struct{})
	s.dispatchMu.Lock()
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = &blockingInspectRuntime{Runtime: s.runtimes[d.Session.ID], entered: entered, release: release}
	s.runtimeMu.Unlock()
	d.Session.Status = "unknown"
	if _, err := s.store.Apply(d.Session.ID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := s.reconcile(protocol.RecoveryInput{SessionID: d.Session.ID, Action: "check"})
		done <- err
	}()
	<-entered
	assertOtherSessionAdmitted(t, s, w, "idle-model", release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Automatic and explicit reconciliation may observe the same instance. The
// blocking fixture signals its first entry but must allow repeated inspection.
func TestRecoveryInspectFixtureAllowsRepeatedObservation(t *testing.T) {
	release := make(chan struct{})
	close(release)
	r := &blockingInspectRuntime{Runtime: &memoryRuntime{}, entered: make(chan struct{}), release: release}
	for range 2 {
		if _, err := r.Inspect(context.Background(), domain.Session{}); err != nil {
			t.Fatal(err)
		}
	}
}
