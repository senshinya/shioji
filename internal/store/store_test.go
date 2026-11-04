package store_test

import (
	"fmt"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"testing"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "worker.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func workspace(t *testing.T, s *store.Store) domain.Workspace {
	t.Helper()
	w, err := s.CreateWorkspace("Example", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func TestIdentitySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := s.DeviceID()
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if id == "" || s.DeviceID() != id {
		t.Fatalf("device identity changed: %q -> %q", id, s.DeviceID())
	}
}
func TestSubmissionDeduplicatesWithoutCreatingAnotherSession(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	in := domain.Submission{OperationID: "op-1", WorkspaceID: w.ID, Text: "hello", Config: domain.RunConfig{Model: "test"}}
	first, err := s.Accept(in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Accept(in)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID != again.SessionID {
		t.Fatal("duplicate session")
	}
	in.Text = "different"
	if _, err = s.Accept(in); err == nil {
		t.Fatal("same operation accepted different payload")
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(snap.Sessions))
	}
}
func TestEmptySubmissionDoesNotCreateSessionOrJournalEntry(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	before, _ := s.Snapshot()
	_, err := s.Accept(domain.Submission{OperationID: "empty", WorkspaceID: w.ID, Text: " \n"})
	if err == nil {
		t.Fatal("empty accepted")
	}
	after, _ := s.Snapshot()
	if len(after.Sessions) != 0 || after.Sequence != before.Sequence {
		t.Fatal("empty submission mutated state")
	}
}
func TestAcceptedContextDoesNotFollowWorkspaceEdits(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "one", WorkspaceID: w.ID, Text: "run"})
	if err != nil {
		t.Fatal(err)
	}
	original := w.Folders[0]
	w.Folders = []string{t.TempDir()}
	if _, err = s.UpdateWorkspace(w); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Session.Context.CWD != original {
		t.Fatal("accepted context followed workspace edits")
	}
}
func TestFinalContentOverridesDeltasAndLateDeltaDoesNotReopenItem(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "one", WorkspaceID: w.ID, Text: "run"})
	for _, e := range []domain.Event{
		{Type: "item.upsert", Item: &domain.Item{ID: "assistant", Kind: "assistant", Text: "draft"}},
		{Type: "item.delta", ItemID: "assistant", Text: " partial"},
		{Type: "item.upsert", Item: &domain.Item{ID: "assistant", Kind: "assistant", Text: "authoritative", Final: true}},
		{Type: "item.delta", ItemID: "assistant", Text: " stale"},
	} {
		if _, err := s.Apply(op.SessionID, e); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 2 || d.Items[1].Text != "authoritative" || !d.Items[1].Final {
		t.Fatalf("items: %+v", d.Items)
	}
}
func TestSnapshotBarrierAndJournalAreConsistent(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	snap, _ := s.Snapshot()
	op, err := s.Accept(domain.Submission{OperationID: "one", WorkspaceID: w.ID, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(snap.Sequence, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Sequence != snap.Sequence+1 || events[0].Session.ID != op.SessionID {
		t.Fatalf("events: %+v", events)
	}
}
func TestRestartMarksDispatchUnknownAndDoesNotReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.sqlite")
	s, _ := store.Open(path)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "one", WorkspaceID: w.ID, Text: "hello"})
	if err := s.SetOperation(op.ID, "dispatched", ""); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Operation(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "indeterminate" {
		t.Fatalf("state=%s", got.State)
	}
	d, _ := s.Session(op.SessionID, 0, 100)
	if d.Session.Status != "unknown" {
		t.Fatalf("status=%s", d.Session.Status)
	}
}

func TestRecoveryCoversOutstandingOperationsBeyondSnapshotPage(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	first, err := s.Accept(domain.Submission{OperationID: "old-unknown", WorkspaceID: w.ID, Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(first.ID, "dispatched", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		op, err := s.Accept(domain.Submission{OperationID: fmt.Sprint("later-", i), WorkspaceID: w.ID, Text: "later"})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Operation(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "indeterminate" {
		t.Fatalf("old operation escaped recovery: %s", got.State)
	}
}
