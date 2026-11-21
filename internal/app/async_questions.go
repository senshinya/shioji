package app

import (
	"errors"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (m *Model) CanReplyToQuestion() bool {
	if !m.Connected || m.WorkerMinor < protocol.QuestionReplyMinor || m.HasPendingInput() ||
		m.ReadOnlyConversationReason() != "" || m.ArchivedContext() != "" {
		return false
	}
	s := m.Details[m.SessionID].Session
	if s.ID == "" || s.ContextChanged || s.ExecutionGuard != "" {
		return false
	}
	ready := m.RunReadiness()
	if s.Status.Active() {
		return m.ActiveTurn() != "" && ready.Capabilities.Steer
	}
	return s.Status.CanSubmit() && !s.Goal.Active() && len(m.Queue().Items) == 0 &&
		ready.State == domain.RuntimeReady && !m.ModelUnavailable()
}

func (m *Model) BeginQuestionReply(item, question, text string) (domain.Submission, protocol.QuestionReplyInput, error) {
	text = strings.TrimSpace(text)
	if !m.CanReplyToQuestion() || text == "" || len(text) > 16<<10 {
		return domain.Submission{}, protocol.QuestionReplyInput{}, errors.New("暂不能提交此答案")
	}
	s := m.Details[m.SessionID].Session
	in := domain.Submission{OperationID: domain.NewID(), SessionID: s.ID, WorkspaceID: s.WorkspaceID,
		Config: m.Config(), Text: text, QuestionReply: &domain.QuestionReply{ItemID: item, QuestionID: question}}
	m.Pending[in.OperationID] = in
	m.PendingDevices[in.OperationID] = m.ConnectionID()
	m.pendingGenerations[in.OperationID] = m.Snapshot.Generation
	reply := protocol.QuestionReplyInput{OperationID: in.OperationID, SessionID: s.ID, ItemID: item,
		QuestionID: question, Text: text, Config: in.Config}
	return in, reply, nil
}
