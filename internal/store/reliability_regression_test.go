package store_test

import (
	"context"
	"strings"
	"testing"

	"shioji/internal/domain"
)

func TestShutdownPreservesProofOfUnhandedInput(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "unhanded-shutdown", WorkspaceID: w.ID, Text: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordShutdownUnknown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	restored, err := s.Operation(op.ID)
	if err != nil || restored.State != domain.OperationRejected {
		t.Fatalf("state after shutdown: %+v, %v", restored, err)
	}
}

func TestRestartRepairsDispatchingBubbleWithExistingConfirmedReceipt(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "split-confirm", WorkspaceID: w.ID, Text: "one bubble"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, domain.OperationDispatched, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "native-turn", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, domain.OperationConfirmed, ""); err != nil {
		t.Fatal(err)
	}
	// Recreate the stale projection produced by an older worker.
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{
		ID: "input:" + op.ID, Kind: "user", Text: "one bubble", Status: "dispatching", Final: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	receipt, _ := s.Operation(op.ID)
	d, err := s.Session(op.SessionID, 0, 10)
	if err != nil || receipt.State != domain.OperationConfirmed || len(d.Items) != 1 || d.Items[0].Status != "sent" || d.Items[0].TurnID != "" {
		t.Fatalf("unexpected persisted relation: %+v %+v %v", receipt, d.Items, err)
	}
}

func TestEscapedStreamingTextRespectsJournalByteBudget(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "escaped", WorkspaceID: w.ID, Text: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"plain", "escaped"} {
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: id, Kind: "assistant"}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 90; i++ {
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.delta", ItemID: "plain", Text: strings.Repeat("x", 1<<10)}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.delta", ItemID: "escaped", Text: strings.Repeat("<", 1<<10)}); err != nil {
			t.Fatalf("valid stream exceeded journal budget at %d KiB: %v", i+1, err)
		}
	}
}
