package worker

import (
	"context"
	"encoding/json"
	"net"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/supervisor"
	"testing"
	"time"
)

func TestPrivateLifecycleTimedOutAdmissionCannotDrainLater(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "other"})
	s.config.ServiceInstance = "captured"
	client, server := net.Pipe()
	defer server.Close()
	s.dispatchMu.Lock()
	result := make(chan bool, 1)
	go func() { accepted, _ := supervisor.ServeLifecycle(server, s.DrainService); result <- accepted }()
	deadline := time.Now().Add(30 * time.Millisecond)
	client.SetDeadline(deadline)
	// Exercise the wire contract as well as final dispatch admission.
	in := map[string]any{"instance": "captured", "operationID": "late-stop", "replacement": map[string]any{"policy": "when-idle"}, "expiresAt": time.Now().Add(time.Hour), "admitBefore": deadline}
	if err := json.NewEncoder(client).Encode(in); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := json.NewDecoder(client).Decode(&reply); err == nil {
		t.Fatal("lock should outlive request deadline")
	}
	client.Close()
	s.dispatchMu.Unlock()
	select {
	case accepted := <-result:
		if accepted {
			t.Fatal("late request accepted after caller timeout")
		}
	case <-time.After(time.Second):
		t.Fatal("late handler did not settle")
	}
	if _, err := supervisor.ReadDrainIntent(s.config.Directory, "late-stop"); err == nil {
		t.Fatal("timed-out admission persisted drain intent")
	}
	if s.updating.Load() {
		t.Fatal("timed-out request closed business admission")
	}
}

type completionRetirementHarness struct{}

func (completionRetirementHarness) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "retire", Name: "retire"}
}
func (completionRetirementHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := (memoryHarness{id: "retire", hold: true}).Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &completionRetirementRuntime{idleTestRuntime: &idleTestRuntime{memoryRuntime: r.(*memoryRuntime)}}, nil
}

type completionRetirementRuntime struct{ *idleTestRuntime }

func (r *completionRetirementRuntime) Close(ctx context.Context) error {
	if r.options.Scope != nil {
		id := r.options.Scope.Session
		if err := r.options.OnEvent(domain.Event{SessionID: id, Type: "runtime.lost"}); err != nil {
			return err
		}
		if err := r.options.OnEvent(domain.Event{SessionID: id, Type: "work.upsert", Work: &domain.WorkActivity{ID: "background", Kind: "background", TurnID: "turn/reply", State: "unknown"}}); err != nil {
			return err
		}
	}
	return r.memoryRuntime.Close(ctx)
}
func TestNativeWorkCompletionEvictionReportsInterruptionBeforeInvalidation(t *testing.T) {
	s, w := memoryWorker(t, completionRetirementHarness{})
	op, err := s.submit(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "run", Config: domain.RunConfig{Model: "retire-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	r := s.currentRuntime(op.SessionID).(*completionRetirementRuntime)
	for _, e := range []domain.Event{{SessionID: op.SessionID, Type: "work.upsert", Work: &domain.WorkActivity{ID: "background", Kind: "background", TurnID: "turn/reply", State: "waiting"}}, {SessionID: op.SessionID, Type: "turn", Turn: &domain.Turn{ID: "turn/reply", Status: "completed"}}} {
		if err = r.options.OnEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	s.dispatchMu.Lock()
	err = s.reclaimRuntime(op.SessionID, time.Now(), true, s.ctx)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Session.Completion; c == nil || c.Pending || c.Outcome != "interrupted" {
		t.Fatal("eviction reported successful background completion", c)
	}
}
