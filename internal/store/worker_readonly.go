package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"shioji/internal/domain"
)

func readWorkerDatabase(directory string) (*sql.DB, error) {
	path := filepath.Join(directory, "state.sqlite")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(200)"}}
	u.RawQuery = q.Encode()
	return sql.Open("sqlite", u.String())
}

// ReadWorkerIdentity never creates a store, migrates it or takes writer ownership.
func ReadWorkerIdentity(ctx context.Context, directory string) (string, int, error) {
	db, err := readWorkerDatabase(directory)
	if err != nil {
		return "", 0, err
	}
	defer db.Close()
	var device string
	var schema int
	if err = db.QueryRowContext(ctx, "SELECT device_id FROM metadata WHERE id=1").Scan(&device); err == nil {
		err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema)
	}
	if err == nil && device == "" {
		err = errors.New("执行设备身份记录为空")
	}
	return device, schema, err
}

// ReadWorkerStopReady observes the still-running old writer's committed WAL.
// It never opens Store or applies migrations while that writer owns the device.
func ReadWorkerStopReady(ctx context.Context, directory, expected string) (domain.WorkerUpdateResult, error) {
	db, err := readWorkerDatabase(directory)
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	defer db.Close()
	var device string
	if err = db.QueryRowContext(ctx, "SELECT device_id FROM metadata WHERE id=1").Scan(&device); err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	if expected == "" || device != expected {
		return domain.WorkerUpdateResult{}, errors.New("执行设备身份已变化")
	}
	var busy bool
	err = db.QueryRowContext(ctx, workerStopBlockQuery).Scan(&busy)
	result := domain.WorkerUpdateResult{Ready: !busy}
	if busy {
		result.Reason = "等待实际任务和未决输入完成"
	}
	return result, err
}
