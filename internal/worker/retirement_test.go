package worker

import (
	"context"
	"shioji/internal/domain"
	"testing"
	"time"
)

type tailRetirementRuntime struct {
	*idleTestRuntime
	sid string
}

func (r *tailRetirementRuntime) Close(ctx context.Context) error {
	if err := r.options.OnEvent(domain.Event{Type: "item.upsert", SessionID: r.sid, Item: &domain.Item{ID: "final-tail", Kind: "assistant", Text: "last output", Final: true}}); err != nil {
		return err
	}
	return r.idleTestRuntime.Close(ctx)
}
func TestManagedRetirementPersistsFinalEvent(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	sid := d.Session.ID
	original := s.currentRuntime(sid).(*idleTestRuntime)
	s.runtimeMu.Lock()
	s.runtimes[sid] = &tailRetirementRuntime{idleTestRuntime: original, sid: sid}
	s.runtimeMu.Unlock()
	s.dispatchMu.Lock()
	err := s.cleanManagedExecution(s.ctx, sid)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.store.Session(sid, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range after.Items {
		if item.ID == "final-tail" && item.Text == "last output" {
			return
		}
	}
	t.Fatal("retirement dropped final output before closing the event producer")
}

type retirementDeadlineRuntime struct {
	*idleTestRuntime
	budget chan time.Duration
}

func (r *retirementDeadlineRuntime) Close(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	r.budget <- time.Until(deadline)
	return r.idleTestRuntime.Close(ctx)
}
func TestRetirementKeepsFiveSecondBudgetInsideLongerShutdown(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "deadline", "")
	r := &retirementDeadlineRuntime{idleTestRuntime: s.currentRuntime(d.Session.ID).(*idleTestRuntime), budget: make(chan time.Duration, 1)}
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = r
	s.runtimeMu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	s.dispatchMu.Lock()
	err := s.retireRuntime(ctx, d.Session.ID, "update", true)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if budget := <-r.budget; budget > 5*time.Second {
		t.Fatal("per-runtime deadline exceeded force budget", budget)
	}
}
