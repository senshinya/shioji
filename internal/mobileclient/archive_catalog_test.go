package mobileclient

import (
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestArchiveCatalogKeepsWorkspaceScopeAndSearchDeviceQualified(t *testing.T) {
	earlier := time.Now().Add(-time.Hour)
	later := earlier.Add(time.Minute)
	makeCatalog := func(device, address string, archivedAt *time.Time) DeviceCatalog {
		foreign := domain.Session{ID: "foreign", WorkspaceID: "same"}
		foreign.Context.DeviceID = "another-device"
		return DeviceCatalog{Address: address, Snapshot: domain.Snapshot{
			DeviceID: device, Generation: device + "-generation",
			Workspaces: []domain.Workspace{{ID: "same", DeviceID: device, Name: "Build", Archived: true, ArchivedAt: archivedAt}},
			Sessions:   []domain.Session{{ID: "same-session", WorkspaceID: "same", Title: "History"}, foreign},
		}}
	}
	a, b := makeCatalog("a", "studio-a", &earlier), makeCatalog("b", "studio-b", &later)
	b.Name = "Build Server"
	rows := CollectArchive([]DeviceCatalog{a, b}, WorkspaceLocation{})
	if len(rows.Sessions) != 2 || len(rows.Workspaces) != 2 || rows.Sessions[0].Device.ID != "b" ||
		rows.Workspaces[0].Device.ID != "b" || rows.Workspaces[0].Count != 1 {
		t.Fatalf("wrong archive sources, order or count: %+v", rows)
	}
	hits := rows.Match(" studio-a ")
	if len(hits.Workspaces) != 1 || len(hits.Sessions) != 1 || hits.Sessions[0].Device.ID != "a" ||
		hits.Workspaces[0].Device.ID != "a" {
		t.Fatalf("device search mixed identical IDs: %+v", hits)
	}
	if byName := rows.Match("build server"); len(byName.Workspaces) != 1 || len(byName.Sessions) != 1 || byName.Sessions[0].Device.ID != "b" {
		t.Fatalf("could not find the archive by its owning device's name: %+v", byName)
	}
	scoped := CollectArchive([]DeviceCatalog{a, b}, WorkspaceLocation{DeviceID: "b", WorkspaceID: "same"})
	if len(scoped.Sessions) != 1 || scoped.Sessions[0].Device.ID != "b" ||
		len(scoped.Workspaces) != 1 || scoped.Workspaces[0].Device.ID != "b" {
		t.Fatalf("workspace scope escaped its owner: %+v", scoped)
	}
}
