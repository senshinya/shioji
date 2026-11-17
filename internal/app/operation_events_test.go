package app

import (
	"fmt"
	"testing"

	"shioji/internal/domain"
)

func TestOperationEventsKeepRecentReceiptsCurrentAndBounded(t *testing.T) {
	m := model()
	for i := range 110 {
		op := domain.Operation{ID: fmt.Sprint(i), SessionID: "s", State: domain.OperationAccepted}
		if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, Type: "submission", SessionID: "s", Operation: &op}); err != nil {
			t.Fatal(err)
		}
	}
	op := domain.Operation{ID: "109", SessionID: "s", State: domain.OperationConfirmed}
	if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, Type: "submission", SessionID: "s", Operation: &op}); err != nil {
		t.Fatal(err)
	}
	if len(m.Snapshot.Operations) != 100 {
		t.Fatal("recent receipt projection is missing or unbounded", len(m.Snapshot.Operations))
	}
	if got := m.Snapshot.Operations[0]; got.ID != "109" || got.State != domain.OperationConfirmed {
		t.Fatal("live confirmation did not replace the stale accepted receipt", got)
	}
	count := 0
	for _, receipt := range m.Snapshot.Operations {
		if receipt.ID == "109" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("confirmation inserted a duplicate receipt", count)
	}
}
