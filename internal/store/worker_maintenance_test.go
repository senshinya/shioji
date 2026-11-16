package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/store"
)

func TestMaintenanceKeepsPendingTitleWorkOutOfRetiringEnvironment(t *testing.T) {
	s := open(t)
	id := maintenanceSession(t, s, "selected-title")
	if _, err := s.AcceptWorkerMaintenance(maintenanceOperation(t, s, "title-maintenance", id)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimTitleGeneration(); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("selected environment started new title work", err)
	}
}

func maintenanceSession(t *testing.T, s *store.Store, id string) string {
	t.Helper()
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: id, WorkspaceID: w.ID, Text: "preserve history"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn-" + id, Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	return op.SessionID
}

func maintenanceOperation(t *testing.T, s *store.Store, id string, sessions ...string) domain.WorkerMaintenance {
	t.Helper()
	op := domain.WorkerMaintenance{ID: id, DeviceID: s.DeviceID(), ExpectedBuildID: "old", BuildID: "new", Phase: domain.WorkerMaintenanceStopping, Actions: []domain.WorkerMaintenanceAction{{ID: "action-" + id, Digest: "input"}}}
	for _, sid := range sessions {
		state, err := s.MaintenanceSessionState(sid)
		if err != nil {
			t.Fatal(err)
		}
		op.Targets = append(op.Targets, domain.WorkerMaintenanceResult{Target: domain.WorkerMaintenanceTarget{SessionID: sid, Generation: "pinned-generation", ProcessRef: "pinned-processes", Revision: state.Session.Revision, QueueRevision: state.Queue.Revision}, State: "stopping"})
	}
	return op
}

func TestMaintenanceAcceptIsAtomicAndRevalidatesQueue(t *testing.T) {
	s := open(t)
	first := maintenanceSession(t, s, "first")
	second := maintenanceSession(t, s, "second")
	d, _ := s.Session(first, 0, 1)
	if _, err := s.Enqueue(domain.Submission{OperationID: "queue-first", SessionID: first, WorkspaceID: d.Session.WorkspaceID, Text: "keep queued"}); err != nil {
		t.Fatal(err)
	}
	op := maintenanceOperation(t, s, "cleanup", first, second)
	d2, _ := s.Session(second, 0, 1)
	if _, err := s.Enqueue(domain.Submission{OperationID: "queue-race", SessionID: second, WorkspaceID: d2.Session.WorkspaceID, Text: "new item"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptWorkerMaintenance(op); err == nil {
		t.Fatal("stale preview accepted")
	}
	q, _ := s.Queue(first)
	if q.Paused {
		t.Fatal("rejected selection partially paused queues")
	}
	op = maintenanceOperation(t, s, "cleanup", first, second)
	accepted, err := s.AcceptWorkerMaintenance(op)
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{first, second} {
		q, _ := s.Queue(sid)
		if !q.Paused || len(q.Items) != 1 {
			t.Fatal("selected queue lost or dispatched", q)
		}
	}
	if accepted.Revision != 1 {
		t.Fatal("no durable revision", accepted)
	}
	if again, err := s.AcceptWorkerMaintenance(op); err != nil || again.Revision != accepted.Revision {
		t.Fatal("duplicate acceptance changed operation", again, err)
	}
	op.BuildID = "different"
	if _, err = s.AcceptWorkerMaintenance(op); err == nil {
		t.Fatal("same ID accepted different replacement")
	}
}

func TestMaintenanceSurvivesReopenAndRejectsActiveSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := maintenanceSession(t, s, "idle")
	op := maintenanceOperation(t, s, "persist", id)
	accepted, err := s.AcceptWorkerMaintenance(op)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Phase = domain.WorkerMaintenanceNeedsForce
	accepted, err = s.SaveWorkerMaintenance(accepted, accepted.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveWorkerMaintenance(op, 1); err == nil {
		t.Fatal("stale writer replaced maintenance state")
	}
	s.Close()
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.WorkerMaintenance("persist")
	if err != nil || got.Phase != domain.WorkerMaintenanceNeedsForce || got.Revision != 2 {
		t.Fatal("maintenance receipt lost", got, err)
	}
	d, _ := s.Session(id, 0, 100)
	if d.Session.Status != "completed" || len(d.Items) == 0 {
		t.Fatal("maintenance changed conversation", d)
	}
	got.Phase = domain.WorkerMaintenanceCancelled
	got, err = s.SaveWorkerMaintenance(got, got.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Accept(domain.Submission{OperationID: "new-turn", WorkspaceID: d.Session.WorkspaceID, SessionID: id, Text: "work"}); err != nil {
		t.Fatal(err)
	}
	active := maintenanceOperation(t, s, "race", id)
	if _, err = s.AcceptWorkerMaintenance(active); err == nil {
		t.Fatal("active turn admitted to cleanup")
	}
}

// main's archive-only worker supports schema 12 but has no persisted manual
// shutdown guard. Its published maximum must not admit maintenance records.
func TestMaintenanceDatabaseExcludesPreGuardSchema12Worker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := maintenanceSession(t, s, "protected")
	if _, err = s.AcceptWorkerMaintenance(maintenanceOperation(t, s, "maintenance", id)); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	var version int
	if err = legacy.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	const preGuardWorkerMaximum = 12
	if version <= preGuardWorkerMaximum {
		t.Fatalf("pre-guard schema-12 worker would admit database containing stop authorization (actual schema %d)", version)
	}
}

func TestExplicitMaintenanceAcceptsActiveSessionAndInvalidatesRequests(t *testing.T) {
	s := open(t)
	id := maintenanceSession(t, s, "explicit-active")
	if _, err := s.Apply(id, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "active", Status: "running"}}); err != nil {
		t.Fatal(err)
	}
	request := domain.PendingRequest{ID: "approval", SessionID: id, State: domain.RequestPending, Revision: 1}
	if _, err := s.Apply(id, domain.Event{Type: "request.open", Request: &request}); err != nil {
		t.Fatal(err)
	}
	op := maintenanceOperation(t, s, "explicit-close", id)
	op.Explicit = true
	op.Standalone = true
	op.BuildID = ""
	if _, err := s.AcceptWorkerMaintenance(op); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if d.Session.Status != "unknown" || d.Session.ExecutionGuard != "pinned-generation" || !d.Queue.Paused {
		t.Fatal("active cleanup did not quarantine execution", d)
	}
	for _, r := range d.Requests {
		if r.State.AwaitingResolution() {
			t.Fatal("dead runtime request remained answerable", r)
		}
	}
}
