package app

import (
	"path/filepath"
	"testing"

	"shioji/internal/domain"
)

func TestRetryMessageKeepsDraftAcrossReceiptRecovery(t *testing.T) {
	for _, state := range []domain.OperationState{domain.OperationConfirmed, domain.OperationRejected} {
		t.Run(string(state), func(t *testing.T) {
			m := model()
			session := domain.Session{ID: "s", WorkspaceID: "w", Status: domain.ExecutionFailed}
			m.SessionID = session.ID
			m.Snapshot.Sessions = []domain.Session{session}
			m.Details[session.ID] = domain.SessionDetail{Session: session}
			key := m.DraftKey()
			var draft string
			if state == domain.OperationConfirmed {
				draft = "继续"
			}
			m.Drafts[key] = draft
			in, err := m.BeginRetryMessage(session.ID)
			if err != nil || in.Text != "继续" || in.Continue {
				t.Fatalf("retry input: %+v, %v", in, err)
			}
			path := filepath.Join(t.TempDir(), "client.json")
			if err := m.Save(path); err != nil {
				t.Fatal(err)
			}
			restored := NewModel()
			if err := restored.Load(path); err != nil {
				t.Fatal(err)
			}
			restored.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: in.SessionID, State: state})
			if got := restored.Drafts[key]; got != draft || restored.SubmissionKeepsDraft(in.OperationID) {
				t.Fatalf("receipt changed draft %q to %q or left pending metadata", draft, got)
			}
		})
	}
}
