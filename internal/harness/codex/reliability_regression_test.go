package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"strings"
	"testing"
	"time"

	"shioji/internal/harness/codex/wire"
)

func TestNativeWriteHonorsRPCDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	defer reader.Close()
	r := &Runtime{stdin: writer, done: make(chan struct{}), pending: make(map[string]chan wire.Message)}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.call(ctx, "turn/start", struct{}{}, nil) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the blocked write should fail")
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("the blocked write exceeded its deadline")
	}
}

type auditWriter struct{ io.Writer }

func (auditWriter) Close() error { return nil }

func TestApprovalWithNoWrittenBytesCanBeAnsweredAgain(t *testing.T) {
	n := NewNormalizer()
	n.Bind("t", "s", true)
	pending := requestFor(t, n, `{"id":"approval","method":"item/commandExecution/requestApproval","params":{"threadId":"t","turnId":"u","itemId":"i","command":"echo fixture","availableDecisions":["decline","cancel"]}}`)
	got := domain.PendingRequest{}
	reader, writer := io.Pipe()
	defer writer.Close()
	reader.Close()
	r := &Runtime{stdin: writer, normalizer: n, done: make(chan struct{}), onEvent: func(e domain.Event) error {
		if e.Request != nil {
			got = *e.Request
		}
		return nil
	}}
	answer := domain.Answer{RequestID: pending.ID, Revision: pending.Revision, Decision: "decline"}
	if err := r.Respond(answer); err == nil || IsUncertain(err) {
		t.Fatalf("failed approval looked conclusive: %v", err)
	}
	if got.ID != pending.ID || got.State != "pending" || got.Revision <= pending.Revision {
		t.Fatalf("stale state not observed: %+v", got)
	}
	if _, err := n.Response(domain.Answer{RequestID: got.ID, Revision: got.Revision, Decision: "decline"}); err != nil {
		t.Fatalf("request could not be answered again: %v", err)
	}
}

func TestLargeNativeErrorCanBePersistedBeforeReadingTheNextFrame(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := db.CreateWorkspace("isolated", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "native-output", WorkspaceID: w.ID, Text: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	n := NewNormalizer()
	n.Bind("source-thread", op.SessionID, true)
	cmd := exec.Command("/bin/sleep", "5")
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	r := &Runtime{cmd: cmd, normalizer: n, done: make(chan struct{}), onEvent: func(e domain.Event) error {
		_, err := db.Apply(op.SessionID, e)
		return err
	}}
	var wireLines bytes.Buffer
	encoder := json.NewEncoder(&wireLines)
	frame := map[string]any{"method": "turn/completed", "params": map[string]any{
		"threadId": "source-thread", "turn": map[string]any{
			"id": "native-turn", "status": "failed", "error": map[string]string{"message": strings.Repeat("<", 90<<10)},
		},
	}}
	if err = encoder.Encode(frame); err != nil {
		t.Fatal(err)
	}
	if err = encoder.Encode(map[string]any{"method": "thread/name/updated", "params": map[string]string{"threadId": "source-thread", "threadName": "still consuming events"}}); err != nil {
		t.Fatal(err)
	}
	r.read(&wireLines)
	d, err := db.Session(op.SessionID, 0, 1)
	if err != nil || d.Session.Status != "failed" || d.Session.Title != "still consuming events" {
		t.Fatalf("native reader stopped at the long error: status=%s title=%s err=%v native=%v", d.Session.Status, d.Session.Title, err, r.err)
	}
}
