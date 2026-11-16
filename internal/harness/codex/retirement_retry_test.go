package codex

import (
	"context"
	"os"
	"shioji/internal/harness/codex/wire"
	"shioji/internal/managedexec"
	"testing"
)

// Filesystem/executor requests may finish after the source process exits. A
// timed-out release is retryable once those actual retained requests settle.
func TestRuntimeRetirementRetriesAfterExecutorRequestsSettle(t *testing.T) {
	manager, err := managedexec.New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := manager.Begin("retirement-retry")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	writer := pendingActivityWriter{sent: make(chan wire.Message, 1)}
	executor := &Executor{scope: scope, stdin: writer, done: done, cancel: func() {}, requests: map[string]executorRequest{"pending": {method: "fs/readFile"}}}
	runtime := &adapterRuntime{Runtime: &Runtime{scope: scope, stdin: writer, done: done}, executor: executor}
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err = runtime.Close(expired); err == nil {
		t.Fatal("unfinished executor request was hidden")
	}
	executor.mu.Lock()
	delete(executor.requests, "pending")
	executor.mu.Unlock()
	if err = runtime.Close(context.Background()); err != nil {
		t.Fatal("completed request could never release cached close error", err)
	}
}
