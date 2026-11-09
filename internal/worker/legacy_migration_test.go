package worker

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/store"
	"shioji/internal/transport"
	"strings"
	"testing"
)

func TestLegacyBackupIncludesWALAndDoesNotReplaceExistingBackup(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.CreateWorkspace("before", []string{dir}); err != nil {
		t.Fatal(err)
	}
	path, ids, err := backupLegacyDatabase(context.Background(), dir, "operation", db.DeviceID())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatal(ids)
	}
	if _, err = db.CreateWorkspace("after", []string{dir}); err != nil {
		t.Fatal(err)
	}
	again, _, err := backupLegacyDatabase(context.Background(), dir, "operation", db.DeviceID())
	if err != nil || again != path {
		t.Fatal(again, err)
	}
	copy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	var n int
	if err = copy.QueryRow("SELECT count(*) FROM workspaces").Scan(&n); err != nil || n != 1 {
		t.Fatal("backup omitted WAL or was overwritten", n, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup permissions", info, err)
	}
	if _, _, err = backupLegacyDatabase(context.Background(), dir, "foreign", "foreign-device"); err == nil {
		t.Fatal("backed up a foreign device")
	}
}
func TestLegacyMigrationStatusDoesNotInventMissingOperation(t *testing.T) {
	if _, err := LegacyMigrationStatus(t.TempDir(), "missing"); !os.IsNotExist(err) {
		t.Fatal("unknown migration was treated as accepted", err)
	}
	if _, err := LegacyMigrationStatus(t.TempDir(), "../escape"); err == nil {
		t.Fatal("migration identifier escaped state directory")
	}
	_ = domain.MigrationPrepared
}

func TestLegacyPreflightRefusesForeignDatabaseBeforeStopping(t *testing.T) {
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = preflightLegacyDatabase(context.Background(), dir, "foreign", store.SchemaVersion); e == nil {
		t.Fatal("foreign database passed pre-stop check")
	}
	if e = preflightLegacyDatabase(context.Background(), dir, db.DeviceID(), store.SchemaVersion-1); e == nil {
		t.Fatal("newer schema passed pre-stop check")
	}
	if e = preflightLegacyDatabase(context.Background(), dir, db.DeviceID(), store.SchemaVersion); e != nil {
		t.Fatal(e)
	}
}

func TestOrdinaryWorkerStartupCannotBypassInterruptedMigration(t *testing.T) {
	dir, e := os.MkdirTemp("/tmp", "shioji-start-fence-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	lock, e := migrationLock(dir)
	if e != nil {
		t.Fatal(e)
	}
	lock.Close()
	r := legacyMigrationRecord{StopIssued: true, Status: domain.WorkerMigrationStatus{ID: "interrupted", DeviceID: "retained", Phase: domain.MigrationFailed}}
	if e = writeMigration(dir, &r); e != nil {
		t.Fatal(e)
	}
	if e = writeMigrationIndex(filepath.Join(dir, "legacy-migrations", "active"), r.Status.ID); e != nil {
		t.Fatal(e)
	}
	s, e := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{memoryHarness{id: "memory"}}})
	if e == nil {
		s.Close()
		t.Fatal("normal startup bypassed pending migration")
	}
	if !strings.Contains(e.Error(), "迁移") {
		t.Fatal("startup failed for unrelated reason", e)
	}
	if _, e = os.Stat(filepath.Join(dir, "state.sqlite")); !os.IsNotExist(e) {
		t.Fatal("ordinary startup touched database", e)
	}
}

func TestMigrationSpawnFailureRetainsOnlyPostStopRecoveryEnvironment(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		dir := t.TempDir()
		lock, e := migrationLock(dir)
		if e != nil {
			t.Fatal(e)
		}
		lock.Close()
		r := legacyMigrationRecord{StopIssued: stopped, Source: transport.WorkerProcessIdentity{Arguments: []string{"shioji-worker"}}, Status: domain.WorkerMigrationStatus{ID: "spawn", DeviceID: "device"}}
		if e = writeMigrationEnvironment(dir, "spawn", []string{"PRIVATE=test"}); e != nil {
			t.Fatal(e)
		}
		if e = spawnMigrationRunner(dir, "/missing/worker", &r, []string{"PRIVATE=test"}); e == nil {
			t.Fatal("missing executable started")
		}
		_, e = readMigrationEnvironment(dir, "spawn")
		if stopped && e != nil {
			t.Fatal("transient resume failure lost original environment", e)
		}
		if !stopped && !os.IsNotExist(e) {
			t.Fatal("unused pre-stop environment retained", e)
		}
	}
}
