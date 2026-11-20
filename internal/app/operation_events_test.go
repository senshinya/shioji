package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestHomeReceiptBeforeSessionEventKeepsDraftWithoutFalseUncertainty(t *testing.T) {
	m := NewModel()
	m.WorkspaceID = "w"
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.Connected = true
		p.ApplySnapshot(domain.Snapshot{DeviceID: "d", Generation: "g", Workspaces: []domain.Workspace{{ID: "w", DeviceID: "d"}}})
	})
	in := domain.Submission{OperationID: "first", WorkspaceID: "w", Text: "first message"}
	m.Pending[in.OperationID] = in
	m.PendingDevices[in.OperationID] = "local"
	m.Drafts["home"] = in.Text
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "new-session", State: domain.OperationAccepted})
	})
	m.SubmissionError(in, errors.New("lost command reply"))
	if !m.Connected || m.UncertainSubmissions[in.OperationID] || m.SessionID != "" || m.Drafts["home"] != in.Text {
		t.Fatal("receipt ahead of history caused a false warning, fabricated a session or cleared the draft")
	}
}

func TestOperationEventsKeepRecentReceiptsCurrentAndBounded(t *testing.T) {
	m := model()
	for i := range 110 {
		op := domain.Operation{ID: fmt.Sprint(i), SessionID: "s", State: domain.OperationAccepted}
		if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, Type: "submission", SessionID: "s", Operation: &op}); err != nil {
			t.Fatal(err)
		}
	}
	op := domain.Operation{ID: "109", SessionID: "s", State: domain.OperationConfirmed}
	if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, Type: "submission", SessionID: "s", Operation: &op}); err != nil {
		t.Fatal(err)
	}
	if len(m.Snapshot.Operations) != 100 {
		t.Fatal("recent receipt projection is missing or unbounded", len(m.Snapshot.Operations))
	}
	if got := m.Snapshot.Operations[0]; got.ID != "109" || got.State != domain.OperationConfirmed {
		t.Fatal("live confirmation did not replace the stale accepted receipt", got)
	}
	count := 0
	for _, receipt := range m.Snapshot.Operations {
		if receipt.ID == "109" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("confirmation inserted a duplicate receipt", count)
	}
}

func TestMissingSubmissionCanOnlyBeSettledByItsOriginalJournal(t *testing.T) {
	m := model()
	m.WorkerMinor = protocol.RecoveryMinor
	m.Drafts["home"] = "preserve the original"
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err = m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := model()
	if err = restored.Load(path); err != nil {
		t.Fatal(err)
	}
	restored.WorkerMinor = protocol.RecoveryMinor
	restored.Snapshot.Generation = "another-journal"
	if got := restored.SubmissionReceiptQuery(in.OperationID); got.SealGeneration != "" {
		t.Fatal("a replacement journal could claim the original never arrived", got)
	}
	restored.Snapshot.Generation = "g"
	if got := restored.SubmissionReceiptQuery(in.OperationID); got.SealGeneration != "g" {
		t.Fatal("the original journal was not preserved with the draft", got)
	}
	restored.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationAccepted})
	if got := restored.SubmissionReceiptQuery(in.OperationID); got.SealGeneration != "" {
		t.Fatal("an observed acceptance could be turned into a missing receipt", got)
	}
}
