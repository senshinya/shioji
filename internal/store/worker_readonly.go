package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"shioji/internal/domain"
)

// ReadWorkerStopReady observes the still-running old writer's committed WAL.
// It never opens Store or applies migrations while that writer owns the device.
func ReadWorkerStopReady(ctx context.Context, directory, expected string) (domain.WorkerUpdateResult, error) {
	u := url.URL{Scheme: "file", Path: filepath.Join(directory, "state.sqlite")}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(200)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
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
