package store_test

import (
	"testing"

	"shioji/internal/domain"
)

func TestChangedImportCanBeRecheckedAfterRejectedFirstInput(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	p := domain.ConversationPreview{
		Candidate: domain.ConversationCandidate{SourceRef: "outside", HarnessID: "memory", Title: "Original name", Directory: w.Folders[0]},
		ResumeRef: "original-snapshot",
		Turns:     []domain.Turn{{ID: "t", Status: "interrupted"}},
		Items:     []domain.Item{{ID: "u", TurnID: "t", Kind: "user", Text: "original prompt"}},
	}
	original, err := s.ImportConversation(w, "memory", p, domain.RunConfig{})
	if err != nil {
		t.Fatal(err)
	}
	input := domain.Submission{OperationID: "first-send", WorkspaceID: w.ID, SessionID: original.ID, Text: "draft to retain"}
	if _, err := s.Accept(input); err != nil {
		t.Fatal(err)
	}
	settler, ok := any(s).(interface{ RejectChangedConversationInput(string, string) error })
	if !ok {
		t.Fatal("the worker cannot reject a stale import and reopen history recheck atomically")
	}
	if err := settler.RejectChangedConversationInput(input.OperationID, "source changed"); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Session(original.ID, 0, 100)
	if err != nil || !detail.Session.ReadOnlyHistory() || detail.Session.ThreadID != "" {
		t.Fatal("changed snapshot is still allowed to send", err, detail.Session)
	}
	p.ResumeRef = "updated-snapshot"
	p.Items = append(p.Items, domain.Item{ID: "late", TurnID: "t", Kind: "assistant", Text: "late reply"})
	refreshed, err := s.CompleteConversationImport(w, "memory", p, original.ID, detail.Session.Revision)
	if err != nil || refreshed.ID != original.ID || refreshed.Title != original.Title || refreshed.ReadOnlyHistory() {
		t.Fatal("failed first send blocked a valid recheck", err, refreshed)
	}
	op, err := s.Operation(input.OperationID)
	if err != nil || op.State != domain.OperationRejected {
		t.Fatal("history refresh changed the first input's rejection receipt", err, op)
	}
	detail, err = s.Session(original.ID, 0, 100)
	if err != nil || len(detail.Items) != 2 || detail.Items[1].Text != "late reply" {
		t.Fatal("recheck did not replace the stale transcript", err, detail.Items)
	}
}
