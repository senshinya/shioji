package mobileclient

import (
	"slices"
	"strings"
	"time"

	"shioji/internal/domain"
)

type ArchiveDevice struct {
	ID, Address, Generation string
	Name                    string
	Minor                   int
}

func (d ArchiveDevice) Label() string {
	return DeviceLabel(d.Name, d.Address)
}

func (d DeviceCatalog) ArchiveDevice() ArchiveDevice {
	return ArchiveDevice{ID: d.Snapshot.DeviceID, Address: d.Address, Name: d.Name, Generation: d.Snapshot.Generation, Minor: d.Minor}
}

type ArchivedWorkspace struct {
	domain.Workspace
	Device ArchiveDevice
	Count  int
}

type ArchivedConversation struct {
	SessionCatalogEntry
	Device ArchiveDevice
}

type ArchiveRows struct {
	Sessions   []ArchivedConversation
	Workspaces []ArchivedWorkspace
}

func CollectArchive(catalogs []DeviceCatalog, scope WorkspaceLocation) ArchiveRows {
	var out ArchiveRows
	for _, catalog := range catalogs {
		snapshot := catalog.Snapshot
		if snapshot.DeviceID == "" || scope.DeviceID != "" && snapshot.DeviceID != scope.DeviceID {
			continue
		}
		device := catalog.ArchiveDevice()
		counts := make(map[string]int)
		for _, entry := range BrowseSessionCatalog(snapshot, scope, true) {
			out.Sessions = append(out.Sessions, ArchivedConversation{SessionCatalogEntry: entry, Device: device})
			counts[entry.Workspace.ID]++
		}
		for _, workspace := range snapshot.Workspaces {
			if workspace.ID != "" && workspace.DeviceID == device.ID && workspace.Archived && scope.Includes(workspace) {
				out.Workspaces = append(out.Workspaces, ArchivedWorkspace{Workspace: workspace, Device: device, Count: counts[workspace.ID]})
			}
		}
	}
	slices.SortStableFunc(out.Sessions, func(a, b ArchivedConversation) int {
		return archivedAt(b.Session.ArchivedAt, b.Workspace.ArchivedAt).Compare(archivedAt(a.Session.ArchivedAt, a.Workspace.ArchivedAt))
	})
	slices.SortStableFunc(out.Workspaces, func(a, b ArchivedWorkspace) int {
		return archivedAt(b.ArchivedAt, nil).Compare(archivedAt(a.ArchivedAt, nil))
	})
	return out
}

func (r ArchiveRows) Match(query string) ArchiveRows {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return r
	}
	var out ArchiveRows
	for _, row := range r.Sessions {
		if strings.Contains(strings.ToLower(row.Session.Title+" "+row.Workspace.Name+" "+row.Device.Label()), query) {
			out.Sessions = append(out.Sessions, row)
		}
	}
	for _, row := range r.Workspaces {
		if strings.Contains(strings.ToLower(row.Name+" "+row.Device.Label()), query) {
			out.Workspaces = append(out.Workspaces, row)
		}
	}
	return out
}

func archivedAt(primary, workspace *time.Time) time.Time {
	if primary != nil {
		return *primary
	}
	if workspace != nil {
		return *workspace
	}
	return time.Time{}
}
