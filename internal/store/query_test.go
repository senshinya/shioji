package store

import (
	"encoding/json"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestSearchReturnsTheMessageAndContextAroundALateUTF8Match(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, _ := s.CreateWorkspace("Example", []string{t.TempDir()})
	op, _ := s.Accept(domain.Submission{OperationID: "target", WorkspaceID: w.ID, Text: "Original title"})
	text := strings.Repeat("早期说明。", 80) + "\n这里需要旋转，再继续。"
	s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "late-reply", Kind: "assistant", Text: text, Final: true}})
	results, err := s.Search("旋转")
	if err != nil || len(results) != 1 {
		t.Fatal(results, err)
	}
	b, _ := json.Marshal(results[0])
	var hit struct {
		ItemID               string `json:"itemId"`
		MatchStart, MatchEnd int
	}
	json.Unmarshal(b, &hit)
	if hit.ItemID != "late-reply" || hit.MatchStart != 1213 || hit.MatchEnd != 1219 || !strings.Contains(results[0].Snippet, "旋转") || strings.ContainsAny(results[0].Snippet, "\n\r\t") || len([]rune(results[0].Snippet)) > 160 {
		t.Fatal("search lost the exact UTF-8 target or returned only the beginning of the message", string(b))
	}
}

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

func TestGitContextUpdatePreservesConcurrentCompletion(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, _ := s.CreateWorkspace("Test", []string{t.TempDir()})
	op, _ := s.Accept(domain.Submission{OperationID: "race", WorkspaceID: w.ID, Text: "test"}, domain.LaunchContext{CWD: w.Folders[0], Branch: "main"})
	observed, _ := s.Session(op.SessionID, 0, 1)
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	observed.Session.ContextChanged = true
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "session.git", Session: &observed.Session}); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Session(op.SessionID, 0, 1)
	if d.Session.Status != "completed" || !d.Session.ContextChanged {
		t.Fatalf("Git observation overwrote lifecycle: %+v", d.Session)
	}
	// A late runtime/thread snapshot must not erase independently detected context changes.
	observed.Session.ContextChanged = false
	observed.Session.ThreadID = "thread"
	observed.Session.Status = "completed"
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "session", Session: &observed.Session}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Session(op.SessionID, 0, 1)
	if !d.Session.ContextChanged || d.Session.ThreadID != "thread" {
		t.Fatal("runtime snapshot erased Git context or thread binding")
	}
}
