package worker

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

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
