package app

import "shioji/internal/domain"

// Feedback is an operation result for the client, separate from agent lifecycle
// notifications and authoritative pending requests. It is never persisted.
type Feedback struct {
	ID, ConnectionID, WorkspaceID, SessionID string
	Title, Body, Details                     string
	Kind                                     FeedbackKind
}
type FeedbackKind uint8

const (
	FeedbackFailure FeedbackKind = iota
	FeedbackSuccess
	FeedbackInformation
	FeedbackSubmissionUncertain
	FeedbackSubmissionFailed
)

func (m *Model) markSubmissionUncertain(in domain.Submission) {
	if m.UncertainSubmissions == nil {
		m.UncertainSubmissions = map[string]bool{}
	}
	m.UncertainSubmissions[in.OperationID] = true
	m.ReportFeedback(Feedback{ID: in.OperationID, ConnectionID: m.PendingDevices[in.OperationID], WorkspaceID: in.WorkspaceID, SessionID: in.SessionID, Kind: FeedbackSubmissionUncertain, Title: "消息是否送达仍待确认", Body: "原文与附件已保留。"})
}

func (m *Model) ReportFeedback(f Feedback) {
	if f.ID == "" {
		f.ID = domain.NewID()
	}
	if f.ConnectionID == "" {
		f.ConnectionID = m.sourceConnection
		if f.ConnectionID == "" {
			f.ConnectionID = m.ConnectionID()
		}
	}
	if f.SessionID != "" && f.WorkspaceID == "" {
		for _, s := range m.Snapshot.Sessions {
			if s.ID == f.SessionID {
				f.WorkspaceID = s.WorkspaceID
				break
			}
		}
	}
	// The UI drains the queue, but an inactive/minimized client remains bounded.
	m.Feedbacks = append(m.Feedbacks, f)
	if len(m.Feedbacks) > 30 {
		m.Feedbacks = append([]Feedback(nil), m.Feedbacks[len(m.Feedbacks)-30:]...)
	}
}
