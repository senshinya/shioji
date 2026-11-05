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
