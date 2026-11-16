package store_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/store"
)

func TestCompleteConversationImportPersistsBeyondPreviewLimits(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	p := domain.ConversationPreview{Candidate: domain.ConversationCandidate{SourceRef: "long", HarnessID: "memory", Title: "Long history", Directory: w.Folders[0]}, ResumeRef: "complete-prefix"}
	for i := range 75 {
		turn := fmt.Sprintf("turn-%d", i)
		p.Turns = append(p.Turns, domain.Turn{ID: turn, Status: "completed"})
		for j, kind := range []string{"user", "tool", "assistant"} {
			p.Items = append(p.Items, domain.Item{ID: fmt.Sprintf("item-%d-%d", i, j), TurnID: turn, Kind: kind, Text: fmt.Sprintf("history %d %d", i, j), Final: true})
		}
	}
	imported, err := s.ImportConversation(w, "memory", p, domain.RunConfig{})
	if err != nil {
		t.Fatal("complete import is still limited by the preview budget", err)
	}
	d, err := s.SessionHistory(domain.HistoryQuery{SessionID: imported.ID, Rounds: 5})
	if err != nil || d.History == nil || !d.History.Older {
		t.Fatal("full import did not use ordinary paged history", d.History, err)
	}
	matches, err := s.HistoryMatches(domain.HistorySearchQuery{SessionID: imported.ID, Query: "history 0 0"})
	if err != nil || len(matches.Matches) != 1 {
		t.Fatal("earliest source input was not persisted", matches, err)
	}
}

func TestCompleteLegacyImportReplacesHistoryInPlaceAndRejectsStaleRevision(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	p := domain.ConversationPreview{Candidate: domain.ConversationCandidate{SourceRef: "legacy", HarnessID: "memory", Title: "Source title", Directory: w.Folders[0]}, Truncated: true, Reason: "partial", Turns: []domain.Turn{{ID: "t2", Status: "completed"}}, Items: []domain.Item{{ID: "a2", TurnID: "t2", Kind: "assistant", Text: "tail", Final: true}}}
	old, err := s.ImportConversation(w, "memory", p, domain.RunConfig{Model: "original-model"})
	if err != nil {
		t.Fatal(err)
	}
	repair, ok := any(s).(interface {
		CompleteConversationImport(domain.Workspace, string, domain.ConversationPreview, string, int64) (domain.Session, error)
	})
	if !ok {
		t.Fatal("legacy partial imports have no in-place completion path")
	}
	p.ResumeRef, p.Truncated, p.Reason = "full-prefix", false, ""
	p.Candidate.Title = "Changed source title"
	p.Turns = append([]domain.Turn{{ID: "t1", Status: "completed"}}, p.Turns...)
	p.Items = append([]domain.Item{{ID: "u1", TurnID: "t1", Kind: "user", Text: "earliest input", Final: true}}, p.Items...)
	if _, err = repair.CompleteConversationImport(w, "memory", p, old.ID, old.Revision+1); err == nil {
		t.Fatal("changed session was overwritten")
	}
	current, _ := s.Session(old.ID, 0, 100)
	if len(current.Items) != 1 || !current.Session.Origin.Truncated {
		t.Fatal("failed completion changed existing history")
	}
	full, err := repair.CompleteConversationImport(w, "memory", p, old.ID, old.Revision)
	if err != nil || full.ID != old.ID || full.Revision <= old.Revision || full.Title != old.Title || full.Config.Model != "original-model" || full.Origin.Truncated || full.Origin.ResumeRef != "full-prefix" {
		t.Fatal("completion lost original identity or configuration", full, err)
	}
	d, _ := s.Session(old.ID, 0, 100)
	if len(d.Items) != 2 || len(d.Turns) != 2 || d.Items[0].Text != "earliest input" {
		t.Fatal("completion did not replace the partial transcript", d)
	}
	again, err := repair.CompleteConversationImport(w, "memory", p, old.ID, old.Revision)
	if err != nil || again.Revision != full.Revision {
		t.Fatal("retry reapplied completion", again, err)
	}
}

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
