package mobileclient

import (
	"strings"
	"testing"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestSubmissionPresentationRespectsReceiptStateAndOwner(t *testing.T) {
	m := app.NewModel()
	m.Pending["a"] = domain.Submission{OperationID: "a", WorkspaceID: "workspace", Text: "home draft"}
	m.Pending["b"] = domain.Submission{OperationID: "b", WorkspaceID: "other-workspace", Text: "another draft"}
	m.Pending["c"] = domain.Submission{OperationID: "c", WorkspaceID: "workspace", Text: "another device"}
	m.Pending["d"] = domain.Submission{OperationID: "d", WorkspaceID: "workspace", SessionID: "session", Text: "sent from chat"}
	m.PendingDevices["c"] = "remote"
	m.UncertainSubmissions = map[string]bool{"b": true, "c": true, "d": true}
	if sending, uncertain := SubmissionPresentation(m, "workspace", ""); !sending || len(uncertain) != 0 {
		t.Fatalf("ordinary home send received a recovery panel: sending=%t, receipts=%v", sending, uncertain)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if CanCheckSubmission(m, id, "workspace", "") {
			t.Fatalf("healthy or foreign operation %s could be looked up from this home draft", id)
		}
	}
	m.UncertainSubmissions["a"] = true
	if sending, uncertain := SubmissionPresentation(m, "workspace", ""); sending || len(uncertain) != 1 || uncertain[0].OperationID != "a" {
		t.Fatalf("only the unknown result from the active workspace belongs here: sending=%t, receipts=%v", sending, uncertain)
	}
	if !CanCheckSubmission(m, "a", "workspace", "") || !CanCheckSubmission(m, "d", "workspace", "session") {
		t.Fatal("the original workspace and session must be able to look up their own receipt")
	}
	delete(m.Pending, "a")
	if CanCheckSubmission(m, "a", "workspace", "") {
		t.Fatal("a stale control must not look up an already settled operation")
	}
}

func TestOperationFeedbackFollowsItsDeviceAndSession(t *testing.T) {
	var history OperationFeedbackHistory
	const secret = "fixture-secret"
	message := app.Feedback{ID: "receipt", WorkspaceID: "project", SessionID: "session", Title: "操作失败",
		Details: "token=" + secret + " https://example.com/auth?token=" + secret}
	history.Collect("A", []app.Feedback{message}, nil)
	message.Title = "设备 B 的操作"
	history.Collect("B", []app.Feedback{message}, nil)
	items := history.For("A", "project", "session")
	if len(items) != 1 || items[0].Title != "操作失败" || strings.Contains(items[0].Details, secret) {
		t.Fatalf("operation feedback missed scope or redaction: %#v", items)
	}
	if other := history.For("A", "project", "other"); len(other) != 0 {
		t.Fatal("a different session could read the feedback", other)
	}
}

func TestOperationFeedbackReplacesAnUncertainReceipt(t *testing.T) {
	var history OperationFeedbackHistory
	receipt := app.Feedback{ID: "receipt", WorkspaceID: "project", SessionID: "session",
		Kind: app.FeedbackSubmissionUncertain, Title: "仍待确认"}
	history.Collect("A", []app.Feedback{receipt}, map[string]bool{"receipt": true})
	if got := history.For("A", "project", "session"); len(got) != 1 || got[0].Kind != app.FeedbackSubmissionUncertain {
		t.Fatal("uncertain receipt is missing", got)
	}
	receipt.Kind, receipt.Title = app.FeedbackSubmissionFailed, "消息未发送"
	history.Collect("A", []app.Feedback{receipt}, nil)
	if got := history.For("A", "project", "session"); len(got) != 1 || got[0].Kind != app.FeedbackSubmissionFailed {
		t.Fatal("known failure did not replace the uncertain receipt", got)
	}
}

func TestInlineSubmissionFailureUsesTheOriginalScope(t *testing.T) {
	var history OperationFeedbackHistory
	left := app.SessionLocation{DeviceID: "mac", WorkspaceID: "work", SessionID: "left"}
	right := app.SessionLocation{DeviceID: "mac", WorkspaceID: "work", SessionID: "right"}
	failure := app.Feedback{
		ID: "left-send", WorkspaceID: left.WorkspaceID, SessionID: left.SessionID,
		Kind: app.FeedbackSubmissionFailed, Details: "runtime unavailable",
	}
	history.Collect(left.DeviceID, []app.Feedback{failure}, nil)
	failure.ID, failure.SessionID = "right-send", right.SessionID
	history.Collect(right.DeviceID, []app.Feedback{failure}, nil)
	if got, ok := history.SubmissionFailureFor(left); !ok || got.ID != "left-send" {
		t.Fatal("a later failure with the same reason hid the original session's feedback")
	}
	history.AcknowledgeSubmissionFailures(right)
	if _, ok := history.SubmissionFailureFor(right); ok {
		t.Fatal("returning to the draft did not close its failure")
	}
	if got, ok := history.SubmissionFailureFor(left); !ok || got.ID != "left-send" {
		t.Fatal("returning to one draft closed the other session's failure")
	}
	if len(history.For(right.DeviceID, right.WorkspaceID, right.SessionID)) != 1 {
		t.Fatal("closing the inline failure removed it from recent operations")
	}
	history.Collect(right.DeviceID, []app.Feedback{failure}, nil)
	if _, ok := history.SubmissionFailureFor(right); ok {
		t.Fatal("the same receipt reopened an acknowledged failure")
	}
	failure.ID = "right-retry"
	history.Collect(right.DeviceID, []app.Feedback{failure}, nil)
	if got, ok := history.SubmissionFailureFor(right); !ok || got.ID != failure.ID {
		t.Fatal("a later attempt with the same reason should receive its own failure")
	}
}

func TestRecentOperationsRetainDeviceIdentityAndPendingReceipts(t *testing.T) {
	var history OperationFeedbackHistory
	failure := app.Feedback{ID: "operation", WorkspaceID: "workspace", SessionID: "session", Kind: app.FeedbackSubmissionFailed, Details: "connection closed"}
	history.Collect("device-a", []app.Feedback{failure}, nil)
	pending := app.Feedback{ID: failure.ID, WorkspaceID: failure.WorkspaceID, SessionID: failure.SessionID, Kind: app.FeedbackSubmissionUncertain}
	history.Collect("device-b", []app.Feedback{pending}, map[string]bool{pending.ID: true})
	recent := history.Recent()
	if len(recent) != 2 || recent[0].DeviceID != "device-b" || recent[1].DeviceID != "device-a" {
		t.Fatalf("recent operations lost their device identity or order: %#v", recent)
	}
	history.ClearSettled()
	recent = history.Recent()
	if len(recent) != 1 || recent[0].DeviceID != "device-b" || recent[0].Kind != app.FeedbackSubmissionUncertain {
		t.Fatalf("clearing the journal hid a pending receipt: %#v", recent)
	}
	if len(history.For("device-a", failure.WorkspaceID, failure.SessionID)) != 0 {
		t.Fatal("cleared feedback remained visible in its session")
	}
	if got, ok := history.SubmissionFailure("device-a", failure.Details); !ok || got.SessionID != failure.SessionID {
		t.Fatal("clearing the journal discarded the active failure's scope")
	}
	history.Collect("device-b", []app.Feedback{{ID: pending.ID, WorkspaceID: pending.WorkspaceID, SessionID: pending.SessionID, Kind: app.FeedbackSuccess}}, nil)
	if got := history.Recent(); len(got) != 1 || got[0].DeviceID != "device-b" || got[0].Kind != app.FeedbackSuccess {
		t.Fatalf("a later settled result was hidden: %#v", got)
	}
}
