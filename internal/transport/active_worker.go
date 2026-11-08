package transport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func SaveActiveWorker(directory, buildID string) error {
	if _, err := StagedWorkerPath(directory, buildID); err != nil {
		return err
	}
	data, _ := json.Marshal(struct {
		BuildID string `json:"buildId"`
	}{buildID})
	f, err := os.CreateTemp(directory, ".active-worker-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(directory, "active-worker.json")); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func ActiveWorkerBuild(directory string) (string, error) {
	path := filepath.Join(directory, "active-worker.json")
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > 256 {
		return "", errors.New("已接纳的执行服务启动标识无效")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var saved struct {
		BuildID string `json:"buildId"`
	}
	if err = json.Unmarshal(data, &saved); err != nil {
		return "", err
	}
	if _, err = StagedWorkerPath(directory, saved.BuildID); err != nil {
		return "", err
	}
	return saved.BuildID, nil
}
