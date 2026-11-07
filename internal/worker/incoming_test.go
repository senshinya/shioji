package worker

import (
	"context"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"strings"
	"testing"
	"time"
)

func TestHarnessCallbackUsesDurableQueueWithoutResumingPausedInput(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha", hold: true})
	in := domain.Submission{OperationID: "initial", WorkspaceID: w.ID, Text: "first", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	op, err := s.submit(in)
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, in.OperationID)
	r := s.currentRuntime(op.SessionID).(*memoryRuntime)
	if r.options.OnMessage == nil {
		t.Fatal("owned runtime has no host callback")
	}
	d, _ := s.store.Session(op.SessionID, 0, 20)
	msg := harness.IncomingMessage{OperationID: "callback-1", SessionID: op.SessionID, SourceRef: d.Session.ThreadID, Text: "review changed"}
	if err = r.options.OnMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err = r.options.OnMessage(context.Background(), msg); err != nil {
		t.Fatal("same callback receipt cannot be retried", err)
	}
	q, _ := s.store.Queue(op.SessionID)
	if len(q.Items) != 1 || q.Items[0].Input.Text != "review changed" || q.Items[0].Input.Config != in.Config {
		t.Fatal("callback did not enter the session's durable queue", q)
	}
	original, _ := s.store.Operation(msg.OperationID)
	if original.State != "confirmed" {
		t.Fatal("acknowledged before durable acceptance", original)
	}
	msg.Text = "changed payload"
	if err = r.options.OnMessage(context.Background(), msg); err == nil {
		t.Fatal("reused callback ID accepted a different input")
	}
	q, _ = s.store.PauseQueue(op.SessionID, "user stopped")
	msg.OperationID, msg.Text = "callback-2", "next review"
	if err = r.options.OnMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	q, _ = s.store.Queue(op.SessionID)
	if !q.Paused || len(q.Items) != 2 {
		t.Fatal("callback resumed a user-paused queue", q)
	}
	// Callbacks must never gain permission or access to a different session/history.
	for _, bad := range []harness.IncomingMessage{
		{OperationID: "foreign", SessionID: "other", SourceRef: msg.SourceRef, Text: "foreign"},
		{OperationID: "stale", SessionID: op.SessionID, SourceRef: "old history", Text: "stale"},
		{OperationID: "empty", SessionID: op.SessionID, SourceRef: msg.SourceRef},
		{OperationID: "huge", SessionID: op.SessionID, SourceRef: msg.SourceRef, Text: strings.Repeat("x", 256<<10)},
	} {
		if err = r.options.OnMessage(context.Background(), bad); err == nil {
			t.Fatal("invalid callback admitted", bad.OperationID)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg.OperationID = "cancelled"
	if err = r.options.OnMessage(ctx, msg); err == nil {
		t.Fatal("cancelled callback accepted")
	}
	r.Close(context.Background())
	msg.OperationID = "retired"
	if err = r.options.OnMessage(context.Background(), msg); err == nil {
		t.Fatal("retired runtime accepted input")
	}
}

func TestHarnessCallbackAutomaticallyRunsWhenIdle(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	in := domain.Submission{OperationID: "initial", WorkspaceID: w.ID, Text: "first", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	op, err := s.submit(in)
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, in.OperationID)
	r := s.currentRuntime(op.SessionID).(*memoryRuntime)
	if r.options.OnMessage == nil {
		t.Fatal("owned runtime has no host callback")
	}
	d, _ := s.store.Session(op.SessionID, 0, 20)
	if err = r.options.OnMessage(context.Background(), harness.IncomingMessage{OperationID: "wake", SessionID: op.SessionID, SourceRef: d.Session.ThreadID, Text: "new comment"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, _ = s.store.Session(op.SessionID, 0, 50)
		for _, item := range d.Items {
			if item.Kind == "assistant" && item.Text == "from alpha: new comment" {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("durable callback never dispatched", d)
}
