package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func archiveModel() *Model {
	m := NewModel()
	m.LocalDeviceID = "A"
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B", Name: "Mac mini"}}
	for key, id := range map[string]string{"local": "A", "air": "B"} {
		m.ApplyDeviceUpdate(key, func(p *Model) {
			p.Connected = true
			p.WorkerMinor = 40
			p.ConnectionEpoch = id
			p.ApplySnapshot(domain.Snapshot{DeviceID: id, Generation: id, Sequence: 1, ArchivePolicy: &domain.ArchivePolicy{DeviceID: id, Days: 30, Revision: 1}, Workspaces: []domain.Workspace{{ID: "w" + id, DeviceID: id, Name: "Workspace " + id}}, Sessions: []domain.Session{{ID: "s" + id, WorkspaceID: "w" + id, Title: "Title " + id, Archived: true}}})
		})
	}
	m.WorkspaceID = "wA"
	m.SessionID = "sA"
	m.RefreshDevices()
	return m
}

func TestArchivePolicyPreferencePersistsOfflineAndFencesNewerClients(t *testing.T) {
	m := archiveModel()
	m.DeviceStates["air"].Connected = false
	if err := m.SetArchiveRetention(7); err != nil {
		t.Fatal(err)
	}
	jobs := m.ArchivePolicyJobs()
	if len(jobs) != 1 || jobs[0].ConnectionID != "local" {
		t.Fatal("offline setting dispatched or local missing", jobs)
	}
	body, _ := json.Marshal(domain.ArchivePolicy{DeviceID: "A", Days: 7, Revision: 2})
	m.ApplyDeviceUpdate("local", func(p *Model) { jobs[0].Apply(p, body, nil) })
	file := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(file); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(file); err != nil {
		t.Fatal(err)
	}
	if restored.ArchiveDays() != 7 || len(restored.ArchivePolicyChanges) != 1 {
		t.Fatal("offline setting not durable", restored.ArchivePolicyChanges)
	}
	state := restored.DeviceStates["air"]
	state.Connected = true
	state.WorkerMinor = 40
	state.ConnectionEpoch = "next"
	state.Snapshot.ArchivePolicy = &domain.ArchivePolicy{DeviceID: "B", Days: 14, Revision: 2}
	if jobs := restored.ArchivePolicyJobs(); len(jobs) != 0 {
		t.Fatal("offline setting overwrote another client's policy", jobs)
	}
	if restored.ArchivePolicyStatus() == "" {
		t.Fatal("policy conflict hidden")
	}
	if err := restored.SetArchiveRetention(0); err != nil {
		t.Fatal(err)
	}
	if restored.ArchiveDays() != 0 {
		t.Fatal("never normalized into default")
	}
	jobs = restored.ArchivePolicyJobs()
	if len(jobs) != 1 || jobs[0].ConnectionID != "air" {
		t.Fatal("explicit choice did not replace old intent", jobs)
	}
	jobs[0].Apply(restored, nil, errors.New("write failed"))
	if restored.ArchivePolicyStatus() == "" {
		t.Fatal("worker persistence failure hidden")
	}
}

func TestArchivePolicyLateReceiptCannotReplaceNewerSelection(t *testing.T) {
	m := archiveModel()
	m.SetArchiveRetention(7)
	jobs := m.ArchivePolicyJobs()
	m.SetArchiveRetention(14)
	for _, job := range jobs {
		body, _ := json.Marshal(domain.ArchivePolicy{DeviceID: map[string]string{"local": "A", "air": "B"}[job.ConnectionID], Days: 7, Revision: 2})
		job.Apply(m, body, nil)
	}
	if m.ArchiveDays() != 14 || len(m.ArchivePolicyChanges) != 2 {
		t.Fatal("late receipt erased latest intent")
	}
}

func TestArchiveRowsSearchAllWorkspacesWithoutFiftyItemLimit(t *testing.T) {
	m := archiveModel()
	for i := 0; i < 70; i++ {
		m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: fmt.Sprint("extra-", i), WorkspaceID: "wA", Title: "中文与 English", Archived: true})
	}
	rows := m.ArchivedSessions("", "")
	if len(rows) != 72 {
		t.Fatal("archive list truncated", len(rows))
	}
	if len(m.ArchivedSessions("workspace a", "")) != 71 {
		t.Fatal("workspace title search not global")
	}
	m.Snapshot.Workspaces[0].Archived = true
	m.Snapshot.Sessions[0].Archived = false
	if len(m.ArchivedSessions("", m.Snapshot.Workspaces[0].ID)) == 0 || len(m.ArchivedWorkspaces("")) != 1 {
		t.Fatal("archived workspace contents missing")
	}
}

func TestDeletedOperationReceiptSettlesAnOfflineClientWithoutResurrectingHistory(t *testing.T) {
	m := archiveModel()
	m.Pending["old"] = domain.Submission{OperationID: "old", WorkspaceID: "wB", Text: "old input"}
	m.PendingDevices["old"] = "air"
	m.Drafts["home"] = "new unrelated draft"
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Generation: "B", Sequence: 3, Workspaces: []domain.Workspace{{ID: "wB", DeviceID: "B"}}})
		p.ApplyOperation(domain.Operation{ID: "old", SessionID: "sB", State: "rejected", Deleted: true})
	})
	if !m.DeviceStates["air"].Connected || len(m.Pending) != 0 || len(m.Details) != 0 || m.Drafts["home"] != "new unrelated draft" {
		t.Fatal("deleted receipt broke reconnection", m.Pending, m.DeviceStates["air"].Error)
	}
}

func TestArchivePolicyOwnReceiptAdvancesPendingNewerLocalChoice(t *testing.T) {
	m := archiveModel()
	m.SetArchiveRetention(7)
	first := m.ArchivePolicyJobs()
	m.SetArchiveRetention(14)
	for _, job := range first {
		id := map[string]string{"local": "A", "air": "B"}[job.ConnectionID]
		body, _ := json.Marshal(domain.ArchivePolicy{DeviceID: id, Days: 7, Revision: 2})
		m.ApplyDeviceUpdate(job.ConnectionID, func(p *Model) { job.Apply(p, body, nil) })
	}
	jobs := m.ArchivePolicyJobs()
	if len(jobs) != 2 {
		t.Fatal("latest local choice lost", len(jobs), m.ArchivePolicyStatus())
	}
	for _, job := range jobs {
		if in := job.Input.(protocol.ArchivePolicyInput); in.Revision != 2 || in.Days != 14 {
			t.Fatal("newer choice did not follow acknowledged local write", in)
		}
	}
}

func TestArchiveDeletionClearsOnlyOwningDeviceDraftsAndSelections(t *testing.T) {
	m := archiveModel()
	m.Drafts["home"] = "keep home"
	m.Drafts["session:sA"] = "keep local"
	m.Drafts["session:sB"] = "delete remote"
	m.Details["sB"] = domain.SessionDetail{Session: domain.Session{ID: "sB", WorkspaceID: "wB"}, Items: []domain.Item{{ID: "old", Text: "old history"}}}
	m.SecondarySessionID = "sB"
	m.SecondaryWorkspaceID = "wB"
	m.FocusSecondary = true
	m.Navigation.Pinned = []SessionLocation{{DeviceID: "A", WorkspaceID: "wA", SessionID: "sA"}, {DeviceID: "B", WorkspaceID: "wB", SessionID: "sB"}}
	m.ApplyDeviceUpdate("air", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 2, Type: "archive.deleted", Deletion: &domain.ArchiveDeletion{DeviceID: "B", WorkspaceID: "wB", SessionIDs: []string{"sB"}}}); err != nil {
			t.Fatal(err)
		}
	})
	if _, ok := m.Drafts["session:sB"]; ok {
		t.Fatal("deleted remote draft retained")
	}
	if _, ok := m.Details["sB"]; ok {
		t.Fatal("deleted history retained")
	}
	if m.SecondarySessionID != "" || m.FocusSecondary {
		t.Fatal("deleted parallel selection retained")
	}
	if len(m.Snapshot.Sessions) != 1 || m.Snapshot.Sessions[0].ID != "sA" || len(m.Navigation.Pinned) != 1 {
		t.Fatal("deletion projection wrong", m.Snapshot, m.Navigation)
	}
	if m.Drafts["home"] != "keep home" || m.Drafts["session:sA"] != "keep local" || m.SessionID != "sA" {
		t.Fatal("remote deletion changed other work")
	}
}

func TestArchiveDeletionRejectsOtherDevicesIdentity(t *testing.T) {
	m := archiveModel()
	m.ApplyDeviceUpdate("air", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 2, Type: "archive.deleted", Deletion: &domain.ArchiveDeletion{DeviceID: "A", SessionIDs: []string{"sA"}}}); err == nil {
			t.Fatal("foreign deletion event accepted")
		}
	})
	if len(m.Snapshot.Sessions) != 2 {
		t.Fatal("foreign event deleted local session")
	}
}

func TestReconnectingSnapshotRemovesDeletedDraftAndHistory(t *testing.T) {
	m := archiveModel()
	m.Drafts["session:sB"] = "deleted while offline"
	m.Details["sB"] = domain.SessionDetail{Session: domain.Session{ID: "sB", WorkspaceID: "wB"}}
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Generation: "B", Sequence: 5, Workspaces: []domain.Workspace{{ID: "wB", DeviceID: "B"}}})
	})
	if _, ok := m.Drafts["session:sB"]; ok {
		t.Fatal("reconnect kept permanently deleted draft")
	}
	if _, ok := m.Details["sB"]; ok {
		t.Fatal("reconnect kept permanently deleted history")
	}
}
