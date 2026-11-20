package worker

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestLookupCanSealMissingInputAgainstALateSubmit(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "audit"})
	in := domain.Submission{OperationID: "never-received", WorkspaceID: w.ID, Text: "keep this draft"}
	wrong, _ := json.Marshal(protocol.OperationQuery{ID: in.OperationID, SealGeneration: "other-journal"})
	if _, err := s.handle("operation", wrong); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("different journal sealed the original input: %v", err)
	}
	query, _ := json.Marshal(protocol.OperationQuery{ID: in.OperationID, SealGeneration: s.store.Generation()})
	out, err := s.handle("operation", query)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := out.(domain.Operation)
	if !ok || receipt.ID != in.OperationID || receipt.State != domain.OperationRejected || receipt.SessionID != "" {
		t.Fatalf("missing input was not settled: %+v", out)
	}
	body, _ := json.Marshal(in)
	if _, err := s.handle("session.submit", body); err == nil {
		t.Fatal("late input was accepted after its terminal receipt")
	}
	snapshot, err := s.store.Snapshot()
	if err != nil || len(snapshot.Sessions) != 0 {
		t.Fatalf("late input created a session: %+v, %v", snapshot.Sessions, err)
	}
}

func TestLookupDoesNotSealInputDuringPreflight(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "audit"})
	in := domain.Submission{OperationID: "in-preflight", WorkspaceID: w.ID, Text: "wait for acceptance",
		Config: domain.RunConfig{Model: "audit-model", Permission: "limited"}}
	s.dispatchMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.dispatchMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := s.submit(in)
		done <- err
	}()
	preparing := func() bool {
		s.inputReceiptMu.Lock()
		defer s.inputReceiptMu.Unlock()
		return s.inputReceipts[in.OperationID] > 0
	}
	for until := time.Now().Add(time.Second); !preparing(); {
		if time.Now().After(until) {
			t.Fatal("submission never entered preflight")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.inputReceipt(protocol.OperationQuery{ID: in.OperationID, SealGeneration: s.store.Generation()}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("live preflight should retain the right to accept the original input: %v", err)
	}
	s.dispatchMu.Unlock()
	locked = false
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := awaitReceipt(t, s, in.OperationID)
	if got.State != domain.OperationConfirmed {
		t.Fatalf("original input was not accepted: %+v", got)
	}
}
