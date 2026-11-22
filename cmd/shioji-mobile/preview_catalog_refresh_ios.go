package main

import (
	"time"

	"github.com/egoist/mygo"

	"shioji/internal/domain"
)

func (a *mobileApp) seedSessionCatalogRefresh(device string) {
	a.sessionGroupings.Global = groupByWorkspace
	catalog := a.sessionCatalogs[device]
	cached := catalog.Snapshot
	cached.Workspaces = append(cached.Workspaces, domain.Workspace{ID: "old-workspace", DeviceID: device, Name: "旧工作区"})
	cached.Sessions = append(cached.Sessions,
		domain.Session{ID: "old-session", WorkspaceID: "house", Title: "旧会话", Status: domain.ExecutionCompleted},
		domain.Session{ID: "old-workspace-session", WorkspaceID: "old-workspace", Title: "旧工作区的会话", Status: domain.ExecutionCompleted},
	)
	catalog.Snapshot = cached
	catalog.RequestedAt = time.Time{}
	a.sessionCatalogs[device] = catalog
	time.AfterFunc(6*time.Second, func() {
		mygo.RunOnMain(func() {
			fresh := cached
			fresh.Workspaces = []domain.Workspace{cached.Workspaces[0], {ID: "new-workspace", DeviceID: device, Name: "新工作区"}}
			fresh.Sessions = []domain.Session{
				cached.Sessions[0],
				{ID: "new-session", WorkspaceID: "house", Title: "新会话", Status: domain.ExecutionCompleted},
				{ID: "new-workspace-session", WorkspaceID: "new-workspace", Title: "新工作区的会话", Status: domain.ExecutionCompleted},
			}
			catalog.Snapshot = fresh
			catalog.RequestedAt = time.Now()
			a.sessionCatalogs[device] = catalog
			a.observeNavigationOrder(fresh)
			a.refresh()
		})
	})
}
