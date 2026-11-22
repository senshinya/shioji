package app

import (
	"testing"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestMessageInAnotherSessionKeepsBothDraftsScoped(t *testing.T) {
	m := model()
	m.WorkerMinor = protocol.RecoveryMinor
	m.ConnectionEpoch = "epoch"
	m.SessionID = "left"
	m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: "right-workspace", DeviceID: "d"})
	left := domain.Session{ID: "left", WorkspaceID: "w", ThreadID: "left-thread", Status: domain.ExecutionCompleted}
	right := domain.Session{ID: "right", WorkspaceID: "right-workspace", ThreadID: "right-thread", Status: domain.ExecutionRunning}
	m.Snapshot.Sessions = []domain.Session{left, right}
	m.Details[right.ID] = domain.SessionDetail{Session: right, Turns: []domain.Turn{{ID: "right-turn", Status: domain.ExecutionRunning}}}
	m.Drafts["session:left"], m.Drafts["session:right"] = "left draft", "right draft"

	in, method, turn, err := m.BeginMessageInSession(right.ID, false)
	if err != nil || in.SessionID != right.ID || in.WorkspaceID != right.WorkspaceID || in.Text != "right draft" ||
		method != "turn.enqueue" || turn != "" {
		t.Fatalf("wrong submission route: %+v, %s, %s, %v", in, method, turn, err)
	}
	if m.Pending[in.OperationID].Text != in.Text {
		t.Fatalf("owning model did not retain the operation: %+v", m.Pending)
	}
	if m.SessionID != left.ID || m.WorkspaceID != left.WorkspaceID {
		t.Fatalf("primary selection moved: %q / %q", m.SessionID, m.WorkspaceID)
	}
	if m.SubmissionReceiptQuery(in.OperationID).SealGeneration != "g" {
		t.Fatal("recovery scope was not retained")
	}
	m.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: right.ID, State: domain.OperationConfirmed})
	if m.Drafts["session:left"] != "left draft" || m.Drafts["session:right"] != "" {
		t.Fatalf("confirmation cleared the wrong draft: %+v", m.Drafts)
	}
}

func TestMessageInAnotherSessionRejectsForeignWorker(t *testing.T) {
	m := model()
	m.SessionID = "left"
	m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: "other-workspace", DeviceID: "other-worker"})
	m.Snapshot.Sessions = []domain.Session{{ID: "right", WorkspaceID: "other-workspace", ThreadID: "other-thread"}}
	m.Drafts["session:right"] = "draft"
	if _, _, _, err := m.BeginMessageInSession("right", false); err == nil || len(m.Pending) != 0 {
		t.Fatalf("foreign session was allowed to submit: %v", err)
	}
}

func TestContinuationInAnotherSessionPreservesSelectionDraftsAndQueue(t *testing.T) {
	m := model()
	m.ConnectionEpoch = "epoch"
	m.WorkerMinor = protocol.RecoveryMinor
	m.SessionID = "left"
	m.Readiness.HarnessID = "alpha"
	m.Readiness.Harnesses = []domain.HarnessInfo{{
		ID: "alpha", State: domain.RuntimeReady, Models: m.Readiness.Models,
		Permissions: m.Readiness.Permissions, Capabilities: domain.HarnessCapabilities{Continue: true},
	}}
	m.Snapshot.Workspaces = append(m.Snapshot.Workspaces,
		domain.Workspace{ID: "right-workspace", DeviceID: "d"},
		domain.Workspace{ID: "foreign-workspace", DeviceID: "foreign"})
	left := domain.Session{ID: "left", WorkspaceID: "w", Status: domain.ExecutionCompleted, HarnessID: "alpha"}
	right := domain.Session{ID: "right", WorkspaceID: "right-workspace", ThreadID: "original-thread",
		Status: domain.ExecutionInterrupted, HarnessID: "alpha",
		Recovery: &domain.SessionRecovery{Phase: domain.RecoveryReady, CanContinue: true}}
	foreign := domain.Session{ID: "foreign-session", WorkspaceID: "foreign-workspace"}
	m.Snapshot.Sessions = []domain.Session{left, right, foreign}
	m.Details[right.ID] = domain.SessionDetail{Session: right, Queue: domain.QueueState{Paused: true}}
	m.Drafts["session:left"], m.Drafts["session:right"] = "left draft", "right draft"

	if _, err := m.BeginContinuationInSession(foreign.ID); err == nil || len(m.Pending) != 0 {
		t.Fatal("the continuation accepted a foreign session")
	}
	in, err := m.BeginContinuationInSession(right.ID)
	if err != nil || in.SessionID != right.ID || in.WorkspaceID != right.WorkspaceID ||
		in.OperationID == "" || !in.Continue || in.Text != domain.ContinuationPrompt {
		t.Fatalf("unexpected continuation: %+v, %v", in, err)
	}
	if _, err := m.BeginContinuationInSession(right.ID); err == nil || len(m.Pending) != 1 {
		t.Fatal("a second continuation bypassed the pending receipt")
	}
	if m.SubmissionReceiptQuery(in.OperationID).SealGeneration != "g" {
		t.Fatal("the continuation lost its original Worker generation")
	}
	m.Accepted(in, domain.Operation{ID: in.OperationID, SessionID: right.ID, State: domain.OperationConfirmed})
	if m.SessionID != left.ID || m.WorkspaceID != left.WorkspaceID ||
		m.Drafts["session:left"] != "left draft" || m.Drafts["session:right"] != "right draft" || !m.Details[right.ID].Queue.Paused {
		t.Fatal("confirmation changed selection, a draft, or the paused queue")
	}
}
