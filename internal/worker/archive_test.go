package worker

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestArchiveDeletionRefusesUnverifiedLiveRuntime(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		s, w := memoryWorker(t, memoryHarness{id: "idle"})
		d := submitIdleFixture(t, s, w, "unverified", "")
		r := s.currentRuntime(d.Session.ID)
		a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
		if err != nil {
			t.Fatal(err)
		}
		method, in := "session.delete", protocol.ArchiveDeleteInput{SessionID: a.ID, Revision: a.Revision}
		if workspace {
			aw, e := s.store.SetWorkspaceArchived(w.ID, w.Revision, true)
			if e != nil {
				t.Fatal(e)
			}
			method, in = "workspace.delete", protocol.ArchiveDeleteInput{WorkspaceID: w.ID, Revision: aw.Revision}
		}
		s.dispatchMu.Lock()
		_, err = s.deleteArchive(method, in)
		s.dispatchMu.Unlock()
		if err == nil || r.Stopped() {
			t.Fatal("deleted unverified execution", err)
		}
		if err = s.cleanupArchives(a.ArchivedAt.Add(31 * 24 * time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.Session(a.ID, 0, 1); err != nil || r.Stopped() {
			t.Fatal("expiry retired unverified execution", err)
		}
	}
}

func TestArchiveDeletionCleansDiffCommentDependencies(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "exclusive", true: "shared"}[shared], func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			snapshot, err := s.putDiffResource(w.ID, domain.DiffSnapshotResource, "diff-snapshot.json", domain.FileVersions{Path: "private.go", New: "private source"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.attachments.RetainDiffSnapshot(w.ID, snapshot); err != nil {
				t.Fatal(err)
			}
			add := func(id string) domain.Session {
				d := submitIdleFixture(t, s, w, id, "")
				comment, e := s.putDiffResource(w.ID, domain.DiffCommentResource, "private.go:1", domain.DiffCommentRecord{Snapshot: snapshot, Content: domain.CodeCommentContent{Comment: id}})
				if e != nil {
					t.Fatal(e)
				}
				_, e = s.store.Apply(d.Session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "comment", Kind: "user", Attachments: []domain.Attachment{comment}}})
				if e != nil {
					t.Fatal(e)
				}
				d, e = s.store.Session(d.Session.ID, 0, 1)
				if e != nil {
					t.Fatal(e)
				}
				a, e := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
				if e != nil {
					t.Fatal(e)
				}
				return a
			}
			first := add("first")
			var second domain.Session
			if shared {
				second = add("second")
			}
			remove := func(a domain.Session) {
				s.dispatchMu.Lock()
				_, e := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: a.ID, Revision: a.Revision})
				s.dispatchMu.Unlock()
				if e != nil {
					t.Fatal(e)
				}
				for i := 0; i < 3; i++ {
					if e = s.cleanArchiveGarbage(); e != nil {
						t.Fatal(e)
					}
				}
			}
			path := filepath.Join(s.attachments.Directory, w.ID, snapshot.ID)
			remove(first)
			if shared {
				if _, err = os.Stat(path); err != nil {
					t.Fatal("shared snapshot removed", err)
				}
				remove(second)
			}
			for _, suffix := range []string{"", ".json", ".retained"} {
				if _, err = os.Stat(path + suffix); !os.IsNotExist(err) {
					t.Fatal("deleted comment retained private snapshot", suffix, err)
				}
			}
		})
	}
}

func TestWorkerArchiveCleanupUsesOwningClockAndSkipsLiveWork(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "archive-first", "")
	runtime := s.currentRuntime(d.Session.ID)
	archived, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := s.store.ArchivePolicy()
	if _, err = s.store.ConfigureArchive(7, policy.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupArchives(archived.ArchivedAt.Add(7*24*time.Hour - time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Session(archived.ID, 0, 1); err != nil {
		t.Fatal("deleted early", err)
	}
	_, err = s.store.Apply(archived.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "late", Title: "active child", State: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupArchives(archived.ArchivedAt.Add(8 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if runtime.Stopped() {
		t.Fatal("archive cleanup stopped active work")
	}
	_, err = s.store.Apply(archived.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "late", Title: "active child", State: "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupArchives(archived.ArchivedAt.Add(8 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Session(archived.ID, 0, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired archive retained", err)
	}
	if !runtime.Stopped() || s.currentRuntime(archived.ID) != nil {
		t.Fatal("deleted archive kept runtime ownership")
	}
	if _, err = os.Stat(filepath.Join(s.config.Directory, "executions", archived.ID)); !os.IsNotExist(err) {
		t.Fatal("deleted scope retained", err)
	}
	if _, err = s.store.Apply(archived.ID, domain.Event{Type: "runtime.lost"}); err == nil {
		t.Fatal("late callback restored deleted state")
	}
}
