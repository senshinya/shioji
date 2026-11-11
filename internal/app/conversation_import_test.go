package app

import (
	"shioji/internal/domain"
	"testing"
)

func TestReadOnlyImportedConversationCannotSendOrConsumeDraft(t *testing.T) {
	m := model()
	m.SessionID = "imported"
	m.Snapshot.Sessions = []domain.Session{{ID: "imported", WorkspaceID: "w", Status: "completed", Origin: &domain.ConversationOrigin{SourceRef: "source", Reason: "仅可查看"}}}
	m.Drafts["session:imported"] = "keep"
	if _, err := m.BeginSubmission(); err == nil {
		t.Fatal("read-only history admitted a new empty context")
	}
	if m.Drafts["session:imported"] != "keep" || len(m.Pending) != 0 {
		t.Fatal("read-only submission consumed draft")
	}
}
