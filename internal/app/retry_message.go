package app

import (
	"errors"

	"shioji/internal/domain"
)

const RetryMessageText = "继续"

func (m *Model) retryMessageTarget(id string) *Model {
	if id == "" {
		return nil
	}
	if id == m.SessionID {
		return m
	}
	target, _ := m.queueSession(id)
	return target
}

func (m *Model) CanRetryMessage(id string) bool {
	target := m.retryMessageTarget(id)
	if target == nil || !target.Connected || target.Active() || target.HasPendingInput() ||
		target.PermissionChangePending(id) || target.ActiveQueueEdit(id).ID != "" ||
		target.ReadOnlyConversationReason() != "" || target.ArchivedContext() != "" ||
		target.RunReadiness().State != domain.RuntimeReady || target.ModelUnavailable() {
		return false
	}
	detail := target.Details[id]
	if !detail.Session.Status.CanSubmit() || detail.Session.ContextChanged || detail.Session.Goal.Active() || len(detail.Queue.Items) != 0 {
		return false
	}
	for _, request := range detail.Requests {
		if request.State.AwaitingResolution() {
			return false
		}
	}
	return true
}

// BeginRetryMessage sends a new ordinary turn without taking any composer input.
func (m *Model) BeginRetryMessage(id string) (domain.Submission, error) {
	if !m.CanRetryMessage(id) {
		return domain.Submission{}, errors.New("当前会话暂时无法重试")
	}
	text := RetryMessageText
	return m.retryMessageTarget(id).beginSubmission("session.submit", &text)
}

func (m *Model) SubmissionKeepsDraft(operation string) bool {
	return m.independentInputs[operation]
}
