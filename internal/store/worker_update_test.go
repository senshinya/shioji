package store_test

import (
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"testing"
)

func TestWorkerUpdateRejectsActiveWorkAndBacksUpPausedQueue(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "active", WorkspaceID: w.ID, Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.sqlite")
	result, err := s.PrepareWorkerUpdate(path)
	if err != nil || result.Ready || result.Reason == "" {
		t.Fatal("update accepted active work", result, err)
	}
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}})
	s.Enqueue(domain.Submission{OperationID: "queued", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "keep queued"})
	result, err = s.PrepareWorkerUpdate(path)
	if err != nil || !result.Ready {
		t.Fatal("idle update failed", result, err)
	}
	restored, err := store.Open(path)
	if err != nil {
		t.Fatal("backup cannot reopen", err)
	}
	defer restored.Close()
	d, err := restored.Session(op.SessionID, 0, 100)
	if err != nil || restored.DeviceID() != s.DeviceID() || !d.Queue.Paused || len(d.Queue.Items) != 1 || d.Queue.Items[0].Input.Text != "keep queued" {
		t.Fatal("consistent backup lost state", d, err)
	}
}

func TestWorkerUpdateWaitsForRequestsUnknownDeliveryAndTitleJobs(t *testing.T) {
	for _, reason := range []string{"approval", "question", "unknown delivery", "title job"} {
		t.Run(reason, func(t *testing.T) {
			s := open(t)
			w := workspace(t, s)
			op, err := s.Accept(domain.Submission{OperationID: "input", WorkspaceID: w.ID, Text: "work"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
				t.Fatal(err)
			}
			if reason == "approval" || reason == "question" {
				kind := "approval"
				if reason == "question" {
					kind = "question"
				}
				if _, err = s.Apply(op.SessionID, domain.Event{Type: "request.open", Request: &domain.PendingRequest{ID: "pending", TurnID: "turn", Kind: kind, State: "pending"}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			if reason == "unknown delivery" {
				if err = s.SetOperation(op.ID, "indeterminate", "lost receipt"); err != nil {
					t.Fatal(err)
				}
			}
			if reason == "title job" {
				if _, err = s.ClaimTitleGeneration(); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.PrepareWorkerUpdate(filepath.Join(t.TempDir(), "backup.sqlite"))
			if err != nil || result.Ready || result.Reason == "" {
				t.Fatal("update failed to wait", result, err)
			}
		})
	}
}
