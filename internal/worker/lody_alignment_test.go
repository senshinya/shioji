package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/supervisor"
	"sync/atomic"
	"testing"
	"time"
)

// Only a live turn or its initialization owns upgrade admission. Historical
// uncertainty stays recorded, but is not a reservation held by a live caller.
func TestManagedUpgradeIgnoresReleasedBackgroundAndHistoricalState(t *testing.T) {
	for _, state := range []string{"background", "guard", "delivery", "request"} {
		t.Run(state, func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			d := submitIdleFixture(t, s, w, "first", "")
			var err error
			switch state {
			case "background":
				_, err = s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "bg", Kind: "background", TurnID: d.Turns[0].ID, State: "waiting", OriginRef: d.Session.ThreadID}})
			case "guard":
				err = s.store.GuardExecution(d.Session.ID, "old-generation")
			case "delivery":
				err = s.store.SetOperation("first", "indeterminate", "old receipt unknown")
			case "request":
				_, err = s.store.Apply(d.Session.ID, domain.Event{Type: "request.open", Request: &domain.PendingRequest{ID: "old", Kind: "question", State: "pending"}})
			}
			if err != nil {
				t.Fatal(err)
			}
			s.config.ServiceInstance = "lody-alignment"
			body, _ := json.Marshal(supervisor.Control{Instance: s.config.ServiceInstance, OperationID: "update", Replacement: domain.WorkerReplacement{Policy: "when-idle"}})
			out, err := s.serviceControl("worker.prepare-drain", body)
			if err != nil || !out.(domain.WorkerUpdateResult).Ready {
				t.Fatal("released state still holds upgrade admission", out, err)
			}
		})
	}
}

type failingRetirementRuntime struct {
	*idleTestRuntime
	fail atomic.Bool
}

func (r *failingRetirementRuntime) Close(ctx context.Context) error {
	if r.fail.Load() {
		return errors.New("fixture cleanup unconfirmed")
	}
	return r.idleTestRuntime.Close(ctx)
}

func TestFailedRetirementReleasesAgentSlotWithoutReusingOldInstance(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	old := &failingRetirementRuntime{idleTestRuntime: s.currentRuntime(d.Session.ID).(*idleTestRuntime)}
	old.fail.Store(true)
	t.Cleanup(func() { old.fail.Store(false) })
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = old
	s.runtimeMu.Unlock()
	now := time.Now()
	sweepIdle(t, s, now)
	if err := s.reclaimIdleRuntimes(func() time.Time { return now.Add(20 * time.Minute) }); err == nil {
		t.Fatal("cleanup failure was hidden")
	}
	after, err := s.store.Session(d.Session.ID, 0, 100)
	if err != nil || !after.Session.Status.CanSubmit() {
		t.Fatal("failed old Agent still holds the execution slot", after.Session, err)
	}
	resumed := submitIdleFixture(t, s, w, "second", d.Session.ID)
	if s.currentRuntime(d.Session.ID) == old || resumed.Session.ThreadID != d.Session.ThreadID {
		t.Fatal("successor reused the failed Agent or lost its history")
	}
	if err = old.options.OnEvent(domain.Event{SessionID: d.Session.ID, Type: "turn", Turn: &domain.Turn{ID: d.Turns[0].ID, Status: "running"}}); err != nil {
		t.Fatal(err)
	}
	if err = old.options.OnLost(); err != nil {
		t.Fatal(err)
	}
	latest, err := s.store.Session(d.Session.ID, 0, 100)
	if err != nil || latest.Session.Status != "completed" || latest.Turns[0].Status == "running" {
		t.Fatal("late retired callbacks changed the successor", latest.Session, err)
	}
	old.fail.Store(false)
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) && !old.Stopped() {
		time.Sleep(10 * time.Millisecond)
	}
	if !old.Stopped() {
		t.Fatal("detached Agent was not retried in the background")
	}
}

func TestManagedUpgradeStillWaitsForLiveTurn(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "other", hold: true})
	op, err := s.submit(domain.Submission{OperationID: "live", WorkspaceID: w.ID, Text: "live", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	ready, err := s.businessStopReady()
	if err != nil || ready.Ready {
		t.Fatal("live turn lost its admission", ready, err)
	}
}

func TestManagedUpgradeDoesNotTreatOrphanedRunningHistoryAsLiveTurn(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	old := s.currentRuntime(d.Session.ID)
	t.Cleanup(func() { old.Close(context.Background()) })
	s.runtimeMu.Lock()
	delete(s.runtimes, d.Session.ID)
	s.runtimeMu.Unlock()
	d.Session.Status = "running"
	if _, err := s.store.Apply(d.Session.ID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
		t.Fatal(err)
	}
	ready, err := s.businessStopReady()
	if err != nil || !ready.Ready {
		t.Fatal("orphaned history holds a live machine-turn reservation", ready, err)
	}
}

func TestStoppedInitializationDoesNotHoldUpgradeOrShutdown(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	s, w := memoryWorker(t, blockedCreationHarness{started: started, release: release})
	op, err := s.submit(domain.Submission{OperationID: "initializing", WorkspaceID: w.ID, Text: "must not send", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("initialization did not start")
	}
	if err = s.interruptTurn(op.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	ready, err := s.businessStopReady()
	if err != nil || !ready.Ready {
		t.Fatal("stopped creation still holds upgrade admission", ready, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal("shutdown still awaits the abandoned creation promise", err)
	}
}

func TestDetachRetainsAdapterWhileOldCallbacksDrain(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	old := &failingRetirementRuntime{idleTestRuntime: s.currentRuntime(d.Session.ID).(*idleTestRuntime)}
	old.fail.Store(true)
	t.Cleanup(func() { old.fail.Store(false) })
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = old
	gate := s.runtimeEvents[d.Session.ID]
	s.runtimeMu.Unlock()
	now := time.Now()
	sweepIdle(t, s, now)
	gate.mu.RLock()
	held := true
	defer func() {
		if held {
			gate.mu.RUnlock()
		}
	}()
	retired := make(chan error, 1)
	go func() { retired <- s.reclaimIdleRuntimes(func() time.Time { return now.Add(20 * time.Minute) }) }()
	scope, err := s.executions.Existing(d.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(time.Second); ; {
		detached, err := scope.IsDetached()
		if err != nil {
			t.Fatal(err)
		}
		if detached {
			break
		}
		if time.Now().After(until) {
			t.Fatal("detachment did not reach the callback barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Let the independent reaper observe this generation before its logical
	// release can finish. It must retain the adapter as well as process tickets.
	time.Sleep(1200 * time.Millisecond)
	gate.mu.RUnlock()
	held = false
	if err = <-retired; err == nil {
		t.Fatal("fixture close failure was hidden")
	}
	old.fail.Store(false)
	for until := time.Now().Add(7 * time.Second); !old.Stopped(); {
		if time.Now().After(until) {
			t.Fatal("early scope recovery forgot the retired adapter")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type lateSteerHarness struct{ entered, release chan struct{} }

func (a lateSteerHarness) Descriptor() domain.HarnessDescriptor {
	return (stalledSteerHarness{}).Descriptor()
}
func (a lateSteerHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := (memoryHarness{id: "stalled", hold: true}).Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &lateSteerRuntime{&stalledSteerRuntime{memoryRuntime: r.(*memoryRuntime), entered: a.entered, release: a.release}}, nil
}

type lateSteerRuntime struct{ *stalledSteerRuntime }

func (r *lateSteerRuntime) Steer(context.Context, domain.Session, string, string, harness.TurnInput) error {
	r.entered <- struct{}{}
	<-r.release
	return &harness.DeliveryError{Uncertain: true, Message: "late retired response"}
}

func TestRetiredSteerFinalizerDoesNotPauseSuccessorQueue(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	s, w := memoryWorker(t, lateSteerHarness{entered, release})
	op, err := s.submit(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first", Config: domain.RunConfig{Model: "stalled-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	d, _ := s.store.Session(op.SessionID, 0, 1)
	if _, err = s.steer(protocol.SteerInput{Input: domain.Submission{OperationID: "old-steer", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "old steer", Config: d.Session.Config}, TurnID: d.Turns[0].ID}); err != nil {
		t.Fatal(err)
	}
	<-entered
	s.dispatchMu.Lock()
	err = s.retireRuntime(s.ctx, op.SessionID, "stop-timeout", true)
	if err == nil {
		err = s.detachFailedRuntime(op.SessionID, errors.New("old steering call unconfirmed"))
	}
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.submit(domain.Submission{OperationID: "second", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "second", Config: d.Session.Config})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, next.ID)
	q, err := s.store.Queue(op.SessionID)
	if err != nil || q.Paused {
		t.Fatal("successor did not own the fresh queue", q, err)
	}
	release <- struct{}{}
	for until := time.Now().Add(150 * time.Millisecond); time.Now().Before(until); {
		q, err = s.store.Queue(op.SessionID)
		if err != nil || q.Paused {
			t.Fatal("retired steering response paused the successor", q, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestArchiveGarbageRetainsDetachedGenerationRecords(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	old := &failingRetirementRuntime{idleTestRuntime: s.currentRuntime(d.Session.ID).(*idleTestRuntime)}
	old.fail.Store(true)
	t.Cleanup(func() { old.fail.Store(false) })
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = old
	s.runtimeMu.Unlock()
	s.dispatchMu.Lock()
	failure := s.retireRuntime(s.ctx, d.Session.ID, "cleanup", true)
	err := s.detachFailedRuntime(d.Session.ID, failure)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.executions.Begin(d.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Fence(); err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Lock()
	err = s.cleanArchiveGarbageItem(store.ArchiveGarbage{SessionID: d.Session.ID})
	s.dispatchMu.Unlock()
	if err == nil {
		t.Fatal("permanent cleanup removed a pending predecessor's ownership records")
	}
	if _, err = os.Stat(filepath.Join(scope.Directory, old.options.Scope.Generation, "scope.json")); err != nil {
		t.Fatal("detached ownership cannot be retried after permanent cleanup", err)
	}
}

func TestDetachedCleanupGuardsCheckoutEvenAfterProcessesExit(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	gitTestRun(t, w.Folders[0], "init", "-b", "main")
	d := submitIdleFixture(t, s, w, "first", "")
	old := &failingRetirementRuntime{idleTestRuntime: s.currentRuntime(d.Session.ID).(*idleTestRuntime)}
	old.fail.Store(true)
	t.Cleanup(func() { old.fail.Store(false) })
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = old
	s.runtimeMu.Unlock()
	s.dispatchMu.Lock()
	failure := s.retireRuntime(s.ctx, d.Session.ID, "cleanup", true)
	err := s.detachFailedRuntime(d.Session.ID, failure)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.executions.Existing(d.Session.ID)
	if err != nil || scope.VerifyStopped() != nil {
		t.Fatal("fixture has live registered processes", err)
	}
	status, err := queryGit(s.ctx, w.Folders[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = s.checkoutGuard(status, ""); err == nil {
		t.Fatal("unsettled retired cleanup released the shared checkout")
	}
}

type lateCreateHarness struct {
	starts           *atomic.Int32
	entered, release chan struct{}
}

func (a lateCreateHarness) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "late-create", Name: "Late create"}
}
func (a lateCreateHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := (memoryHarness{id: "late-create"}).Start(ctx, o)
	if err != nil {
		return nil, err
	}
	var generation int32
	if o.Scope != nil {
		generation = a.starts.Add(1)
	}
	return &lateCreateRuntime{memoryRuntime: r.(*memoryRuntime), generation: generation, entered: a.entered, release: a.release}, nil
}

type lateCreateRuntime struct {
	*memoryRuntime
	generation       int32
	entered, release chan struct{}
}

func (r *lateCreateRuntime) Create(context.Context, domain.Session) (string, error) {
	if r.generation == 1 {
		r.entered <- struct{}{}
		<-r.release
	}
	if r.generation == 1 {
		return "old-thread", nil
	}
	return "new-thread", nil
}

func TestRetiredCreateCannotReplaceSuccessorThread(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	s, w := memoryWorker(t, lateCreateHarness{starts: &atomic.Int32{}, entered: entered, release: release})
	cfg := domain.RunConfig{Model: "late-create-model", Permission: "limited"}
	op, err := s.submit(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "first", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	s.dispatchMu.Lock()
	err = s.retireRuntime(s.ctx, op.SessionID, "stop-timeout", true)
	if err == nil {
		err = s.detachFailedRuntime(op.SessionID, errors.New("old creation did not settle"))
	}
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.submit(domain.Submission{OperationID: "second", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "second", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, next.ID)
	release <- struct{}{}
	for until := time.Now().Add(150 * time.Millisecond); time.Now().Before(until); {
		d, err := s.store.Session(op.SessionID, 0, 100)
		if err != nil || d.Session.ThreadID != "new-thread" {
			t.Fatal("retired creation overwrote the successor's history", d.Session, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLateRetirementDoesNotClearSuccessorBinding(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	gate := s.runtimeEvents[d.Session.ID]
	s.mu.Lock()
	held := true
	defer func() {
		if held {
			s.mu.Unlock()
		}
	}()
	retired := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 100*time.Millisecond)
		defer cancel()
		s.dispatchMu.Lock()
		err := s.retireRuntime(ctx, d.Session.ID, "cleanup", true)
		s.dispatchMu.Unlock()
		retired <- err
	}()
	for until := time.Now().Add(time.Second); gate.Load(); {
		if time.Now().After(until) {
			t.Fatal("retirement did not settle its captured producer")
		}
		time.Sleep(time.Millisecond)
	}
	<-retired
	s.retirementMu.Lock()
	pending := s.retirements[d.Session.ID]
	s.retirementMu.Unlock()
	replacement, err := (idleHarness{}).Start(s.ctx, harness.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = replacement
	s.runtimeMu.Unlock()
	s.bound[d.Session.ID] = true
	s.mu.Unlock()
	held = false
	if pending != nil {
		<-pending.done
	}
	s.mu.Lock()
	bound := s.bound[d.Session.ID]
	s.mu.Unlock()
	if !bound {
		t.Fatal("late retired cleanup cleared the successor's binding")
	}
}
