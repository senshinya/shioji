package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
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
	if err := m.SetDeviceArchiveRetention("B", 7); err != nil {
		t.Fatal(err)
	}
	if jobs := m.ArchivePolicyJobs(nil); len(jobs) != 0 {
		t.Fatal("offline setting dispatched to a different device", jobs)
	}
	file := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(file); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(file); err != nil {
		t.Fatal(err)
	}
	if len(restored.ArchivePolicyChanges) != 1 || restored.ArchivePolicyChanges["B"].Days != 7 {
		t.Fatal("offline setting not durable", restored.ArchivePolicyChanges)
	}
	state := restored.DeviceStates["air"]
	state.Connected = true
	state.WorkerMinor = 40
	state.ConnectionEpoch = "next"
	state.Snapshot.ArchivePolicy = &domain.ArchivePolicy{DeviceID: "B", Days: 14, Revision: 2}
	if jobs := restored.ArchivePolicyJobs(nil); len(jobs) != 0 {
		t.Fatal("offline setting overwrote another client's policy", jobs)
	}
	if details := restored.ArchivePolicyChanges["B"].Error; !strings.Contains(details, "已被其他客户端更改") {
		t.Fatal("policy conflict hidden", details)
	}
	if err := restored.SetDeviceArchiveRetention("B", 0); err != nil {
		t.Fatal(err)
	}
	jobs := restored.ArchivePolicyJobs(nil)
	if len(jobs) != 1 || jobs[0].ConnectionID != "air" || jobs[0].Input.(protocol.ArchivePolicyInput).Days != 0 {
		t.Fatal("explicit choice did not replace old intent", jobs)
	}
	jobs[0].Apply(restored, nil, errors.New("write failed"))
	if details := restored.ArchivePolicyChanges["B"].Error; details != "write failed" {
		t.Fatal("worker persistence failure hidden", details)
	}
}

func TestArchivePolicyInFlightChangePreservesSelectionMatchingOldPolicy(t *testing.T) {
	m := archiveModel()
	if err := m.SetDeviceArchiveRetention("A", 7); err != nil {
		t.Fatal(err)
	}
	jobs := m.ArchivePolicyJobs(nil)
	if len(jobs) != 1 || jobs[0].ConnectionID != "local" {
		t.Fatal("job targeted the wrong device", jobs)
	}
	if err := m.SetDeviceArchiveRetention("A", 30); err != nil {
		t.Fatal(err)
	}
	if next := m.ArchivePolicyJobs(func(connection, epoch string) bool { return connection == "local" && epoch == "A" }); len(next) != 0 {
		t.Fatal("new choice dispatched before the earlier one settled", next)
	}
	body, _ := json.Marshal(domain.ArchivePolicy{DeviceID: "A", Days: 7, Revision: 2})
	m.ApplyDeviceUpdate("local", func(p *Model) { jobs[0].Apply(p, body, nil) })
	jobs = m.ArchivePolicyJobs(nil)
	if len(jobs) != 1 || jobs[0].ConnectionID != "local" || jobs[0].Input != (protocol.ArchivePolicyInput{Days: 30, Revision: 2}) {
		t.Fatal("late receipt erased latest intent", jobs, m.ArchivePolicyChanges)
	}
}

func TestArchivePolicyIgnoresLegacyGlobalPreference(t *testing.T) {
	file := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(file, []byte(`{"preferences":{"archiveRetentionDays":7}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.Load(file); err != nil {
		t.Fatal(err)
	}
	m.DeviceStates = archiveModel().DeviceStates
	if jobs := m.ArchivePolicyJobs(nil); len(jobs) != 0 || len(m.ArchivePolicyChanges) != 0 {
		t.Fatal("legacy global preference changed a device policy", jobs, m.ArchivePolicyChanges)
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

func TestWorkspaceDeletionClearsItsPreparingHomeInput(t *testing.T) {
	m := archiveModel()
	m.Pending["preparing"] = domain.Submission{OperationID: "preparing", WorkspaceID: "wA", Text: "home input"}
	m.PendingDevices["preparing"] = "local"
	m.Pending["other"] = domain.Submission{OperationID: "other", WorkspaceID: "wB", Text: "other input"}
	m.PendingDevices["other"] = "air"
	m.Drafts["home"] = "keep home draft"
	m.ApplyOperation(domain.Operation{ID: "preparing", State: domain.OperationPreparing})
	m.ApplyArchiveDeletion(domain.ArchiveDeletion{DeviceID: "A", WorkspaceID: "wA"})
	if _, pending := m.Pending["preparing"]; pending || m.Pending["other"].WorkspaceID != "wB" || m.Drafts["home"] != "keep home draft" {
		t.Fatal("workspace deletion did not release only its own preparation", m.Pending, m.Drafts["home"])
	}
}

func TestArchivePolicyOwnReceiptAdvancesPendingNewerLocalChoice(t *testing.T) {
	m := archiveModel()
	for _, id := range []string{"A", "B"} {
		if err := m.SetDeviceArchiveRetention(id, 7); err != nil {
			t.Fatal(err)
		}
	}
	first := m.ArchivePolicyJobs(nil)
	for _, id := range []string{"A", "B"} {
		if err := m.SetDeviceArchiveRetention(id, 14); err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range first {
		id := map[string]string{"local": "A", "air": "B"}[job.ConnectionID]
		body, _ := json.Marshal(domain.ArchivePolicy{DeviceID: id, Days: 7, Revision: 2})
		m.ApplyDeviceUpdate(job.ConnectionID, func(p *Model) { job.Apply(p, body, nil) })
	}
	jobs := m.ArchivePolicyJobs(nil)
	if len(jobs) != 2 {
		t.Fatal("latest local choice lost", len(jobs), m.ArchivePolicyChanges)
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
