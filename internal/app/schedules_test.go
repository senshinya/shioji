package app

import (
	"path/filepath"
	"testing"

	"shioji/internal/domain"
)

func TestScheduleCacheKeepsOnlyOwnedReadOnlySummaries(t *testing.T) {
	model := NewModel()
	model.LocalDeviceID = "owner"
	workspace := domain.Workspace{ID: "work", DeviceID: "owner"}
	model.DeviceStates["local"] = &DeviceState{Connected: true, ConnectionEpoch: "epoch", Snapshot: domain.Snapshot{DeviceID: "owner", Workspaces: []domain.Workspace{workspace}},
		ScheduleSummaries: []domain.Schedule{
			{ID: "first", DeviceID: "owner", WorkspaceID: "work", Text: "private prompt", Latest: &domain.ScheduleRun{State: domain.ScheduleFailed, Error: "private output"}},
			{ID: "wrong", DeviceID: "another-device", WorkspaceID: "work"},
			{ID: "missing", DeviceID: "owner", WorkspaceID: "deleted-workspace"},
		}}
	path := filepath.Join(t.TempDir(), "client.json")
	if err := model.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	state := restored.DeviceStates["local"]
	if state == nil || state.Connected || len(state.ScheduleSummaries) != 1 {
		t.Fatal("restored wrong sources or connected cached state", state)
	}
	task := state.ScheduleSummaries[0]
	if task.ID != "first" || task.Text != "" || task.Latest.Error != "" || task.Latest.State != domain.ScheduleFailed {
		t.Fatal("cached detail beyond summary or lost state", task)
	}
}
