package app

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func (m *Model) EncodeState() ([]byte, error) {
	completed := !m.FirstRun
	return json.Marshal(savedState{WorkerMaintenanceIDs: m.WorkerMaintenanceIDs, Navigation: m.Navigation, ServicePreparations: m.ServicePreparations, ServicePreparationDeviceID: m.ServicePreparationDeviceID, WorkerMigrations: m.WorkerMigrations, SignInIDs: m.SignInIDs, SetupCompleted: &completed, ModelMenus: m.ModelMenus, LocalDeviceName: m.LocalDeviceName, LocalDeviceIcon: m.LocalDeviceIcon, DraftSkills: m.DraftSkills, HomeConfigContext: m.homeConfigContext, HomeHarnesses: m.HomeHarnesses, HarnessConfigs: m.HarnessConfigs, HistoryActions: m.HistoryActions, HistoryEdits: m.HistoryEdits, Preferences: m.Preferences, SecondaryWorkspaceID: m.SecondaryWorkspaceID, SecondarySessionID: m.SecondarySessionID, FocusSecondary: m.FocusSecondary, Attachments: m.Attachments, DeviceCache: m.cachedDevices(), Connections: normalizedConnections(m.Connections), ActiveConnection: m.ActiveConnection, PendingDevices: m.PendingDevices, QueueExpanded: m.QueueExpanded, QueueEdits: m.QueueEdits, WorkspaceID: m.WorkspaceID, Drafts: m.Drafts, Configs: m.Configs, Answers: m.Answers, Pager: m.Pager, Collapsed: m.Collapsed, Pending: m.Pending, DeviceID: m.localIdentity(), GitDrafts: m.GitDrafts, GitRepositories: m.GitRepositories, Inspectors: m.Inspectors, InspectorLayout: m.InspectorLayout, InspectorSplitHeight: m.InspectorSplitHeight, InspectorWidth: m.InspectorWidth})
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
