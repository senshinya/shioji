package mobileclient

import (
	"context"
	"net"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestDeviceScheduleRowsUseOwningWorkspaceAndConnection(t *testing.T) {
	catalog := DeviceSchedules{
		DeviceID: "worker",
		Workspaces: []domain.Workspace{
			{ID: "team", DeviceID: "worker"},
			{ID: "archived", DeviceID: "worker", Archived: true},
			{ID: "foreign", DeviceID: "other-worker"},
		},
		Tasks: []domain.Schedule{
			{ID: "enabled", DeviceID: "worker", WorkspaceID: "team", Enabled: true},
			{ID: "paused", DeviceID: "worker", WorkspaceID: "team"},
			{ID: "archived-task", DeviceID: "worker", WorkspaceID: "archived", Enabled: true},
			{ID: "cross-device", DeviceID: "other-worker", WorkspaceID: "team", Enabled: true},
			{ID: "invalid-workspace", DeviceID: "worker", WorkspaceID: "foreign", Enabled: true},
		},
	}
	team := WorkspaceLocation{DeviceID: "worker", WorkspaceID: "team"}
	if rows := BrowseDeviceSchedules(catalog, team, ScheduleEnabled, true); len(rows) != 1 || rows[0].Task.ID != "enabled" {
		t.Fatalf("unexpected enabled rows: %+v", rows)
	}
	if rows := BrowseDeviceSchedules(catalog, team, SchedulePaused, true); len(rows) != 1 || rows[0].Task.ID != "paused" {
		t.Fatalf("unexpected paused rows: %+v", rows)
	}
	if rows := BrowseDeviceSchedules(catalog, WorkspaceLocation{DeviceID: "other-worker", WorkspaceID: "team"}, "", true); len(rows) != 0 {
		t.Fatalf("another device's identical workspace ID matched: %+v", rows)
	}
	if rows := BrowseDeviceSchedules(catalog, WorkspaceLocation{}, ScheduleOffline, false); len(rows) != 3 {
		t.Fatalf("last known rows were not retained offline: %+v", rows)
	}
	if rows := BrowseDeviceSchedules(catalog, WorkspaceLocation{}, ScheduleEnabled, false); len(rows) != 0 {
		t.Fatalf("offline tasks appeared enabled: %+v", rows)
	}
}

func TestReadDeviceSchedulesRejectsInvalidSummary(t *testing.T) {
	for _, task := range []domain.Schedule{
		{ID: "foreign", DeviceID: "other-worker", WorkspaceID: "team"},
		{ID: "private", DeviceID: "worker", WorkspaceID: "team", Text: "full prompt"},
	} {
		t.Run(task.ID, func(t *testing.T) {
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			served := make(chan error, 1)
			go func() {
				codec, epoch, err := protocol.Welcome(right, "worker")
				if err == nil {
					err = writeSearchFixture(codec, epoch, "schedule.list", []domain.Schedule{task})
				}
				served <- err
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := protocol.Connect(ctx, left, "worker")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readConnectedSchedules(ctx, client, "worker.test:22"); err == nil {
				t.Fatal("accepted an unverified or private schedule summary")
			}
			if err := <-served; err != nil {
				t.Fatal(err)
			}
		})
	}
}
