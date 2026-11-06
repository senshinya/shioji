package store_test

import (
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"strings"
	"testing"
)

func TestOversizedEventRollsBackInsteadOfPoisoningEventCursor(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "worker.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "budget", WorkspaceID: w.ID, Text: "work"})
	before, _ := s.Snapshot()
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "too-big", Kind: "tool", Name: strings.Repeat("x", 800<<10)}}); err == nil {
		t.Fatal("unpageable event persisted")
	}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "normal", Kind: "assistant", Text: "still readable"}}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(before.Sequence, 100)
	if err != nil || len(events) != 1 || events[0].Item.ID != "normal" {
		t.Fatal("event cursor poisoned by failed mutation", events, err)
	}
	detail, _ := s.Session(op.SessionID, 0, 100)
	for _, item := range detail.Items {
		if item.ID == "too-big" {
			t.Fatal("rejected event mutated history")
		}
	}
}
