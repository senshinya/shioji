package store

import (
	"path/filepath"
	"testing"

	"shioji/internal/domain"
)

func TestReceivedHomeInputSurvivesRestartAsDefiniteNonDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.CreateWorkspace("test", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	in := domain.Submission{OperationID: "received-home", WorkspaceID: w.ID, Text: "preserve unsent input"}
	if op, fresh, err := db.ReceiveHomeSubmission(in); err != nil || !fresh || op.State != domain.OperationPreparing {
		t.Fatalf("receive input: %+v %v %v", op, fresh, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Recover(); err != nil {
		t.Fatal(err)
	}
	op, fresh, err := db.ReceiveHomeSubmission(in)
	if err != nil || fresh || op.State != domain.OperationRejected || op.SessionID != "" {
		t.Fatalf("restart replayed or lost the receipt: %+v %v %v", op, fresh, err)
	}
	snapshot, err := db.Snapshot()
	if err != nil || len(snapshot.Sessions) != 0 {
		t.Fatal("recover created a session", err)
	}
	in.Text = "different input"
	if _, _, err = db.ReceiveHomeSubmission(in); err == nil {
		t.Fatal("receipt ID accepted different input after restart")
	}
}
