package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
)

// CopyWorkerDatabase uses SQLite's committed state, including any WAL frames.
// The caller holds the exclusive worker lease and owns the destination.
func CopyWorkerDatabase(ctx context.Context, directory, destination string) error {
	db, err := readWorkerDatabase(directory)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", destination)
	return err
}

func CheckWorkerDatabase(ctx context.Context, directory string) error {
	db, err := readWorkerDatabase(directory)
	if err != nil {
		return err
	}
	defer db.Close()
	var check string
	if err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return errors.New("迁移后的数据库未通过 SQLite 完整性检查")
	}
	return nil
}

// CloseWorkerWAL folds the database into a single file before an atomic swap.
// SQLite itself refuses to change journal mode while another connection holds
// a conflicting lock. The caller must hold the worker lease throughout.
func CloseWorkerWAL(ctx context.Context, directory string) error {
	u := url.URL{Scheme: "file", Path: filepath.Join(directory, "state.sqlite")}
	u.RawQuery = "mode=rw&_pragma=busy_timeout%28200%29&_pragma=fullfsync%281%29"
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var mode string
	if err = db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil {
		return err
	}
	if mode != "delete" {
		return fmt.Errorf("无法关闭 SQLite WAL（%s）", mode)
	}
	return nil
}
