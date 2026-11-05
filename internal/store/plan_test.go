package store_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"strings"
	"testing"
)

func TestLatestPlanSurvivesRestartAndStaysOutsideTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "plan-session", WorkspaceID: w.ID, Text: "work"})
	other, _ := s.Accept(domain.Submission{OperationID: "other-session", WorkspaceID: w.ID, Text: "other"})
	for _, text := range []string{"first", "latest"} {
		var event domain.Event
		json.Unmarshal([]byte(`{"type":"plan","plan":{"turnId":"turn","explanation":"actual structured event","steps":[{"text":"`+text+`","status":"running"}]}}`), &event)
		if _, err = s.Apply(op.SessionID, event); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	detail, err := s.Session(op.SessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(detail)
	if !strings.Contains(string(data), `"text":"latest"`) || strings.Contains(string(data), `"text":"first"`) {
		t.Fatal("latest plan was not restored independently of history pagination", string(data))
	}
	for _, item := range detail.Items {
		if strings.Contains(item.Text, "latest") {
			t.Fatal("plan leaked into transcript")
		}
	}
	detail, _ = s.Session(other.SessionID, 0, 100)
	data, _ = json.Marshal(detail)
	if strings.Contains(string(data), `"plan"`) {
		t.Fatal("plan leaked into another session", string(data))
	}
}

func TestPlanSchemaMigrationPreservesExistingWorkerState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "old", WorkspaceID: w.ID, Text: "existing session"})
	device := s.DeviceID()
	s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TABLE plans"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.DeviceID() != device {
		t.Fatal("migration changed device identity")
	}
	before, err := s.Session(op.SessionID, 0, 100)
	if err != nil || before.Session.Title != "existing session" {
		t.Fatal("migration lost prior session", before, err)
	}
	plan := &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "migrated", Status: "pending"}}}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "plan", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Session(op.SessionID, 0, 100)
	if after.Plan == nil || after.Plan.Steps[0].Text != "migrated" {
		t.Fatal("migrated plan unavailable", after.Plan)
	}
}
