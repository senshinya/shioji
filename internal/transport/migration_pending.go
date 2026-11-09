package transport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
)

// The helper owns this record. Bootstrap reads only its normalized progress to
// avoid bypassing an interrupted migration with an ordinary worker launch.
func PendingWorkerMigration(directory string) (*domain.WorkerMigrationStatus, error) {
	root := filepath.Join(directory, "legacy-migrations")
	b, err := os.ReadFile(filepath.Join(root, "active"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id := string(b)
	if id == "" || len(id) > 80 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return nil, errors.New("迁移进度索引无效")
	}
	path := filepath.Join(root, id+".json")
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 128<<10 {
		return nil, errors.New("迁移进度记录不安全")
	}
	b, err = os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record struct {
		Format     int                          `json:"format"`
		Status     domain.WorkerMigrationStatus `json:"status"`
		StopIssued bool                         `json:"stopIssued"`
	}
	if err = json.Unmarshal(b, &record); err != nil {
		return nil, err
	}
	if record.Format != 1 || record.Status.ID != id || record.Status.DeviceID == "" {
		return nil, errors.New("迁移进度身份无效")
	}
	if record.Status.Phase.Active() || record.Status.Phase == domain.MigrationFailed && record.StopIssued {
		return &record.Status, nil
	}
	return nil, nil
}
