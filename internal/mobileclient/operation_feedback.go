package mobileclient

import (
	"cmp"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"shioji/internal/app"
	"shioji/internal/domain"
)

// SubmissionPresentation separates a healthy in-flight send from a result
// whose receipt must be checked on the original worker.
func SubmissionPresentation(m *app.Model, workspace, session string) (sending, preparing bool, uncertain []domain.Submission) {
	owner := m.ConnectionID()
	for _, in := range m.Pending {
		if !pendingSubmissionInScope(m, in, owner, workspace, session) {
			continue
		}
		if m.UncertainSubmissions[in.OperationID] {
			uncertain = append(uncertain, in)
		} else if in.QuestionReply == nil && !in.Continue {
			sending = true
			if session == "" {
				state := m.SubmissionReceiptState(in.OperationID)
				preparing = preparing || state == domain.OperationPreparing || state == domain.OperationAccepted
			}
		}
	}
	slices.SortFunc(uncertain, func(a, b domain.Submission) int { return cmp.Compare(a.OperationID, b.OperationID) })
	return sending, preparing, uncertain
}

func CanCheckSubmission(m *app.Model, id, workspace, session string) bool {
	in, ok := m.Pending[id]
	return ok && m.UncertainSubmissions[id] && pendingSubmissionInScope(m, in, m.ConnectionID(), workspace, session)
}

func pendingSubmissionInScope(m *app.Model, in domain.Submission, owner, workspace, session string) bool {
	connection := m.PendingDevices[in.OperationID]
	if connection == "" {
		connection = "local"
	}
	return in.WorkspaceID == workspace && in.SessionID == session && connection == owner
}

type RecentOperation struct {
	DeviceID string
	app.Feedback
	dismissed          bool
	inlineAcknowledged bool
}

// OperationFeedbackHistory is an in-memory record of results from an owning worker.
type OperationFeedbackHistory []RecentOperation

func (h *OperationFeedbackHistory) Collect(deviceID string, incoming []app.Feedback, uncertain map[string]bool) {
	if deviceID == "" {
		return
	}
	*h = slices.DeleteFunc(*h, func(entry RecentOperation) bool {
		return entry.DeviceID == deviceID && entry.Kind == app.FeedbackSubmissionUncertain && !uncertain[entry.ID]
	})
	for _, entry := range incoming {
		if entry.Kind == app.FeedbackSubmissionUncertain && !uncertain[entry.ID] {
			continue
		}
		entry.Title = RedactOperationDiagnostics(entry.Title)
		entry.Body = RedactOperationDiagnostics(entry.Body)
		entry.Details = RedactOperationDiagnostics(entry.Details)
		record := RecentOperation{DeviceID: deviceID, Feedback: entry}
		*h = slices.DeleteFunc(*h, func(old RecentOperation) bool {
			same := old.DeviceID == deviceID && old.ID == entry.ID && old.WorkspaceID == entry.WorkspaceID && old.SessionID == entry.SessionID
			if same && old.Kind == entry.Kind {
				record.dismissed = old.dismissed
				record.inlineAcknowledged = old.inlineAcknowledged
			}
			return same
		})
		*h = append(*h, record)
	}
	if excess := len(*h) - 30; excess > 0 {
		*h = slices.Delete(*h, 0, excess)
	}
}

func (h OperationFeedbackHistory) Recent() []RecentOperation {
	var records []RecentOperation
	for _, entry := range slices.Backward(h) {
		if !entry.dismissed {
			records = append(records, entry)
		}
	}
	return records
}

func (h OperationFeedbackHistory) ClearSettled() {
	for i := range h {
		if h[i].Kind != app.FeedbackSubmissionUncertain {
			// Composer errors still need their original scope after the journal is cleared.
			h[i].dismissed = true
		}
	}
}

func (h OperationFeedbackHistory) For(deviceID, workspaceID, sessionID string) []app.Feedback {
	var records []app.Feedback
	for _, entry := range slices.Backward(h) {
		if !entry.dismissed && entry.DeviceID == deviceID && entry.WorkspaceID == workspaceID && entry.SessionID == sessionID {
			records = append(records, entry.Feedback)
		}
	}
	return records
}

func (h OperationFeedbackHistory) SubmissionFailure(deviceID, details string) (app.Feedback, bool) {
	for _, entry := range slices.Backward(h) {
		if entry.DeviceID == deviceID && entry.Kind == app.FeedbackSubmissionFailed && entry.Details == details {
			return entry.Feedback, true
		}
	}
	return app.Feedback{}, false
}

func (h OperationFeedbackHistory) SubmissionFailureFor(source app.SessionLocation) (app.Feedback, bool) {
	for _, entry := range slices.Backward(h) {
		if !entry.inlineAcknowledged && entry.isSubmissionFailureFor(source) {
			return entry.Feedback, true
		}
	}
	return app.Feedback{}, false
}

func (h OperationFeedbackHistory) AcknowledgeSubmissionFailures(source app.SessionLocation) {
	for i := range h {
		if h[i].isSubmissionFailureFor(source) {
			h[i].inlineAcknowledged = true
		}
	}
}

func (entry RecentOperation) isSubmissionFailureFor(source app.SessionLocation) bool {
	return entry.Kind == app.FeedbackSubmissionFailed &&
		entry.DeviceID == source.DeviceID && entry.WorkspaceID == source.WorkspaceID && entry.SessionID == source.SessionID
}

var (
	operationURL           = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	operationAuthorization = regexp.MustCompile(`(?i)((?:"|')?authorization(?:"|')?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\r\n]+)`)
	operationCredential    = regexp.MustCompile(`(?i)((?:"|')?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|(?:client[_-]?)?secret)(?:"|')?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;}]+)`)
)

func RedactOperationDiagnostics(text string) string {
	text = operationURL.ReplaceAllStringFunc(text, func(value string) string {
		address, err := url.Parse(value)
		if err != nil || address.Host == "" {
			return "[链接已隐藏]"
		}
		return address.Scheme + "://" + address.Host
	})
	text = operationAuthorization.ReplaceAllString(text, "${1}[已隐藏]")
	text = operationCredential.ReplaceAllString(text, "${1}[已隐藏]")
	return strings.TrimSpace(text)
}
