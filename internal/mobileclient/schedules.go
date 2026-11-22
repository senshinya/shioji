package mobileclient

import (
	"context"
	"errors"

	"golang.org/x/crypto/ssh"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type DeviceSchedules struct {
	DeviceID   string
	Address    string
	Minor      int
	Workspaces []domain.Workspace
	Tasks      []domain.Schedule
}

type ScheduleStatus string

const (
	ScheduleEnabled  ScheduleStatus = "enabled"
	SchedulePaused   ScheduleStatus = "paused"
	ScheduleOffline  ScheduleStatus = "offline"
	ScheduleArchived ScheduleStatus = "archived"
)

type ScheduleRow struct {
	Task      domain.Schedule
	Workspace domain.Workspace
	Status    ScheduleStatus
}

func ReadDeviceSchedules(ctx context.Context, device SavedDevice, preferred Endpoint, signer ssh.Signer, route RouteDial) (DeviceSchedules, error) {
	return readVerifiedDevice(ctx, device, preferred, signer, route, readConnectedSchedules)
}

func readConnectedSchedules(ctx context.Context, client *protocol.Client, address string) (DeviceSchedules, error) {
	out := DeviceSchedules{DeviceID: client.DeviceID, Address: address, Minor: client.Minor}
	if client.Minor < protocol.ScheduleMinor {
		return out, nil
	}
	if err := client.Call(ctx, "schedule.list", nil, &out.Tasks); err != nil {
		return DeviceSchedules{}, err
	}
	for _, task := range out.Tasks {
		if task.ID == "" || task.DeviceID != client.DeviceID || task.Text != "" {
			return DeviceSchedules{}, errors.New("设备返回了无效的定时任务列表")
		}
	}
	catalog, err := readDeviceCatalog(ctx, client)
	if err != nil {
		return DeviceSchedules{}, err
	}
	for _, workspace := range catalog.Workspaces {
		if workspace.ID != "" && workspace.DeviceID == client.DeviceID {
			workspace.Folders = nil
			out.Workspaces = append(out.Workspaces, workspace)
		}
	}
	return out, nil
}

func BrowseDeviceSchedules(catalog DeviceSchedules, scope WorkspaceLocation, status ScheduleStatus, online bool) []ScheduleRow {
	workspaces := make(map[string]domain.Workspace, len(catalog.Workspaces))
	for _, workspace := range catalog.Workspaces {
		if workspace.ID != "" && workspace.DeviceID == catalog.DeviceID && scope.Includes(workspace) {
			workspaces[workspace.ID] = workspace
		}
	}
	var rows []ScheduleRow
	for _, task := range catalog.Tasks {
		workspace, found := workspaces[task.WorkspaceID]
		if !found || task.ID == "" || task.DeviceID != catalog.DeviceID || task.Text != "" {
			continue
		}
		row := ScheduleRow{Task: task, Workspace: workspace, Status: SchedulePaused}
		switch {
		case !online:
			row.Status = ScheduleOffline
		case workspace.Archived:
			row.Status = ScheduleArchived
		case task.Enabled:
			row.Status = ScheduleEnabled
		}
		if status == "" || status == row.Status ||
			(status == SchedulePaused && row.Status == ScheduleArchived && !task.Enabled) {
			rows = append(rows, row)
		}
	}
	return rows
}
