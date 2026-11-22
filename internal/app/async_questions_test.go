package app

import (
	"context"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestAsyncQuestionReplyKeepsTheOtherPaneAndComposerDrafts(t *testing.T) {
	for _, version := range []struct {
		name  string
		minor int
	}{{"desktop", 54}, {"mobile", protocol.QuestionReplyMinor}} {
		t.Run(version.name, func(t *testing.T) {
			m := model()
			m.WorkerMinor = version.minor
			m.SessionID = "left"
			m.Readiness.Harnesses = []domain.HarnessInfo{{ID: "agent", State: domain.RuntimeReady,
				Models: m.Readiness.Models, Permissions: m.Readiness.Permissions}}
			left := domain.Session{ID: "left", WorkspaceID: "w", ThreadID: "left-thread"}
			right := domain.Session{ID: "right", WorkspaceID: "w", ThreadID: "right-thread",
				Status: domain.ExecutionCompleted, HarnessID: "agent"}
			m.Snapshot.Sessions = []domain.Session{left, right}
			m.Details[right.ID] = domain.SessionDetail{Session: right, Items: []domain.Item{{
				ID: "ask", Kind: "assistant", Final: true, Questions: []domain.Question{{ID: "q", Text: "Which?"}},
			}}}
			m.Drafts["session:left"], m.Drafts["session:right"] = "left draft", "same as the answer"

			if _, _, err := m.BeginQuestionReply(right.ID, left.ThreadID, "ask", "q", "stale"); err == nil || len(m.Pending) != 0 {
				t.Fatal("the answer used a different thread")
			}
			if _, _, err := m.BeginQuestionReply(right.ID, right.ThreadID, "missing", "q", "stale"); err == nil || len(m.Pending) != 0 {
				t.Fatal("the answer used a question missing from the owning history")
			}
			input, reply, err := m.BeginQuestionReply(right.ID, right.ThreadID, "ask", "q", "same as the answer")
			if err != nil || reply.SessionID != right.ID || reply.QuestionID != "q" || m.SessionID != left.ID {
				t.Fatalf("wrong question ownership: %+v, %v", reply, err)
			}
			m.SubmissionError(input, context.DeadlineExceeded)
			if !m.UncertainSubmissions[input.OperationID] || m.SubmissionReceiptQuery(input.OperationID).SealGeneration != "g" {
				t.Fatal("an unknown answer lost its original receipt scope")
			}
			m.ApplyOperation(domain.Operation{ID: input.OperationID, SessionID: right.ID, State: domain.OperationConfirmed})
			if m.Drafts["session:left"] != "left draft" || m.Drafts["session:right"] != "same as the answer" ||
				m.SessionID != left.ID || len(m.Pending) != 0 {
				t.Fatalf("answer altered an ordinary draft or selection: %+v", m.Drafts)
			}
		})
	}
}

func TestAsyncQuestionSupportsBothPublishedProtocolVersions(t *testing.T) {
	for _, minor := range []int{54, 55, protocol.QuestionReplyMinor, protocol.Minor} {
		if !protocol.SupportsQuestionReply(minor) {
			t.Fatalf("protocol 2.%d supports question.reply", minor)
		}
	}
	for _, minor := range []int{53, 56} {
		if protocol.SupportsQuestionReply(minor) {
			t.Fatalf("protocol 2.%d must not send question.reply", minor)
		}
	}
}
