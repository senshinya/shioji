package supervisor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/store"
)

func TestSchemaMigrationSwapsFinalDatabaseAndCanRestoreBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	device := db.DeviceID()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`
		DROP TABLE schedule_runs;
		DROP TABLE schedules;
		PRAGMA user_version=15;
		CREATE TABLE final_event (value TEXT NOT NULL);
		INSERT INTO final_event VALUES ('committed during drain');
	`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	op := Operation{DeviceID: device}
	op.Input.ID = "migration-test"
	migrate := func(directory string) error {
		copy, err := store.Open(filepath.Join(directory, "state.sqlite"))
		if err != nil {
			return err
		}
		return copy.Close()
	}
	if err := stageSchemaMigration(ctx, dir, op, 15, 16, migrate); err != nil {
		t.Fatal(err)
	}
	assertMigrationSchema(t, dir, device, 16)
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := raw.QueryRow("SELECT value FROM final_event").Scan(&value); err != nil || value != "committed during drain" {
		t.Fatal("final old-writer data was lost during migration", value, err)
	}
	if _, err := raw.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO final_event VALUES ('paused candidate write')"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := restoreSchemaMigration(ctx, dir, op, 15, 16); err != nil {
		t.Fatal(err)
	}
	assertMigrationSchema(t, dir, device, 15)
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var count int
	if err := raw.QueryRow("SELECT count(*) FROM final_event").Scan(&count); err != nil || count != 1 {
		t.Fatal("candidate writes were mixed into the restored old database", count, err)
	}
	if err := restoreSchemaMigration(ctx, dir, op, 15, 16); err != nil {
		t.Fatal("rollback must be safe to retry", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func assertMigrationSchema(t *testing.T, dir, device string, schema int) {
	t.Helper()
	actual, version, err := store.ReadWorkerIdentity(context.Background(), dir)
	if err != nil || actual != device || version != schema {
		t.Fatalf("unexpected device/schema: %s/%d (%v)", actual, version, err)
	}
}

func TestSchemaMigrationCandidateStartupFailureRestoresOldService(t *testing.T) {
	dir, options := regressionOptions(t)
	ctx := context.Background()
	raw, err := sql.Open("sqlite", filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("DROP TABLE schedule_runs; DROP TABLE schedules; PRAGMA user_version=15"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	verify := options.Verify
	options.Verify = func(ctx context.Context, id string) (string, domain.WorkerBuild, error) {
		_, build, err := verify(ctx, id)
		switch build.BuildID {
		case "old":
			build.Schema = 15
			return filepath.Join(dir, "fixture-old-schema15"), build, err
		case "next":
			build.MigrationFrom = 15
			return filepath.Join(dir, "fixture-next-schema16-unready"), build, err
		}
		return "", build, err
	}
	for _, name := range []string{"fixture-old-schema15", "fixture-next-schema16-unready"} {
		if err := os.Symlink(os.Args[0], filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	regressionHost(t, options, "ready")
	in := domain.WorkerReplacement{ID: "schema-rollback", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle", Explicit: true, SchemaMigration: true}
	if _, err := Call(ctx, dir, Request{Action: "start", DeviceID: "fixture-device", Input: in}); err != nil {
		t.Fatal(err)
	}
	op := awaitRegressionPhase(t, dir, in.ID, "rolled-back")
	assertMigrationSchema(t, dir, "fixture-device", 15)
	if op.SourceSchema != 15 || op.TargetSchema != 16 || op.Error == "" {
		t.Fatal("rollback lost the migration failure or schemas", op)
	}
}

func TestInterruptedSchemaMigrationRestoresBeforeStartingWriter(t *testing.T) {
	dir, options := regressionOptions(t)
	ctx := context.Background()
	path := filepath.Join(dir, "state.sqlite")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec("PRAGMA user_version=15"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	op := Operation{DeviceID: "fixture-device", Phase: "observing", PreviousBuild: "old", SourceSchema: 15, TargetSchema: 16,
		Input: domain.WorkerReplacement{ID: "interrupted", BuildID: "next", ExpectedBuildID: "old", SchemaMigration: true}}
	if err := stageSchemaMigration(ctx, dir, op, 15, 16, func(copy string) error {
		db, err := store.Open(filepath.Join(copy, "state.sqlite"))
		if err != nil {
			return err
		}
		return db.Close()
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate the interruption after selection was published, before the
	// durable applied receipt and before the paused candidate was resumed.
	if err := options.Select("next"); err != nil {
		t.Fatal(err)
	}
	if err := (journal{filepath.Join(dir, "supervisor")}).save(op); err != nil {
		t.Fatal(err)
	}
	regressionHost(t, options, "rolled-back")
	assertMigrationSchema(t, dir, "fixture-device", 15)
	if selected, err := selectedBuild(filepath.Join(dir, "selected.json")); err != nil || selected != "old" {
		t.Fatal("recovery started the wrong build", selected, err)
	}
}

func selectedBuild(path string) (string, error) {
	var selected string
	err := readJSON(path, &selected)
	return selected, err
}

func TestShutdownDuringSchemaMigrationKeepsPrecommitReceipt(t *testing.T) {
	dir, options := regressionOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op := Operation{DeviceID: "fixture-device", Phase: "observing", PreviousBuild: "old", SourceSchema: 15, TargetSchema: 16,
		Input: domain.WorkerReplacement{ID: "shutdown-migration", BuildID: "next", ExpectedBuildID: "old", SchemaMigration: true}}
	host := &host{ctx: ctx, options: options, journal: journal{filepath.Join(dir, "supervisor")}, wake: make(chan struct{}, 1)}
	if err := host.journal.save(op); err != nil {
		t.Fatal(err)
	}
	cancel()
	host.finishUpdate(ctx, op, "", domain.WorkerBuild{BuildID: "next", Schema: 16, MigrationFrom: 15})
	pending, err := host.journal.load(op.Input.ID)
	if err != nil || !interruptedSchemaMigration(pending) {
		t.Fatal("shutdown replaced the recoverable precommit receipt", pending, err)
	}
}
