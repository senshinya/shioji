package store

import (
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func TestSearchMatchesLiteralConversationAndPreservesOwnership(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, _ := s.CreateWorkspace("Example", []string{t.TempDir()})
	op, _ := s.Accept(domain.Submission{OperationID: "x", WorkspaceID: w.ID, Text: "First title"})
	s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "reply", Kind: "assistant", Text: "Contains 100%_literal 标记", Final: true}})
	results, err := s.Search("100%_")
	if err != nil || len(results) != 1 || results[0].SessionID != op.SessionID || results[0].WorkspaceID != w.ID {
		t.Fatalf("literal search %+v %v", results, err)
	}
	results, err = s.Search("nonexistent")
	if err != nil || len(results) != 0 {
		t.Fatalf("unrelated results %+v %v", results, err)
	}
}
