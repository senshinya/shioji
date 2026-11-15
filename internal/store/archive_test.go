package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
)

func archiveFixture(t *testing.T) (*Store, domain.Workspace, domain.Session) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	w, err := s.CreateWorkspace("Archive fixture", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session := archiveCompleted(t, s, w.ID, "original")
	return s, w, session
}

func TestArchiveMigrationStartsOldArchivesAtUpgrade(t *testing.T) {
	s, w, session := archiveFixture(t)
	if _, err := s.db.Exec("UPDATE sessions SET body=json_set(body,'$.archived',json('true')) WHERE id=?", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE workspaces SET body=json_set(body,'$.archived',json('true')) WHERE id=?", w.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()
	before := time.Now().UTC()
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snap, err := reopened.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{snap.Workspaces[0], snap.Sessions[0]} {
		at := archiveStamp(t, value)
		if at == nil || at.Before(before) || at.After(time.Now()) {
			t.Fatal("legacy archive must start at migration, not immediately expire", at)
		}
	}
}

func TestArchiveRetentionBoundariesAndPersistentPolicy(t *testing.T) {
	s, _, session := archiveFixture(t)
	archived, err := s.SetSessionArchived(session.ID, session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.ArchivePolicy()
	if err != nil || policy.Days != 30 || policy.Revision != 1 || policy.DeviceID != s.DeviceID() {
		t.Fatal(policy, err)
	}
	if _, err = s.ConfigureArchive(1, policy.Revision); err == nil {
		t.Fatal("unsupported retention accepted")
	}
	for _, days := range []int{7, 14, 30} {
		policy, err = s.ConfigureArchive(days, policy.Revision)
		if err != nil {
			t.Fatal(err)
		}
		before := archived.ArchivedAt.Add(time.Duration(days)*24*time.Hour - time.Nanosecond)
		pending, err := s.ArchiveCandidates(before)
		if err != nil || len(pending) != 0 {
			t.Fatal("early deletion", pending, err)
		}
		pending, err = s.ArchiveCandidates(before.Add(time.Nanosecond))
		if err != nil || len(pending) != 1 || pending[0].SessionID != session.ID {
			t.Fatal("expiry boundary lost", pending, err)
		}
	}
	if _, err = s.ConfigureArchive(7, 1); err == nil {
		t.Fatal("stale policy overwrote another client")
	}
	policy, err = s.ConfigureArchive(0, policy.Revision)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.ArchiveCandidates(time.Now().AddDate(10, 0, 0))
	if err != nil || len(pending) != 0 {
		t.Fatal("never expired", pending, err)
	}
	s.Close()
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.ArchivePolicy()
	if err != nil || persisted != policy {
		t.Fatal("policy not durable", persisted, err)
	}
}

func TestPermanentSessionDeletionRedactsHistoryAndFencesInputReplay(t *testing.T) {
	s, w, session := archiveFixture(t)
	if _, err := s.DeleteArchivedSession(session.ID, session.Revision); err == nil {
		t.Fatal("deleted unarchived session")
	}
	archived, err := s.SetSessionArchived(session.ID, session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteArchivedSession(session.ID, session.Revision); err == nil {
		t.Fatal("stale delete accepted")
	}
	result, err := s.DeleteArchivedSession(session.ID, archived.Revision)
	if err != nil || result.WorkspaceID != "" || len(result.SessionIDs) != 1 || result.SessionIDs[0] != session.ID || result.DeviceID != s.DeviceID() {
		t.Fatal(result, err)
	}
	if _, err = s.Session(session.ID, 0, 100); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("history still readable", err)
	}
	snap, err := s.Snapshot()
	if err != nil || len(snap.Sessions) != 0 || len(snap.Operations) != 0 {
		t.Fatal("deleted catalog retained", snap, err)
	}
	events, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		if e.Sequence != int64(i+1) {
			t.Fatal("redaction broke contiguous cursors", events)
		}
		encoded, _ := json.Marshal(e)
		if string(encoded) != "" && containsArchiveText(encoded) {
			t.Fatal("deleted text retained in journal", string(encoded))
		}
	}
	if len(events) == 0 || events[len(events)-1].Deletion == nil {
		t.Fatal("missing deletion event", events)
	}
	if _, err = s.Accept(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "private archive text", Config: domain.RunConfig{Model: "test"}}); err == nil {
		t.Fatal("replayed deleted input created new session")
	}
	receipt, err := s.Operation("original")
	if err != nil || receipt.State != "rejected" || receipt.SessionID != session.ID {
		t.Fatal("reconnecting input owner cannot settle its deleted operation", receipt, err)
	}
	if _, err = s.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "late", Kind: "assistant", Text: "late"}}); err == nil {
		t.Fatal("late event resurrected deleted session")
	}
}

func containsArchiveText(b []byte) bool { return strings.Contains(string(b), "private archive text") }

func TestWorkspaceDeletionIsAtomicAndRetainsOtherWorkspaces(t *testing.T) {
	s, w, first := archiveFixture(t)
	second := archiveCompleted(t, s, w.ID, "second")
	other, err := s.CreateWorkspace("Keep", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	kept := archiveCompleted(t, s, other.ID, "kept")
	archived, err := s.SetWorkspaceArchived(w.ID, w.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE sessions SET body=json_set(body,'$.status','running') WHERE id=?", second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteArchivedWorkspace(w.ID, archived.Revision); err == nil {
		t.Fatal("workspace with running session deleted")
	}
	if _, err = s.Session(first.ID, 0, 100); err != nil {
		t.Fatal("failed deletion partially removed workspace", err)
	}
	s.db.Exec("UPDATE sessions SET body=json_set(body,'$.status','completed') WHERE id=?", second.ID)
	result, err := s.DeleteArchivedWorkspace(w.ID, archived.Revision)
	if err != nil || len(result.SessionIDs) != 2 || result.WorkspaceID != w.ID {
		t.Fatal(result, err)
	}
	snap, err := s.Snapshot()
	if err != nil || len(snap.Workspaces) != 1 || snap.Workspaces[0].ID != other.ID || len(snap.Sessions) != 1 || snap.Sessions[0].ID != kept.ID {
		t.Fatal("workspace delete changed other data", snap, err)
	}
}

func archiveCompleted(t *testing.T, s *Store, workspace, operation string) domain.Session {
	t.Helper()
	op, err := s.Accept(domain.Submission{OperationID: operation, WorkspaceID: workspace, Text: "private archive text", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return d.Session
}

func archiveStamp(t *testing.T, value any) *time.Time {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		ArchivedAt *time.Time `json:"archivedAt"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v.ArchivedAt
}

func TestArchiveTimestampIsOwnedByArchiveTransition(t *testing.T) {
	s, w, session := archiveFixture(t)
	before := time.Now().UTC()
	archived, err := s.SetSessionArchived(session.ID, session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	stamp := archiveStamp(t, archived)
	if stamp == nil || stamp.Before(before) || stamp.After(time.Now()) {
		t.Fatal("archive lacks worker acceptance timestamp", stamp)
	}
	same, err := s.SetSessionArchived(archived.ID, archived.Revision, true)
	if err != nil || !archiveStamp(t, same).Equal(*stamp) {
		t.Fatal("repeated archive extended retention", same, err)
	}
	restored, err := s.SetSessionArchived(same.ID, same.Revision, false)
	if err != nil || archiveStamp(t, restored) != nil {
		t.Fatal("restore retained expiry", restored, err)
	}
	again, err := s.SetSessionArchived(restored.ID, restored.Revision, true)
	if err != nil || !archiveStamp(t, again).After(*stamp) {
		t.Fatal("rearchive did not restart retention", again, err)
	}
	aw, err := s.SetWorkspaceArchived(w.ID, w.Revision, true)
	if err != nil || archiveStamp(t, aw) == nil {
		t.Fatal("workspace missing archive time", aw, err)
	}
	rw, err := s.SetWorkspaceArchived(aw.ID, aw.Revision, false)
	if err != nil || archiveStamp(t, rw) != nil {
		t.Fatal("workspace restore retained expiry", rw, err)
	}
	d, _ := s.Session(session.ID, 0, 100)
	if !d.Session.Archived {
		t.Fatal("workspace restore restored individually archived session")
	}
}

func TestArchiveTimestampSurvivesLateRuntimeSnapshot(t *testing.T) {
	s, _, session := archiveFixture(t)
	archived, err := s.SetSessionArchived(session.ID, session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(session.ID, domain.Event{Type: "session", Session: &session}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(session.ID, 0, 100)
	if err != nil || d.Session.ArchivedAt == nil || !d.Session.ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Fatal("runtime snapshot erased worker-owned retention time", d.Session, err)
	}
}

func TestArchiveDeletionKeepsSharedAttachmentUntilFinalReferenceIsRemoved(t *testing.T) {
	s, w, first := archiveFixture(t)
	second := archiveCompleted(t, s, w.ID, "shared")
	a := domain.Attachment{ID: domain.NewID(), Name: "shared.txt", MIME: "text/plain", Size: 12, SHA256: strings.Repeat("a", 64)}
	for _, session := range []domain.Session{first, second} {
		if _, err := s.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "resource", Kind: "user", Text: "shared content", Attachments: []domain.Attachment{a}, Final: true}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []domain.Session{first, second} {
		d, _ := s.Session(session.ID, 0, 10)
		archived, err := s.SetSessionArchived(session.ID, d.Session.Revision, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.DeleteArchivedSession(session.ID, archived.Revision); err != nil {
			t.Fatal(err)
		}
		shared, err := s.ArchiveResourceReferenced(w.ID, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if shared != (session.ID == first.ID) {
			t.Fatal("shared resource reference was lost", shared, session.ID)
		}
	}
	garbage, err := s.ArchiveGarbage()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range garbage {
		if g.AttachmentID == a.ID && g.WorkspaceID == w.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("deleted resource was not queued for crash-safe cleanup", garbage)
	}
}
