package app

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func (m *Model) EncodeState() ([]byte, error) {
	return json.Marshal(savedState{QueueExpanded: m.QueueExpanded, QueueEdits: m.QueueEdits, WorkspaceID: m.WorkspaceID, Drafts: m.Drafts, Configs: m.Configs, Answers: m.Answers, Pager: m.Pager, Collapsed: m.Collapsed, Pending: m.Pending, DeviceID: m.KnownDeviceID, GitDrafts: m.GitDrafts, GitRepositories: m.GitRepositories, Inspectors: m.Inspectors})
}
func WriteState(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "client-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
