package worker

import (
	"context"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestIdleSettlementDoesNotPinSuccessorGC(t *testing.T) {
	s, w := memoryWorker(t, completionRetirementHarness{})
	cfg := domain.RunConfig{Model: "retire-model", Permission: "limited"}
	op, err := s.submit(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "run", Config: cfg})
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
	scope, err := s.executions.Existing(op.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.VerifyStopped(); err != nil {
		t.Fatal("old registered generation not physically stopped", err)
	}
	next, err := s.submit(domain.Submission{OperationID: "successor", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "next", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, next.ID)
	fresh := s.currentRuntime(op.SessionID).(*completionRetirementRuntime)
	if fresh == r {
		t.Fatal("fixture did not create successor")
	}
	if err = fresh.options.OnEvent(domain.Event{SessionID: op.SessionID, Type: "turn", Turn: &domain.Turn{ID: "turn/successor", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	state, err := s.store.RuntimeGCIdle(op.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("old generation positively stopped; fresh parent=%s works=%+v GC=%+v", d.Session.Status, d.Works, state)
	if len(d.Works) != 1 || d.Works[0].State != "stopped" {
		t.Errorf("verified stopped old work retained an active projection: %+v", d.Works)
	}
	if !state.Idle {
		t.Fatal("verified retired work permanently vetoes successor idle GC")
	}
}

func TestIdleSettlementUnknownHistoryDoesNotImplyStopped(t *testing.T) {
	s, w := memoryWorker(t, completionRetirementHarness{})
	op, err := s.submit(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "run", Config: domain.RunConfig{Model: "retire-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	for _, e := range []domain.Event{{Type: "work.upsert", Work: &domain.WorkActivity{ID: "background", Kind: "background", TurnID: "turn/reply", State: "waiting"}}, {Type: "turn", Turn: &domain.Turn{ID: "turn/reply", Status: "completed"}}} {
		if _, err = s.store.Apply(op.SessionID, e); err != nil {
			t.Fatal(err)
		}
	}
	bad := &failedNativeClose{Runtime: s.currentRuntime(op.SessionID)}
	bad.fail.Store(true)
	defer bad.fail.Store(false)
	s.runtimeMu.Lock()
	s.runtimes[op.SessionID] = bad
	s.runtimeMu.Unlock()
	s.dispatchMu.Lock()
	err = s.reclaimRuntime(op.SessionID, time.Now(), true, context.Background())
	s.dispatchMu.Unlock()
	if err == nil {
		t.Fatal("unsettled native producer was reported as fully stopped")
	}
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Works) != 1 || d.Works[0].State != "unknown" {
		t.Fatal("unproven cleanup invented execution stop", d.Works)
	}
	cfg := domain.RunConfig{Model: "retire-model", Permission: "limited"}
	next, err := s.submit(domain.Submission{OperationID: "successor", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "next", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, next.ID)
	fresh := s.currentRuntime(op.SessionID).(*completionRetirementRuntime)
	if err = fresh.options.OnEvent(domain.Event{SessionID: op.SessionID, Type: "turn", Turn: &domain.Turn{ID: "turn/successor", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	idle, err := s.store.RuntimeGCIdle(op.SessionID)
	if err != nil || !idle.Idle {
		t.Fatal("released old runtime still pins successor", idle, err)
	}
	// A fresh active observation restores ownership and must protect the new owner.
	work := d.Works[0]
	work.State = "running"
	if err = fresh.options.OnEvent(domain.Event{SessionID: op.SessionID, Type: "work.upsert", Work: &work}); err != nil {
		t.Fatal(err)
	}
	idle, err = s.store.RuntimeGCIdle(op.SessionID)
	if err != nil || idle.Idle {
		t.Fatal("current live work lost GC protection", idle, err)
	}
}

func TestIdleSettlementCurrentUnknownWorkRemainsProtected(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "reply", "")
	if _, err := s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "current", Kind: "background", TurnID: d.Turns[0].ID, State: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	idle, err := s.store.RuntimeGCIdle(d.Session.ID)
	if err != nil || idle.Idle {
		t.Fatal("unreleased unknown activity lost GC protection", idle, err)
	}
}
