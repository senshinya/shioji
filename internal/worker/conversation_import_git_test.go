package worker

import (
	"shioji/internal/domain"
	"testing"
)

func TestImportedConversationCapturesGitAtFirstAdmissionThenDetectsBranchChanges(t *testing.T) {
	s, w, dir := gitTestServer(t)
	p := domain.ConversationPreview{Candidate: domain.ConversationCandidate{SourceRef: "external", HarnessID: "memory", Title: "History", Directory: dir}, ResumeRef: "opaque-prefix", Turns: []domain.Turn{{ID: "source-turn", Status: "completed"}}, Items: []domain.Item{{ID: "source-input", TurnID: "source-turn", Kind: "user", Text: "original", Status: "sent", Final: true}, {ID: "source-reply", TurnID: "source-turn", Kind: "assistant", Text: "reply", Final: true}}}
	imported, err := s.store.ImportConversation(w, "memory", p, domain.RunConfig{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if imported.GitBranch != "" {
		t.Fatal("read-only import already accepted execution branch")
	}
	input := domain.Submission{OperationID: "first-continued-input", WorkspaceID: w.ID, SessionID: imported.ID, Text: "continue", Config: domain.RunConfig{Model: "model"}}
	launch, err := prepareGitForTest(s, input, imported.Context)
	if err != nil {
		t.Fatal("Git import cannot continue", err)
	}
	op, err := s.store.AcceptForHarness(input, "memory", launch)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if d.Session.GitBranch != "main" || d.Session.GitHead == "" || d.Session.Context.Repository != dir || d.Session.Context.Branch != "main" {
		t.Fatal("first admission omitted durable Git baseline", d.Session)
	}
	gitTestRun(t, dir, "switch", "other")
	input.OperationID = "second-continued-input"
	if _, err = prepareGitForTest(s, input, d.Session.Context); err == nil {
		t.Fatal("import bypassed later branch-change protection")
	}
	current, _ := s.store.Session(imported.ID, 0, 100)
	if !current.Session.ContextChanged {
		t.Fatal("branch drift was not persisted")
	}
}
