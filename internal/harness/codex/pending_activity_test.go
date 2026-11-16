package codex

import (
	"context"
	"encoding/json"
	"shioji/internal/harness/codex/wire"
	"testing"
)

type pendingActivityWriter struct{ sent chan wire.Message }

func (w pendingActivityWriter) Write(raw []byte) (int, error) {
	var m wire.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, err
	}
	w.sent <- m
	return len(raw), nil
}
func (w pendingActivityWriter) Close() error { return nil }
func TestPendingActivityDoesNotCountHistoryObservationAsExecution(t *testing.T) {
	writer := pendingActivityWriter{make(chan wire.Message, 2)}
	r := &Runtime{stdin: writer, pending: map[string]chan wire.Message{}, done: make(chan struct{})}
	adapter := &adapterRuntime{Runtime: r}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{}, 2)
	go func() {
		_ = r.call(ctx, "thread/read", map[string]string{"threadId": "observed"}, nil)
		finished <- struct{}{}
	}()
	<-writer.sent
	if adapter.PendingOperations() != 0 {
		t.Fatal("read-only history observation counted as execution")
	}
	go func() {
		_ = r.call(ctx, "turn/steer", map[string]string{"threadId": "observed"}, nil)
		finished <- struct{}{}
	}()
	<-writer.sent
	if adapter.PendingOperations() != 1 {
		t.Fatal("unsettled steering request not protected")
	}
	cancel()
	<-finished
	<-finished
	if adapter.PendingOperations() != 0 {
		t.Fatal("completed/cancelled requests remained busy")
	}
}
