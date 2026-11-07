package store_test

import (
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/testutil"
	"testing"
)

func TestStateTraceDurableDelivery(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	capture := func(step string) {
		d, err := s.Session(op.SessionID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := s.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		testutil.StateTrace(t, step, struct {
			Detail   domain.SessionDetail
			Snapshot domain.Snapshot
		}{d, snapshot})
	}
	capture("accepted")
	if err = s.SetOperation("first", "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
	in := domain.Submission{OperationID: "queued", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "queued"}
	if _, err = s.Enqueue(in); err != nil {
		t.Fatal(err)
	}
	capture("queued")
	q, _ := s.Queue(op.SessionID)
	in = q.Items[0].Input
	if _, err = s.AcceptQueued(in, q.Revision); err != nil {
		t.Fatal(err)
	}
	capture("dispatch-reserved")
	if err = s.SetOperation(in.OperationID, "indeterminate", "lost reply"); err != nil {
		t.Fatal(err)
	}
	capture("uncertain")
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	capture("recovered")
}

func TestStateTraceSubmissionGuards(t *testing.T) {
	for _, status := range []string{"idle", "starting", "running", "completed", "interrupted", "failed", "unknown", "abandoned", "future-state"} {
		for _, request := range []string{"", "pending", "submitting", "resolved"} {
			t.Run(status+"/request="+request, func(t *testing.T) {
				s := open(t)
				w := workspace(t, s)
				first, err := s.Accept(domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "initial"})
				if err != nil {
					t.Fatal(err)
				}
				if err = s.SetOperation(first.ID, "confirmed", ""); err != nil {
					t.Fatal(err)
				}
				d, err := s.Session(first.SessionID, 0, 100)
				if err != nil {
					t.Fatal(err)
				}
				// JSON is the unchanged state boundary; the probe compiles on both sides.
				b, _ := json.Marshal(map[string]any{"status": status})
				var patch domain.Session
				json.Unmarshal(b, &patch)
				d.Session.Status = patch.Status
				if _, err = s.Apply(first.SessionID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
					t.Fatal(err)
				}
				if request != "" {
					b, _ = json.Marshal(map[string]any{"id": "r", "sessionId": first.SessionID, "kind": "questions", "state": request})
					var r domain.PendingRequest
					json.Unmarshal(b, &r)
					if _, err = s.Apply(first.SessionID, domain.Event{Type: "request.open", Request: &r}); err != nil {
						t.Fatal(err)
					}
				}
				op, err := s.Accept(domain.Submission{OperationID: "next", WorkspaceID: w.ID, SessionID: first.SessionID, Text: "next"})
				message := ""
				if err != nil {
					message = err.Error()
				}
				detail, e := s.Session(first.SessionID, 0, 100)
				if e != nil {
					t.Fatal(e)
				}
				testutil.StateTrace(t, "accept", struct {
					Operation domain.Operation
					Error     string
					Detail    domain.SessionDetail
				}{op, message, detail})
			})
		}
	}
}
