package worker

import (
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"testing"
	"time"
)

func nativeWorkReply(t *testing.T) (*Server, string, string) {
	t.Helper()
	s, w := memoryWorker(t, memoryHarness{id: "hold", hold: true})
	op, err := s.submit(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "run", Config: domain.RunConfig{Model: "hold-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	d, err := s.store.Session(op.SessionID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	turn := d.Turns[0].ID
	for _, event := range []domain.Event{{SessionID: op.SessionID, Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: turn, State: "running"}}, {SessionID: op.SessionID, Type: "turn", Turn: &domain.Turn{ID: turn, Status: "completed"}}} {
		if err = s.onEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	return s, op.SessionID, turn
}
func TestNativeWorkCompletionStopWithdrawsAndReclaimsUnsettledChild(t *testing.T) {
	s, id, turn := nativeWorkReply(t)
	r := s.currentRuntime(id)
	if err := s.interruptTurn(id, turn); err != nil {
		t.Fatal("Stop rejected post-turn native work", err)
	}
	select {
	case <-r.Done():
	case <-time.After(6 * time.Second):
		t.Fatal("Stop ACK left background child running")
	}
	d, err := s.store.Session(id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Session.Completion; c != nil && c.Unread {
		t.Fatal("Stop published stale completion", c)
	}
}
func TestNativeWorkCompletionRuntimeLossRecordsInterruption(t *testing.T) {
	s, id, _ := nativeWorkReply(t)
	if err := s.onEvent(domain.Event{SessionID: id, Type: "runtime.lost"}); err != nil {
		t.Fatal(err)
	}
	d, err := s.store.Session(id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Session.Completion; c == nil || c.Pending || c.Outcome != "interrupted" || !c.Unread {
		t.Fatal("exact instance loss did not publish interrupted outcome", c)
	}
}

func TestNativeWorkCompletionShutdownWithdrawsBeforeProducerExit(t *testing.T) {
	s, id, _ := nativeWorkReply(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(s.config.Directory, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d, err := db.Session(id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Session.Completion; c != nil && (c.Pending || c.Unread) {
		t.Fatal("shutdown retained pending publication", c)
	}
	for _, item := range d.Items {
		if item.Kind == "warning" {
			t.Fatal("shutdown published stale interruption", item)
		}
	}
}
func TestNativeWorkCompletionProcessExitSettlesCapturedInstance(t *testing.T) {
	s, id, _ := nativeWorkReply(t)
	r := s.currentRuntime(id)
	if err := r.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); ; {
		d, err := s.store.Session(id, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if c := d.Session.Completion; c != nil && !c.Pending && c.Outcome == "interrupted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process exit left completion waiting", d.Session.Completion)
		}
		time.Sleep(time.Millisecond)
	}
}
