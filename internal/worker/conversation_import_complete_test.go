package worker

import (
	"context"
	"fmt"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

type completeImportHarness struct{ memoryHarness }
type completeImportRuntime struct{ *memoryRuntime }

func (a completeImportHarness) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	r, err := a.memoryHarness.Start(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &completeImportRuntime{r.(*memoryRuntime)}, nil
}
func (r *completeImportRuntime) Conversations(context.Context, []string, string) (domain.ConversationCatalog, error) {
	return domain.ConversationCatalog{}, nil
}
func (r *completeImportRuntime) ReadConversation(_ context.Context, launch domain.LaunchContext, source string) (domain.ConversationPreview, error) {
	return domain.ConversationPreview{Candidate: domain.ConversationCandidate{SourceRef: source, HarnessID: "memory", Title: "Long source", Directory: launch.CWD}, Truncated: true, Reason: "preview only", Turns: []domain.Turn{{ID: "last", Status: "completed"}}, Items: []domain.Item{{ID: "tail", TurnID: "last", Kind: "assistant", Text: "preview", Final: true}}}, nil
}
func (r *completeImportRuntime) ImportConversation(ctx context.Context, launch domain.LaunchContext, source string) (domain.ConversationPreview, error) {
	p, _ := r.ReadConversation(ctx, launch, source)
	p.ResumeRef, p.Truncated, p.Reason = "full opaque prefix", false, ""
	p.Items, p.Turns = nil, nil
	for i := range 75 {
		turn := fmt.Sprintf("turn/%d", i)
		p.Turns = append(p.Turns, domain.Turn{ID: turn, Status: "completed"})
		for j, kind := range []string{"user", "tool", "assistant"} {
			p.Items = append(p.Items, domain.Item{ID: fmt.Sprintf("item/%d/%d", i, j), TurnID: turn, Kind: kind, Text: fmt.Sprintf("history %d %d", i, j), Final: true})
		}
	}
	return p, nil
}
func TestWorkerImportsCompleteHistoryInsteadOfPersistingPreview(t *testing.T) {
	s, w := memoryWorker(t, completeImportHarness{memoryHarness{id: "memory"}})
	query := protocol.ConversationQuery{WorkspaceID: w.ID, Revision: w.Revision, HarnessID: "memory", Directory: w.Folders[0], SourceRef: "external"}
	preview, err := s.previewConversation(query)
	if err != nil || !preview.Truncated || len(preview.Items) != 1 {
		t.Fatal("fixture did not exercise a bounded preview", preview, err)
	}
	imported, err := s.importConversation(protocol.ConversationImportInput{ConversationQuery: query})
	if err != nil || imported.Origin == nil || imported.Origin.ResumeRef == "" || imported.Origin.Truncated {
		t.Fatal("worker persisted a read-only preview instead of the full source", imported, err)
	}
	matches, err := s.store.HistoryMatches(domain.HistorySearchQuery{SessionID: imported.ID, Query: "history 0 0"})
	if err != nil || len(matches.Matches) != 1 {
		t.Fatal("worker lost the beginning of the imported history", matches, err)
	}
}

func TestWorkerCompletesLegacyImportWithTheSameSessionID(t *testing.T) {
	s, w := memoryWorker(t, completeImportHarness{memoryHarness{id: "memory"}})
	query := protocol.ConversationQuery{WorkspaceID: w.ID, Revision: w.Revision, HarnessID: "memory", Directory: w.Folders[0], SourceRef: "external"}
	preview, err := s.previewConversation(query)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.store.ImportConversation(w, "memory", preview, domain.RunConfig{Model: "user-selected-model"})
	if err != nil {
		t.Fatal(err)
	}
	in := protocol.ConversationImportInput{ConversationQuery: query, SessionID: old.ID, SessionRevision: old.Revision}
	full, err := s.importConversation(in)
	if err != nil || full.ID != old.ID || full.Origin.ResumeRef == "" || full.Origin.Truncated || full.Config.Model != "user-selected-model" {
		t.Fatal("legacy history could not be completed in place", full, err)
	}
	again, err := s.importConversation(in)
	if err != nil || again.Revision != full.Revision {
		t.Fatal("completion retry was not idempotent", again, err)
	}
	page, err := s.store.SessionHistory(domain.HistoryQuery{SessionID: old.ID, Rounds: 5})
	if err != nil || page.History == nil || !page.History.Older {
		t.Fatal("completed history cannot page", page.History, err)
	}
}
