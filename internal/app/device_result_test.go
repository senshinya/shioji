package app

import (
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func TestDeviceRepliesRejectForeignWorkspaceSessionAndOperation(t *testing.T) {
	m := scopedDeviceModel()
	for _, test := range []struct {
		job Job
		out any
	}{
		{Job{Method: "session.submit", Input: m.Pending["b-op"]}, domain.Operation{ID: "b-op", SessionID: "sA", State: "confirmed"}},
		{Job{Method: "turn.remove", Input: protocol.QueueInput{SessionID: "sB"}}, domain.QueueState{SessionID: "sA", Revision: 1}},
		{Job{Method: "workspace.create", Input: protocol.WorkspaceInput{Name: "x", Folders: []string{"/remote"}}}, domain.Workspace{ID: "wA", DeviceID: "A"}},
	} {
		body, _ := json.Marshal(test.out)
		if err := m.validateDeviceReply("air", test.job, body); err == nil {
			t.Fatal("foreign reply accepted", test.job.Method)
		}
	}
}
func TestDeviceSearchRejectsAnotherDevicesSession(t *testing.T) {
	m := scopedDeviceModel()
	if m.validateSearchResults("air", []domain.SearchResult{{SessionID: "sA", WorkspaceID: "wB"}}) == nil {
		t.Fatal("search can navigate to another device's session")
	}
	if err := m.validateSearchResults("air", []domain.SearchResult{{SessionID: "sB", WorkspaceID: "wB"}}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryReceiptPreservesReferenceValueIdentityAcrossWire(t *testing.T) {
	for _, test := range []struct {
		name string
		refs *domain.HistoryReferences
	}{
		{"legacy", nil},
		{"explicit-removal", &domain.HistoryReferences{}},
		{"inline", &domain.HistoryReferences{
			Attachments:        []domain.Attachment{{ID: "file", Name: "main.go", SourcePath: "/project/main.go", SHA256: "snapshot"}},
			Skills:             []domain.SkillReference{{ID: "opaque-skill", Name: "check"}},
			ReferencePositions: []domain.ReferencePosition{{SkillID: "opaque-skill", Position: 1}, {AttachmentID: "file", Position: 8}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := scopedDeviceModel()
			in := domain.HistoryChange{OperationID: "edit", SessionID: "sB", Revision: 4, ItemID: "user", Restart: true, Text: "revised", Config: domain.RunConfig{Permission: "read-only"}, References: test.refs}
			source := domain.Session{ID: "sB", WorkspaceID: "wB", Context: domain.LaunchContext{DeviceID: "B"}}
			body, err := json.Marshal(domain.HistoryOperation{ID: "edit", Input: in, Source: source, Session: source, State: "complete"})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.validateDeviceReply("air", Job{Method: "session.restart", Input: in}, body); err != nil {
				t.Fatal("successful history receipt rejected after JSON round trip", err)
			}
			for _, mutate := range []func(*domain.HistoryOperation){
				func(op *domain.HistoryOperation) { op.Input.Text = "other message" },
				func(op *domain.HistoryOperation) { op.Input.Config.Permission = "yolo" },
				func(op *domain.HistoryOperation) { op.Input.OperationID = "other operation" },
				func(op *domain.HistoryOperation) {
					op.Input.References = &domain.HistoryReferences{Skills: []domain.SkillReference{{ID: "foreign", Name: "check"}}}
				},
			} {
				var changed domain.HistoryOperation
				if err := json.Unmarshal(body, &changed); err != nil {
					t.Fatal(err)
				}
				mutate(&changed)
				wrong, err := json.Marshal(changed)
				if err != nil {
					t.Fatal(err)
				}
				if m.validateDeviceReply("air", Job{Method: "session.restart", Input: in}, wrong) == nil {
					t.Fatal("modified history input was accepted")
				}
			}
		})
	}
}
