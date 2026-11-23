package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestLivePermissionSelectionWaitsForTheOwningWorkerBeforeSending(t *testing.T) {
	m := harnessModel()
	m.SessionID, m.WorkerMinor = "s", protocol.Minor
	m.Readiness.Harnesses[0].Permissions = []string{"limited", "yolo"}
	m.Readiness.Harnesses[0].PermissionChoices = []domain.OptionChoice{{ID: "limited"}, {ID: "yolo", Live: true}}
	s := domain.Session{ID: "s", WorkspaceID: "w", HarnessID: "alpha", ThreadID: "native", Status: "completed", Revision: 1,
		Config: domain.RunConfig{Model: "a", Permission: "limited"}}
	m.Snapshot.DeviceID = "paired-mac"
	m.Snapshot.Workspaces = []domain.Workspace{{ID: "w", DeviceID: "paired-mac"}}
	s.Context.DeviceID = "paired-mac"
	m.Snapshot.Sessions = []domain.Session{s}
	m.Drafts[m.DraftKey()] = "stay here"
	var job Job
	m.SelectPermission(m.DraftKey(), m.SessionReadiness(s), m.Config(), "yolo", func(j Job) bool { job = j; return true })
	if job.Method != "session.permission" || job.Input != (protocol.SessionPermissionInput{SessionID: "s", ThreadID: "native", Previous: "limited", Permission: "yolo"}) {
		t.Fatalf("permission did not target the owning session: %+v", job)
	}
	if _, err := m.BeginSubmission(); err == nil || m.Config().Permission != "limited" {
		t.Fatal("client used the new selection before worker confirmation")
	}
	s.Revision++
	s.Config.Permission = "yolo"
	body, _ := json.Marshal(s)
	job.Apply(m, body, nil)
	if err := m.ApplyEvent(domain.Event{Sequence: 1, SessionID: s.ID, Session: &s}); err != nil {
		t.Fatal(err)
	}
	in, err := m.BeginSubmission()
	if err != nil || in.Config.Permission != "yolo" || in.Text != "stay here" || len(m.Notifications) != 0 || len(m.Feedbacks) != 0 {
		t.Fatalf("confirmed selection was not silent or did not preserve the draft: %+v error=%v", in, err)
	}
}

func TestSessionConfigIgnoresLegacyClientCacheAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(path, []byte(`{
		"configs":{"session:s":{"model":"previous","permission":"default"}},
		"drafts":{"session:s":"keep this draft"}
	}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.Load(path); err != nil {
		t.Fatal(err)
	}
	m.SessionID = "s"
	m.ApplySnapshot(domain.Snapshot{
		Workspaces: []domain.Workspace{{ID: "w"}},
		Sessions:   []domain.Session{{ID: "s", WorkspaceID: "w", Revision: 2, Config: domain.RunConfig{Model: "current", Permission: "default"}}},
	})
	if got := m.Config().Model; got != "current" {
		t.Errorf("reopened composer shows %q, want worker model current", got)
	}
	if got := m.Drafts[m.DraftKey()]; got != "keep this draft" {
		t.Errorf("migration changed the draft text to %q", got)
	}
}

func TestSharedSessionConfigKeepsOnlyUnsubmittedChoices(t *testing.T) {
	m := harnessModel()
	m.SessionID = "s"
	m.Readiness.Harnesses[0].Models = append(m.Readiness.Harnesses[0].Models,
		domain.Model{ID: "a-plus", DefaultEffort: "medium", Efforts: []string{"medium", "high"}})
	m.Readiness.Harnesses[0].Permissions = []string{"limited", "read-only"}
	session := domain.Session{ID: "s", WorkspaceID: "w", HarnessID: "alpha", Status: "completed", Revision: 1,
		Config: domain.RunConfig{Model: "a", Effort: "medium", Permission: "limited"}}
	m.Snapshot.Sessions = []domain.Session{session}
	m.Drafts[m.DraftKey()] = "keep this draft"
	local := m.Config()
	local.Permission = "read-only"
	m.SetConfig(m.DraftKey(), local)

	session.Revision++
	session.Config.Model, session.Config.Effort = "a-plus", "high"
	if err := m.ApplyEvent(domain.Event{Sequence: 1, SessionID: session.ID, Session: &session}); err != nil {
		t.Fatal(err)
	}
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	want := domain.RunConfig{Model: "a-plus", Effort: "high", Permission: "read-only"}
	if in.Config != want {
		t.Errorf("submission config = %+v, want %+v", in.Config, want)
	}
	// After the worker records this choice, future updates may change it.
	session.Revision++
	session.Config.Permission = "read-only"
	if err := m.ApplyEvent(domain.Event{Sequence: 2, SessionID: session.ID, Session: &session}); err != nil {
		t.Fatal(err)
	}
	session.Revision++
	session.Config.Permission = "limited"
	m.ApplySnapshot(domain.Snapshot{Sequence: 3, Workspaces: m.Snapshot.Workspaces, Sessions: []domain.Session{session}})
	// A late acknowledgement carries the old input, without new session metadata.
	m.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: session.ID, Kind: "submit", State: domain.OperationConfirmed})
	if got := m.Config().Permission; got != "limited" {
		t.Errorf("late receipt replaced the worker permission with %q", got)
	}
}

func TestSessionConfigSettlesSubmittedChoicesOnReconnect(t *testing.T) {
	m := harnessModel()
	m.SessionID = "s"
	m.Readiness.Harnesses[0].Models = append(m.Readiness.Harnesses[0].Models, domain.Model{ID: "selected"})
	session := domain.Session{ID: "s", WorkspaceID: "w", HarnessID: "alpha", Status: "completed", Revision: 1,
		Config: domain.RunConfig{Model: "a", Effort: "medium", Permission: "limited"}}
	m.Snapshot.Sessions = []domain.Session{session}
	selection := m.Config()
	selection.Model = "selected"
	m.SetConfig(m.DraftKey(), selection)
	m.Drafts[m.DraftKey()] = "use the selected model"
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	reopened := harnessModel()
	if err := reopened.Load(path); err != nil {
		t.Fatal(err)
	}
	reopened.SessionID = session.ID
	reopened.ApplySnapshot(m.Snapshot)
	if got := reopened.Config().Model; got != "selected" {
		t.Fatalf("restart lost the unconfirmed model choice: %q", got)
	}

	// Reconnect skips the intermediate revision that ran "selected".
	session.Revision = 3
	session.Config.Model = "latest"
	reopened.ApplySnapshot(domain.Snapshot{
		Sequence:   3,
		Workspaces: m.Snapshot.Workspaces,
		Sessions:   []domain.Session{session},
		Operations: []domain.Operation{{
			ID: in.OperationID, SessionID: session.ID, Kind: "submit", State: domain.OperationConfirmed,
		}},
	})
	if got := reopened.Config().Model; got != "latest" {
		t.Errorf("confirmed submission masked the current worker model: %q", got)
	}
	if !reopened.ModelUnavailable() {
		t.Error("the unavailable worker model was silently replaced")
	}
}
