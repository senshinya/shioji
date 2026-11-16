package worker

import (
	"context"
	"shioji/internal/domain"
	"testing"
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
