package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

const questionDraftPrefix = "async-question:"

func QuestionAnswerKey(device, session, thread, item, question string) string {
	key, _ := json.Marshal([5]string{device, session, thread, item, question})
	return questionDraftPrefix + string(key)
}

func (m *Model) clearQuestionAnswerDrafts(device, session string) {
	key, _ := json.Marshal([2]string{device, session})
	prefix := questionDraftPrefix + strings.TrimSuffix(string(key), "]") + ","
	for answer := range m.Answers {
		key := strings.TrimPrefix(strings.TrimPrefix(answer, "source:"), "custom:")
		if strings.HasPrefix(key, prefix) {
			delete(m.Answers, answer)
		}
	}
}

func (m *Model) CanReplyToQuestion(session, thread string) bool {
	owner, err := m.queueSession(session)
	return err == nil && owner.canReplyToQuestion(thread)
}

func (m *Model) canReplyToQuestion(thread string) bool {
	if !m.Connected || !protocol.SupportsQuestionReply(m.WorkerMinor) || m.HasPendingInput() ||
		m.ReadOnlyConversationReason() != "" || m.ArchivedContext() != "" {
		return false
	}
	s := m.Details[m.SessionID].Session
	if s.ID != m.SessionID || s.WorkspaceID != m.WorkspaceID || s.ThreadID != thread ||
		s.ContextChanged || s.ExecutionGuard != "" {
		return false
	}
	ready := m.RunReadiness()
	if s.Status.Active() {
		return m.ActiveTurn() != "" && ready.Capabilities.Steer
	}
	return s.Status.CanSubmit() && !s.Goal.Active() && len(m.Queue().Items) == 0 &&
		ready.State == domain.RuntimeReady && !m.ModelUnavailable()
}

func (m *Model) BeginQuestionReply(session, thread, item, question, text string) (domain.Submission, protocol.QuestionReplyInput, error) {
	text = strings.TrimSpace(text)
	owner, err := m.queueSession(session)
	if err != nil {
		return domain.Submission{}, protocol.QuestionReplyInput{}, err
	}
	if !owner.canReplyToQuestion(thread) || text == "" || len(text) > 16<<10 || item == "" || question == "" {
		return domain.Submission{}, protocol.QuestionReplyInput{}, errors.New("暂不能提交此答案")
	}
	detail := m.Details[session]
	available := false
	for _, entry := range detail.Items {
		if entry.ReplyTo == question && (entry.Status == "sent" || entry.Status == "dispatching" || entry.Status == "unknown") {
			return domain.Submission{}, protocol.QuestionReplyInput{}, errors.New("此问题已有回答或正在确认提交")
		}
		if entry.ID == item && entry.Kind == "assistant" && entry.Final {
			available = slices.ContainsFunc(entry.Questions, func(q domain.Question) bool { return q.ID == question && !q.Secret })
		}
	}
	if !available {
		return domain.Submission{}, protocol.QuestionReplyInput{}, errors.New("问题已失效或不属于此会话")
	}
	in := domain.Submission{OperationID: domain.NewID(), SessionID: session, WorkspaceID: owner.WorkspaceID,
		HarnessID: detail.Session.HarnessID, Config: owner.Config(), Text: text,
		QuestionReply: &domain.QuestionReply{ItemID: item, QuestionID: question}}
	m.Pending[in.OperationID] = in
	m.PendingDevices[in.OperationID] = m.ConnectionID()
	m.pendingGenerations[in.OperationID] = m.Snapshot.Generation
	reply := protocol.QuestionReplyInput{OperationID: in.OperationID, SessionID: session, ItemID: item,
		QuestionID: question, Text: text, Config: in.Config}
	return in, reply, nil
}
