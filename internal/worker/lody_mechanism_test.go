package worker

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"sync/atomic"
	"testing"
	"time"
)

type lodyMechanismLateInterruptHarness struct{ entered chan struct{} }

func (a lodyMechanismLateInterruptHarness) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "lodyMechanism-stop", Name: "LodyMechanism stop"}
}
func (a lodyMechanismLateInterruptHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, e := (memoryHarness{id: "lodyMechanism-stop", hold: true}).Start(ctx, o)
	if e != nil {
		return nil, e
	}
	return &lodyMechanismLateInterruptRuntime{memoryRuntime: r.(*memoryRuntime), entered: a.entered}, nil
}

type lodyMechanismLateInterruptRuntime struct {
	*memoryRuntime
	entered chan struct{}
}

func (r *lodyMechanismLateInterruptRuntime) Interrupt(ctx context.Context, _ domain.Session, _ string) error {
	r.entered <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}
func TestLodyMechanismOldStopMustNotRetireNewTurn(t *testing.T) {
	entered := make(chan struct{}, 1)
	s, w := memoryWorker(t, lodyMechanismLateInterruptHarness{entered: entered})
	cfg := domain.RunConfig{Model: "lodyMechanism-stop-model", Permission: "limited"}
	op, e := s.submit(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first", Config: cfg})
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, op.ID)
	old := s.currentRuntime(op.SessionID).(*lodyMechanismLateInterruptRuntime)
	d, e := s.store.Session(op.SessionID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	oldTurn := d.Turns[0].ID
	if e = s.interruptTurn(op.SessionID, oldTurn); e != nil {
		t.Fatal(e)
	}
	<-entered
	if e = old.options.OnEvent(domain.Event{SessionID: op.SessionID, Type: "turn", Turn: &domain.Turn{ID: oldTurn, Status: "completed"}}); e != nil {
		t.Fatal(e)
	}
	next, e := s.submit(domain.Submission{OperationID: "second", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "second", Config: cfg})
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, next.ID)
	if s.currentRuntime(op.SessionID) != old {
		t.Fatal("fixture unexpectedly replaced the runtime")
	}
	select {
	case <-old.Done():
		t.Fatal("old Stop's 5s timeout retired the same runtime while its next turn was active")
	case <-time.After(6 * time.Second):
	}
}
func TestLodyMechanismIdleCleanupMustNotHoldGlobalDispatch(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	r := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	r.closing, r.release = entered, release
	now := time.Now()
	sweepIdle(t, s, now)
	cleaned := make(chan error, 1)
	go func() { cleaned <- s.reclaimIdleRuntimes(func() time.Time { return now.Add(20 * time.Minute) }) }()
	<-entered
	submitted := make(chan error, 1)
	go func() {
		_, e := s.submit(domain.Submission{OperationID: "unrelated", WorkspaceID: w.ID, Text: "new session", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
		submitted <- e
	}()
	blocked := false
	select {
	case e := <-submitted:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	close(release)
	if e := <-cleaned; e != nil {
		t.Error(e)
	}
	if blocked {
		if e := <-submitted; e != nil {
			t.Error(e)
		}
		t.Fatal("one idle runtime's Close held the global dispatch lock and blocked an unrelated new session")
	}
}

type lodyMechanismFailedReconfigurationRuntime struct {
	*changingRuntime
	fail atomic.Bool
}

func (r *lodyMechanismFailedReconfigurationRuntime) Close(ctx context.Context) error {
	if r.fail.Load() {
		return errors.New("temporary old-runtime cleanup failure")
	}
	return r.changingRuntime.Close(ctx)
}
func TestLodyMechanismFailedReconfigurationMustDetachOldAgent(t *testing.T) {
	a := &changingCatalog{keepOld: true}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	op, e := s.submit(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, op.ID)
	original := s.currentRuntime(op.SessionID).(*changingRuntime)
	d, e := s.store.Session(op.SessionID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if e = original.options.OnEvent(domain.Event{SessionID: op.SessionID, Type: "turn", Turn: &domain.Turn{ID: d.Turns[0].ID, Status: "completed"}}); e != nil {
		t.Fatal(e)
	}
	bad := &lodyMechanismFailedReconfigurationRuntime{changingRuntime: original}
	bad.fail.Store(true)
	t.Cleanup(func() { bad.fail.Store(false) })
	s.runtimeMu.Lock()
	s.runtimes[op.SessionID] = bad
	s.runtimeMu.Unlock()
	a.version.Store(2)
	for i := 0; i < 2; i++ {
		s.dispatchMu.Lock()
		ctx, cancel := context.WithTimeout(s.ctx, time.Second)
		_, e = s.sessionRuntime(ctx, op.SessionID)
		cancel()
		s.dispatchMu.Unlock()
		t.Logf("new-version adoption attempt %d: %v", i+1, e)
	}
	if s.currentRuntime(op.SessionID) == bad {
		t.Fatal("failed retirement in installation adoption remained the current Agent; successor never became admissible")
	}
}

type shutdownCallbackRuntime struct {
	*idleTestRuntime
	worker *Server
}

func (r *shutdownCallbackRuntime) Close(ctx context.Context) error {
	r.worker.dispatchMu.Lock()
	r.worker.dispatchMu.Unlock()
	return r.idleTestRuntime.Close(ctx)
}
func TestLodyMechanismShutdownLetsCapturedCallbacksDrain(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	old := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = &shutdownCallbackRuntime{idleTestRuntime: old, worker: s}
	s.runtimeMu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatal("shutdown held dispatch while waiting for a captured callback", err)
	}
}

type pendingIdleRuntime struct{ *idleTestRuntime }

func (r *pendingIdleRuntime) PendingOperations() int { return 1 }
func TestUnsettledNativeRequestIsNotReusedByNextTurn(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	old := &pendingIdleRuntime{idleTestRuntime: s.currentRuntime(d.Session.ID).(*idleTestRuntime)}
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = old
	s.runtimeMu.Unlock()
	next := submitIdleFixture(t, s, w, "second", d.Session.ID)
	if s.currentRuntime(d.Session.ID) == old || !old.Stopped() {
		t.Fatal("new turn reused an unsettled native control request")
	}
	if next.Session.ThreadID != d.Session.ThreadID {
		t.Fatal("recycling native requests replaced the conversation")
	}
}
