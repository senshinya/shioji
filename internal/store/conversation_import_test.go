package store_test

import (
	"path/filepath"
	"sync"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/store"
)

func TestConversationImportIsAtomicAndDeduplicatesAcrossReopen(t *testing.T) {
	file := filepath.Join(t.TempDir(), "worker.sqlite")
	s, err := store.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	w := workspace(t, s)
	p := domain.ConversationPreview{Candidate: domain.ConversationCandidate{SourceRef: "opaque/source", HarnessID: "memory", Title: "Imported", Directory: w.Folders[0]}, ResumeRef: "opaque/prefix", Turns: []domain.Turn{{ID: "t", Status: "completed"}}, Items: []domain.Item{{ID: "u", TurnID: "t", Kind: "user", Text: "hello", Status: "sent", Final: true}, {ID: "a", TurnID: "t", Kind: "assistant", Text: "answer", Status: "completed", Final: true}}}
	var wg sync.WaitGroup
	results := make(chan domain.Session, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			session, err := s.ImportConversation(w, "memory", p, domain.RunConfig{Model: "model"})
			results <- session
			failures <- err
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for session := range results {
		if id != "" && id != session.ID {
			t.Fatal("duplicate source created two sessions")
		}
		id = session.ID
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	again, err := s.ImportConversation(w, "memory", p, domain.RunConfig{})
	if err != nil || again.ID != id {
		t.Fatal("retry after reconnect duplicated source", again, err)
	}
	d, err := s.Session(id, 0, 100)
	if err != nil || len(d.Items) != 2 || len(d.Turns) != 1 || len(d.Requests) != 0 || len(d.Works) != 0 || d.Session.ThreadID != "" || d.Session.Origin == nil || d.Session.Context.CWD != w.Folders[0] {
		t.Fatal("incomplete or live imported session", d, err)
	}
	snap, _ := s.Snapshot()
	if len(snap.Sessions) != 1 {
		t.Fatal("dedup lost after reopen", snap.Sessions)
	}
}

func TestConversationImportRejectsChangedWorkspaceAndReadOnlySubmission(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	p := domain.ConversationPreview{Candidate: domain.ConversationCandidate{SourceRef: "source", HarnessID: "memory", Title: "Read only", Directory: w.Folders[0]}, Items: []domain.Item{{ID: "a", Kind: "assistant", Text: "history", Final: true}}}
	p.Candidate.Model = "retired-source-model"
	foreign := p
	foreign.Candidate.Directory = t.TempDir()
	if _, err := s.ImportConversation(w, "memory", foreign, domain.RunConfig{}); err == nil {
		t.Fatal("foreign folder accepted")
	}
	stale := w
	stale.Revision++
	if _, err := s.ImportConversation(stale, "memory", p, domain.RunConfig{}); err == nil {
		t.Fatal("stale workspace accepted")
	}
	imported, err := s.ImportConversation(w, "memory", p, domain.RunConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Config.Model != "retired-source-model" {
		t.Fatal("import silently replaced source model", imported.Config)
	}
	if _, err = s.Accept(domain.Submission{OperationID: "readonly-send", WorkspaceID: w.ID, SessionID: imported.ID, Text: "continue"}); err == nil {
		t.Fatal("readonly source silently created an empty turn")
	}
	bad := p
	bad.Candidate.SourceRef = "malformed"
	bad.Items = append(bad.Items, bad.Items[0])
	if _, err = s.ImportConversation(w, "memory", bad, domain.RunConfig{}); err == nil {
		t.Fatal("duplicate imported item IDs accepted")
	}
	snap, _ := s.Snapshot()
	if len(snap.Sessions) != 1 {
		t.Fatal("failed import left partial session", snap.Sessions)
	}
}

func TestForkOfContinuedImportedConversationKeepsOneImportAnchor(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	p := domain.ConversationPreview{Candidate: domain.ConversationCandidate{SourceRef: "external", HarnessID: "memory", Title: "Imported", Directory: w.Folders[0]}, ResumeRef: "prefix", Turns: []domain.Turn{{ID: "t", Status: "completed"}}, Items: []domain.Item{{ID: "u", Kind: "user", Text: "question", TurnID: "t", Status: "sent", Final: true}, {ID: "a", Kind: "assistant", Text: "answer", TurnID: "t", Final: true}}}
	imported, err := s.ImportConversation(w, "memory", p, domain.RunConfig{})
	if err != nil {
		t.Fatal(err)
	}
	imported.ThreadID = "owned-continuation"
	if _, err = s.Apply(imported.ID, domain.Event{Type: "session", Session: &imported}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(imported.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.PrepareHistory(domain.HistoryChange{OperationID: "fork-import", SessionID: imported.ID, Revision: d.Session.Revision, TurnID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetHistoryThread(op.ID, "private-history-fork"); err != nil {
		t.Fatal(err)
	}
	fork, err := s.CommitHistory(op.ID)
	if err != nil {
		t.Fatal("import index prevents ordinary history fork", err)
	}
	if fork.Session.ID == imported.ID || fork.Session.Origin != nil || fork.Session.ForkedFrom != imported.ID {
		t.Fatal("history fork acquired import ownership", fork.Session)
	}
	again, err := s.ImportConversation(w, "memory", p, domain.RunConfig{})
	if err != nil || again.ID != imported.ID {
		t.Fatal("history fork stole source identity", again, err)
	}
}
