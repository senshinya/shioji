package worker

import (
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"testing"
)

type requestBoundaryRuntime struct {
	harness.Runtime
	accepted []domain.Answer
}

func (r *requestBoundaryRuntime) Respond(a domain.Answer) error {
	r.accepted = append(r.accepted, a)
	return nil
}
func TestWorkerChecksChildRequestRevisionAndExecutionBeforeReply(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	op, err := s.submit(domain.Submission{OperationID: "request-start", WorkspaceID: w.ID, Text: "start", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, "request-start")
	runtime := &requestBoundaryRuntime{Runtime: s.currentRuntime(op.SessionID)}
	s.runtimeMu.Lock()
	s.runtimes[op.SessionID] = runtime
	s.runtimeMu.Unlock()
	d, _ := s.store.Session(op.SessionID, 0, 1)
	child := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "turn/request-start", OriginRef: d.Session.ThreadID, State: "running"}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &child}); err != nil {
		t.Fatal(err)
	}
	req := domain.PendingRequest{ID: "child-request", WorkID: "child", TurnID: child.TurnID, Kind: "approval", State: "pending", Revision: 3, Choices: []domain.RequestChoice{{ID: "allow", Label: "允许这一次"}}}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "request.open", Request: &req}); err != nil {
		t.Fatal(err)
	}
	respond := func(revision int64) error {
		b, _ := json.Marshal(protocol.AnswerInput{SessionID: op.SessionID, Answer: domain.Answer{RequestID: req.ID, Revision: revision, Decision: "allow"}})
		_, e := s.handle("request.respond", b)
		return e
	}
	if err = respond(2); err == nil || len(runtime.accepted) != 0 {
		t.Fatal("worker forwarded a stale answer", err, runtime.accepted)
	}
	if err = respond(3); err != nil || len(runtime.accepted) != 1 {
		t.Fatal("current sourced answer rejected", err)
	}
	child.State = "stopped"
	s.store.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &child})
	if err = respond(3); err == nil || len(runtime.accepted) != 1 {
		t.Fatal("worker forwarded reply to stopped work", err)
	}
}
