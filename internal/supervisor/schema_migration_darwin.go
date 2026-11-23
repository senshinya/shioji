package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/store"
)

func schemaMigrationDirectory(directory, id string) string {
	return filepath.Join(directory, "schema-migration-"+id)
}

func saveSchemaPlan(directory string, op Operation) error {
	return atomicJSON(filepath.Join(directory, "supervisor", "schema-plans", op.Input.ID+".json"), op)
}

func sameSchemaIntent(a, b domain.WorkerReplacement) bool {
	a.SchemaMigration, b.SchemaMigration = false, false
	a.RuntimeHandoff, b.RuntimeHandoff = false, false
	return a == b
}

// Older fixed hosts rewrite JSON with the fields known to their build. The
// independent plan retains the authorized schemas across that reconstruction.
func hydrateSchemaPlan(directory string, op Operation) Operation {
	if !safeID(op.Input.ID) {
		return op
	}
	var plan Operation
	path := filepath.Join(directory, "supervisor", "schema-plans", op.Input.ID+".json")
	if err := readJSON(path, &plan); err == nil && plan.Input.SchemaMigration &&
		plan.SourceSchema > 0 && plan.TargetSchema > plan.SourceSchema &&
		plan.DeviceID == op.DeviceID && plan.PreviousBuild == op.PreviousBuild && sameSchemaIntent(plan.Input, op.Input) {
		op.Input.SchemaMigration = true
		op.Input.RuntimeHandoff = plan.Input.RuntimeHandoff
		op.SourceSchema, op.TargetSchema = plan.SourceSchema, plan.TargetSchema
	}
	return op
}

func schemaMigrationSpace(directory string) error {
	var fs unix.Statfs_t
	if err := unix.Statfs(directory, &fs); err != nil {
		return err
	}
	db, err := os.Stat(filepath.Join(directory, "state.sqlite"))
	if err != nil {
		return err
	}
	bytes := uint64(db.Size())
	wal, err := os.Stat(filepath.Join(directory, "state.sqlite-wal"))
	if err == nil {
		bytes += uint64(wal.Size())
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if 2*bytes > fs.Bavail*uint64(fs.Bsize) {
		return errors.New("磁盘空间不足，无法准备数据库迁移")
	}
	return nil
}

func schemaAt(ctx context.Context, directory, device string) (int, error) {
	actual, schema, err := store.ReadWorkerIdentity(ctx, directory)
	if err == nil && actual != device {
		err = errors.New("迁移副本的设备身份与原业务库不一致")
	}
	return schema, err
}

func stageSchemaMigration(ctx context.Context, directory string, op Operation, from, to int, migrate func(string) error) error {
	working := schemaMigrationDirectory(directory, op.Input.ID)
	if err := os.Mkdir(working, 0700); err != nil {
		return err
	}
	copy := filepath.Join(working, "state.sqlite")
	if actual, err := schemaAt(ctx, directory, op.DeviceID); err != nil {
		return err
	} else if actual != from {
		return fmt.Errorf("实际数据库 schema 为 %d，预期 %d", actual, from)
	}
	if err := store.CopyWorkerDatabase(ctx, directory, copy); err != nil {
		return err
	}
	if err := os.Chmod(copy, 0600); err != nil {
		return err
	}
	if err := migrate(working); err != nil {
		return err
	}
	if actual, err := schemaAt(ctx, working, op.DeviceID); err != nil {
		return err
	} else if actual != to {
		return fmt.Errorf("迁移后的数据库 schema 为 %d，预期 %d", actual, to)
	}
	if err := store.CheckWorkerDatabase(ctx, working); err != nil {
		return err
	}
	return swapMigrationDatabase(ctx, directory, working)
}

func restoreSchemaMigration(ctx context.Context, directory string, op Operation, from, to int) error {
	schema, err := schemaAt(ctx, directory, op.DeviceID)
	if err != nil || schema == from {
		return err
	}
	if schema != to {
		return fmt.Errorf("无法核对待恢复数据库的 schema %d", schema)
	}
	original := schemaMigrationDirectory(directory, op.Input.ID)
	if schema, err = schemaAt(ctx, original, op.DeviceID); err != nil {
		return err
	}
	if schema != from {
		return fmt.Errorf("原数据库的 schema 为 %d，预期 %d", schema, from)
	}
	return swapMigrationDatabase(ctx, directory, original)
}

func swapMigrationDatabase(ctx context.Context, directory, working string) error {
	for _, root := range []string{directory, working} {
		if err := store.CloseWorkerWAL(ctx, root); err != nil {
			return err
		}
		if err := syncMigrationPath(filepath.Join(root, "state.sqlite")); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.RenamexNp(filepath.Join(working, "state.sqlite"), filepath.Join(directory, "state.sqlite"), unix.RENAME_SWAP); err != nil {
		return err
	}
	return errors.Join(syncMigrationPath(working), syncMigrationPath(directory))
}

func syncMigrationPath(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func acquireDatabaseLease(directory string) (*os.File, error) {
	fd, err := unix.Open(filepath.Join(directory, "owner.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	lease := os.NewFile(uintptr(fd), "schema migration writer lease")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lease.Close()
		return nil, err
	}
	return lease, nil
}

func (h *host) withSchemaLease(run func() error) error {
	lease, err := acquireDatabaseLease(h.options.Directory)
	if err != nil {
		return err
	}
	defer lease.Close()
	return run()
}

func interruptedSchemaMigration(op Operation) bool {
	return op.Input.SchemaMigration && op.SourceSchema > 0 && op.TargetSchema > op.SourceSchema &&
		(op.Phase == "migrating" || op.Phase == "observing" || op.Phase == "rollback-stop" || op.Legacy && op.Phase == "switching" ||
			op.Phase == "blocked" && op.Error == interruptedSwitchError)
}

// InterruptedSchemaControl is also checked by the signed repair installer
// when a pre-migration launchd entry restarted and blocked on the old record.
func InterruptedSchemaControl(directory string) (Operation, bool) {
	op, err := (journal{filepath.Join(directory, "supervisor")}).current()
	op = hydrateSchemaPlan(directory, op)
	return op, err == nil && interruptedSchemaMigration(op)
}

func (h *host) recoverSchemaMigration(ctx context.Context, op Operation) (Operation, error) {
	restore, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	err := h.withSchemaLease(func() error {
		return restoreSchemaMigration(restore, h.options.Directory, op, op.SourceSchema, op.TargetSchema)
	})
	if err != nil && ctx.Err() != nil {
		return op, ctx.Err()
	}
	if err == nil {
		err = h.options.Select(op.PreviousBuild)
	}
	if err != nil {
		op.Phase, op.Error = "blocked", err.Error()
	} else {
		op.Phase, op.Error = "rolled-back", "上次迁移在提交前中断，已恢复原数据库"
	}
	return op, h.journal.save(op)
}

func (h *host) finishSchemaBootstrap(ctx context.Context, op Operation) {
	h.mu.Lock()
	h.updating, h.updateID = true, op.Input.ID
	h.mu.Unlock()
	path, build, err := h.options.Verify(ctx, op.Input.BuildID)
	if err == nil && (!build.MigratesSchema(op.SourceSchema) || build.Schema != op.TargetSchema) {
		err = errors.New("迁移候选构建或 schema 已变化")
	}
	if err != nil {
		h.failUpdate(op, err, true)
		return
	}
	h.finishUpdate(ctx, op, path, build)
}
