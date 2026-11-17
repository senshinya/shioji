package worker

import (
	"context"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"testing"
	"time"
)

type stalledDeleteCreateHarness struct{ entered, release chan struct{} }

func (stalledDeleteCreateHarness) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "stalled-create", Name: "stalled-create"}
}
func (a stalledDeleteCreateHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := (memoryHarness{id: "stalled-create"}).Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &stalledDeleteCreateRuntime{memoryRuntime: r.(*memoryRuntime), entered: a.entered, release: a.release}, nil
}

type stalledDeleteCreateRuntime struct {
	*memoryRuntime
	entered, release chan struct{}
}

func (r *stalledDeleteCreateRuntime) Create(ctx context.Context, s domain.Session) (string, error) {
	close(r.entered)
	<-r.release
	return r.memoryRuntime.Create(ctx, s)
}
func TestDeletionReleasesNativeDispatchWait(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	s, w := memoryWorker(t, stalledDeleteCreateHarness{entered: entered, release: release})
	op, err := s.submit(domain.Submission{OperationID: "stalled", WorkspaceID: w.ID, Text: "run", Config: domain.RunConfig{Model: "stalled-create-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native Create did not start")
	}
	var gate *runtimeEventGate
	func() {
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		gate = s.runtimeEvents[op.SessionID]
		archiveThenDeleteLocked(t, s, op.SessionID)
	}()
	select {
	case <-gate.done:
	case <-time.After(time.Second):
		t.Fatal("deletion disabled events but never released the captured native wait")
	}
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		reserved := len(s.dispatchReservations)
		s.mu.Unlock()
		if reserved == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deleted native call retained dispatch reservation")
		}
		time.Sleep(time.Millisecond)
	}
	if !s.toolConfigMu.TryLock() {
		t.Fatal("deleted native call retained configuration read lock")
	}
	s.toolConfigMu.Unlock()
	s.dispatchMu.Lock()
	ready, err := s.businessStopReady()
	s.dispatchMu.Unlock()
	if err != nil || !ready.Ready {
		t.Fatal("deleted native call still vetoed worker drain", ready, err)
	}
}

func TestRuntimeGateRetirementNotifiesWaitersAfterEventsWereDisabled(t *testing.T) {
	gate := &runtimeEventGate{done: make(chan struct{})}
	gate.Store(true)
	// Covers callers that disabled source events before the final retirement.
	gate.Store(false)
	gate.deactivate()
	gate.deactivate()
	select {
	case <-gate.done:
	default:
		t.Fatal("retirement never notified native waiters")
	}
	if err := gate.apply(func() error { t.Fatal("retired gate admitted late producer"); return nil }); err != errRuntimeRetired {
		t.Fatal(err)
	}
}
