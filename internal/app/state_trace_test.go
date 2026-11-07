package app

import (
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/testutil"
	"testing"
)

func jsonValue(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStateTraceSubmission(t *testing.T) {
	for _, outcome := range []string{"accepted", "dispatched", "confirmed", "rejected", "indeterminate", "abandoned"} {
		t.Run(outcome, func(t *testing.T) {
			m := model()
			m.Drafts["home"] = "original"
			in, err := m.BeginSubmission()
			if err != nil {
				t.Fatal(err)
			}
			m.Accepted(in, domain.Operation{ID: in.OperationID, SessionID: "s", State: "accepted"})
			capture := func(step string) {
				b, err := m.EncodeState()
				if err != nil {
					t.Fatal(err)
				}
				testutil.StateTrace(t, step, struct {
					State          any
					Error, Session string
					Pending        int
				}{State: jsonValue(t, b), Error: m.Error, Session: m.SessionID, Pending: len(m.Pending)})
			}
			capture("accepted")
			m.Drafts["home"] = "typed while pending"
			var observed domain.Operation
			body, _ := json.Marshal(map[string]string{"id": in.OperationID, "sessionId": "s", "state": outcome, "error": "delivery detail"})
			if err := json.Unmarshal(body, &observed); err != nil {
				t.Fatal(err)
			}
			m.ApplyOperation(observed)
			capture("outcome")
			m.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
			capture("late-confirmation")
		})
	}
}

func TestStateTraceQueueEditing(t *testing.T) {
	m := model()
	m.SessionID = "s"
	m.Drafts["session:s"] = "displaced"
	m.Details["s"] = domain.SessionDetail{Queue: domain.QueueState{SessionID: "s", Revision: 4, Items: []domain.QueueItem{{ID: "q", Input: domain.Submission{Text: "queued"}}}}}
	if err := m.EditQueued("q"); err != nil {
		t.Fatal(err)
	}
	testutil.StateTrace(t, "editing", struct {
		Draft string
		Edit  QueueEdit
	}{m.Drafts["session:s"], m.QueueEdits["s"]})
	m.Drafts["session:s"] = "revised"
	q := m.Queue()
	q.Revision++
	m.FinishQueueEdit("s", "q", "revised", q)
	testutil.StateTrace(t, "confirmed", struct {
		Draft string
		Queue domain.QueueState
		Edit  QueueEdit
	}{m.Drafts["session:s"], m.Queue(), m.QueueEdits["s"]})
}
