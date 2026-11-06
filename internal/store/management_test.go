package store_test

import (
	"encoding/json"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestRenameIsIndependentOfStreamProgressAndPreservesExplicitTitle(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "title-progress", WorkspaceID: w.ID, Text: "initial"})
	s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "activity", Kind: "tool", Text: "progress"}})
	renamed, err := s.RenameSession(op.SessionID, "用户名称", 0, false)
	if err != nil {
		t.Fatal("stream output prevented metadata rename", err)
	}
	s.Apply(op.SessionID, domain.Event{Type: "session.title", Text: "Codex 名称"})
	d, _ := s.Session(op.SessionID, 0, 100)
	if d.Session.Title != renamed.Title {
		t.Fatal("source name overwrote explicit title")
	}
	if _, err = s.RenameSession(op.SessionID, "stale", 0, false); err == nil {
		t.Fatal("stale rename overwrote user edit")
	}
	if _, err = s.RenameSession(op.SessionID, "", 1, true); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Session(op.SessionID, 0, 100)
	if d.Session.Title != "Codex 名称" {
		t.Fatal("reset did not use latest actual source title")
	}
}

func TestArchivedConversationRemainsSearchableWithArchiveState(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "archived-search", WorkspaceID: w.ID, Text: "find archived content"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
	d, _ := s.Session(op.SessionID, 0, 100)
	if _, err := s.SetSessionArchived(op.SessionID, d.Session.Revision, true); err != nil {
		t.Fatal(err)
	}
	results, err := s.Search("archived content")
	b, _ := json.Marshal(results)
	if err != nil || len(results) != 1 || !strings.Contains(string(b), `"archived":true`) {
		t.Fatal("archive not discoverable with its state", string(b), err)
	}
}
