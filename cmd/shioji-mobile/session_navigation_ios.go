package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

const (
	sessionCatalogTimeout  = 15 * time.Second
	sessionCatalogInterval = 25 * time.Second
)

const (
	workspaceSettingsAction = "workspace-settings"
	workspaceImportAction   = "workspace-import"
	workspaceArchiveAction  = "workspace-archive"
)

type sessionListTarget struct {
	location     app.SessionLocation
	action       string
	pinned       bool
	archived     bool
	notification *mobileNotificationRoute
	attention    *attentionTarget
	title        string
	scope        string
	problem      string
	attempt      uint64
	retry        bool
}

type sessionGrouping string

const (
	groupByStatus    sessionGrouping = "status"
	groupByTime      sessionGrouping = "time"
	groupByWorkspace sessionGrouping = "workspace"
	groupByManual    sessionGrouping = "manual"
)

type sessionGroupingSettings struct {
	Global    sessionGrouping `json:"global,omitempty"`
	Workspace sessionGrouping `json:"workspace,omitempty"`
}

func (a *mobileApp) groupingForScope(global bool) sessionGrouping {
	group := a.sessionGroupings.Workspace
	if global {
		group = a.sessionGroupings.Global
	}
	if group == groupByTime || global && group == groupByWorkspace || !global && group == groupByManual {
		return group
	}
	return groupByStatus
}

func (a *mobileApp) groupingFile() string {
	return filepath.Join(a.rootDirectory, "session-groupings.json")
}

func (a *mobileApp) loadSessionBrowsing() {
	if a.rootDirectory != "" {
		a.loadNavigationOrder()
	}
	if value, err := os.ReadFile(a.groupingFile()); err == nil {
		_ = json.Unmarshal(value, &a.sessionGroupings)
	} else if os.IsNotExist(err) {
		if legacy, err := os.ReadFile(filepath.Join(a.rootDirectory, "workspace-grouping")); err == nil && strings.TrimSpace(string(legacy)) == "true" {
			a.sessionGroupings.Global = groupByWorkspace
		}
	}
	a.sessionCatalogs = make(map[string]mobileclient.DeviceCatalog)
	for _, device := range a.savedDevices.Devices {
		id := device.Endpoint.DeviceID
		if id == a.endpoint.DeviceID {
			continue
		}
		if directory, err := a.deviceDirectory(device); err == nil {
			cached := readCachedCatalog(directory, id)
			a.sessionCatalogs[id] = mobileclient.DeviceCatalog{Snapshot: cached.Snapshot, Address: device.Endpoint.Address}
		}
	}
	for _, catalog := range a.browseCatalogs() {
		a.observeNavigationOrder(catalog.Snapshot)
	}
}

func (a *mobileApp) setSessionGrouping(group sessionGrouping, global bool) {
	if a.groupingForScope(global) == group {
		return
	}
	settings := a.sessionGroupings
	if global {
		settings.Global = group
	} else {
		settings.Workspace = group
	}
	if !a.preview && a.rootDirectory != "" {
		data, _ := json.Marshal(settings)
		if err := app.WriteState(a.groupingFile(), data); err != nil {
			a.sessionListNotice = "无法保存会话分组"
			a.home.Invalidate()
			return
		}
	}
	a.sessionGroupings = settings
	a.home.Invalidate()
}

func (a *mobileApp) cacheSessionCatalog() {
	id := a.endpoint.DeviceID
	if a.sessionCatalogs == nil || id == "" || id != a.model.Snapshot.DeviceID {
		return
	}
	// Do not replace a verified browse result with an older disk snapshot
	// when leaving a device that still cannot be reached.
	if a.model.Connected || a.sessionCatalogs[id].Snapshot.DeviceID == "" {
		catalog := a.sessionCatalog(id)
		catalog.Snapshot.Operations = nil
		catalog.RouteID = a.activeEndpoint().RouteID()
		catalog.Timing = a.activeDeviceTiming(catalog.RouteID)
		catalog.CheckedAt = time.Now()
		if a.model.Connected && catalog.RequestedAt.IsZero() {
			catalog.RequestedAt = catalog.CheckedAt
		}
		a.sessionCatalogs[id] = catalog
	}
}

func (a *mobileApp) sessionCatalog(id string) mobileclient.DeviceCatalog {
	name := a.splitRoot().savedDevice(id).Name
	if id != "" && id == a.model.Snapshot.DeviceID {
		catalog := mobileclient.DeviceCatalog{Snapshot: a.model.Snapshot, Address: a.activeEndpoint().Address, Name: name, Minor: a.model.WorkerMinor}
		if a.preview {
			catalog.Address = a.deviceName()
		}
		if a.model.Connected {
			catalog.RequestedAt = catalog.Snapshot.ObservedAt
		}
		return catalog
	}
	if pane := a.parallelDevice; pane != nil && pane.model.Snapshot.DeviceID == id && pane.model.Connected {
		return pane.sessionCatalog(id)
	}
	catalog := a.sessionCatalogs[id]
	catalog.Name = name
	return catalog
}

func (a *mobileApp) browseCatalogs() []mobileclient.DeviceCatalog {
	var catalogs []mobileclient.DeviceCatalog
	currentIncluded := false
	for _, saved := range a.savedDevices.Devices {
		id := saved.Endpoint.DeviceID
		catalog := a.sessionCatalog(id)
		if catalog.Snapshot.DeviceID == id && id != "" {
			catalogs = append(catalogs, catalog)
			currentIncluded = currentIncluded || id == a.model.Snapshot.DeviceID
		}
	}
	if id := a.model.Snapshot.DeviceID; !currentIncluded && id != "" {
		catalogs = append(catalogs, a.sessionCatalog(id))
	}
	return catalogs
}

func (a *mobileApp) refreshSessionCatalogs() {
	if a.signer == nil || a.sessionCatalogs == nil {
		return
	}
	a.catalogNextRefresh = time.Now().Add(sessionCatalogInterval)
	if a.sessionCatalogCancel != nil {
		a.sessionCatalogCancel()
	}
	refresh, cancel := context.WithCancel(context.Background())
	a.sessionCatalogCancel = cancel
	signer, route := a.signer, a.routeDial()
	for _, device := range a.savedDevices.Devices {
		id := device.Endpoint.DeviceID
		if id == a.endpoint.DeviceID || a.parallelDevice != nil && id == a.parallelDevice.endpoint.DeviceID {
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(refresh, sessionCatalogTimeout)
			defer cancel()
			catalog, err := mobileclient.ReadDeviceCatalog(ctx, device, signer, route)
			mygo.RunOnMain(func() {
				if refresh.Err() != nil || id == a.endpoint.DeviceID || a.parallelDevice != nil && id == a.parallelDevice.endpoint.DeviceID {
					return
				}
				if err == nil && catalog.Snapshot.DeviceID == id {
					catalog.Snapshot.Operations = nil
					a.sessionCatalogs[id] = catalog
					a.observeNavigationOrder(catalog.Snapshot)
					a.updateArchivedBrowseScope(catalog.Snapshot)
					a.saveBrowseCatalog(device, catalog.Snapshot)
				} else {
					last := a.sessionCatalogs[id]
					last.RequestedAt = time.Time{}
					last.CheckedAt = time.Now()
					a.sessionCatalogs[id] = last
				}
				a.refresh()
			})
		}()
	}
}

func (a *mobileApp) maintainSessionCatalogs(c *ui.Context) {
	if a.preview || a.signer == nil || len(a.savedDevices.Devices) < 2 || !c.WindowActive() || !mygo.App.IsActive() {
		return
	}
	if !c.Now().Before(a.catalogNextRefresh) {
		a.refreshSessionCatalogs()
	}
	if delay := a.catalogNextRefresh.Sub(c.Now()); delay > 0 {
		c.After(delay)
	}
}

func (a *mobileApp) saveBrowseCatalog(device savedMobileDevice, snapshot domain.Snapshot) {
	directory, err := a.deviceDirectory(device)
	if err != nil {
		return
	}
	cached := readCachedCatalog(directory, snapshot.DeviceID)
	if cached.Snapshot.Generation == snapshot.Generation && cached.Snapshot.Sequence > snapshot.Sequence {
		return
	}
	cached.Snapshot = snapshot
	if data, err := json.Marshal(cached); err == nil {
		_ = app.WriteState(filepath.Join(directory, "catalog.json"), data)
	}
}

func (a *mobileApp) selectListScope(scope mobileclient.WorkspaceLocation) {
	a.cancelSessionListOpening()
	a.sessionListNotice = ""
	if !a.workspaceArchiveRequest.busy {
		a.workspaceArchiveRequest.err = ""
	}
	a.filter, a.archive = scope, false
}

func (a *mobileApp) updateArchivedBrowseScope(snapshot domain.Snapshot) {
	if a.filter.WorkspaceID == "" || a.filter.DeviceID != snapshot.DeviceID {
		return
	}
	for _, workspace := range snapshot.Workspaces {
		if workspace.Archived && a.filter.Includes(workspace) {
			a.selectListScope(mobileclient.WorkspaceLocation{})
			return
		}
	}
}

func (a *mobileApp) openSessionListTarget(target sessionListTarget) {
	if a.searching {
		a.callUIKit("closeSearch", nil, nil)
	}
	waiting := a.sessionListOpening != nil
	a.cancelSessionListOpening()
	a.sessionListNotice = ""
	ref := target.location
	canOpen := a.model.Connected || a.preview || (!target.pinned && !waiting)
	if target.notification != nil || (target.attention != nil || target.action == "archive" || target.action == workspaceArchiveAction) && !a.preview {
		canOpen = a.model.Connected && a.model.ConnectionEpoch != "" && a.endpoint.DeviceID == ref.DeviceID
	}
	if ref.DeviceID == a.model.Snapshot.DeviceID && canOpen {
		a.presentSessionListTarget(target)
		a.home.Invalidate()
		return
	}
	if ref.SessionID != "" && target.action == "" && target.notification == nil && target.attention == nil && a.chat == nil {
		// The browse catalog supplies the destination header until the
		// owning device confirms the session and returns its history.
		catalog := a.sessionCatalog(ref.DeviceID)
		for _, session := range catalog.Snapshot.Sessions {
			if session.ID == ref.SessionID && session.WorkspaceID == ref.WorkspaceID {
				target.title = session.Title
				if target.title == "" {
					target.title = "会话"
				}
				for _, workspace := range catalog.Snapshot.Workspaces {
					if workspace.ID == ref.WorkspaceID && workspace.DeviceID == ref.DeviceID {
						target.scope = conversationHeaderScope(workspace.Name, a.deviceLabel(ref.DeviceID, catalog.Address))
						break
					}
				}
				break
			}
		}
	}
	if err := a.openDeviceFromResults(a.home, ref.DeviceID); err != nil {
		a.sessionListNotice = err.Error()
		a.home.Invalidate()
		return
	}
	if target.title != "" {
		a.sessionOpening = &target
		a.awaitSessionListTarget(&target)
		if !a.wide {
			a.pushConversation(a.home)
		}
	} else {
		a.sessionListNotice = "正在连接所属设备…"
		a.awaitSessionListTarget(&target)
	}
}

func (a *mobileApp) awaitSessionListTarget(target *sessionListTarget) {
	a.sessionListOpening = target
	target.attempt++
	attempt := target.attempt
	a.refresh()
	time.AfterFunc(sessionCatalogTimeout, func() {
		mygo.RunOnMain(func() {
			if a.sessionListOpening == target && target.attempt == attempt {
				a.sessionListOpening = nil
				if a.sessionOpening == target {
					target.problem = "暂时连不上所属设备"
					target.retry = true
				} else {
					a.sessionListNotice = "无法连接所属设备"
				}
				a.refresh()
			}
		})
	})
}

func (a *mobileApp) cancelSessionListOpening() {
	a.cancelAttentionShortcut()
	if a.sessionListOpening != nil {
		a.sessionListOpening, a.sessionListNotice = nil, ""
	}
	if a.sessionOpening != nil {
		a.sessionOpening = nil
		if a.chat != nil {
			a.chat.Close()
		}
	}
}

func (a *mobileApp) completeSessionListOpening() {
	target := a.sessionListOpening
	if target == nil || !a.model.Connected || a.model.Snapshot.DeviceID != target.location.DeviceID ||
		a.endpoint.DeviceID != target.location.DeviceID || (target.notification != nil || target.attention != nil || target.action == workspaceArchiveAction) && a.model.ConnectionEpoch == "" {
		return
	}
	a.sessionListOpening, a.sessionListNotice = nil, ""
	inConversation := a.sessionOpening == target
	if inConversation {
		a.sessionLoadingTarget = target.location.SessionID
		a.sessionOpening = nil
	}
	a.presentSessionListTarget(*target)
	if inConversation && a.sessionListNotice != "" {
		target.problem = a.sessionListNotice
		a.sessionLoadingTarget = ""
		a.sessionOpening, a.sessionListNotice = target, ""
	}
}

func (a *mobileApp) presentSessionListTarget(target sessionListTarget) {
	ref := target.location
	if target.pinned && !a.isSessionPinned(ref) {
		if target.title != "" {
			a.sessionListNotice = "会话已取消置顶"
		}
		return
	}
	workspace := a.workspace(ref.WorkspaceID)
	if workspace.ID == "" || workspace.DeviceID != ref.DeviceID || (workspace.Archived && !target.archived && target.notification == nil) {
		a.sessionListNotice = "工作区已归档或移除"
		return
	}
	if ref.SessionID == "" {
		parent := a.home
		if a.wide && a.detail != nil {
			parent = a.detail
		}
		switch target.action {
		case workspaceSettingsAction:
			a.openWorkspace(parent, workspace)
		case workspaceImportAction:
			a.openConversationImport(parent, workspace)
		case workspaceArchiveAction:
			if a.canChangeWorkspaceArchive(workspace) {
				a.confirmWorkspaceArchive(a.home, &a.workspaceArchiveRequest, workspace, nil)
			} else {
				a.sessionListNotice = "需要连接并更新执行服务"
			}
		default:
			a.model.WorkspaceID = workspace.ID
			a.presentDraft(a.home, false)
		}
		return
	}
	session := a.sessionByID(ref.SessionID)
	if session.ID == "" || session.WorkspaceID != workspace.ID ||
		(session.Context.DeviceID != "" && session.Context.DeviceID != ref.DeviceID) ||
		target.notification == nil && (session.Archived || workspace.Archived) != target.archived {
		a.sessionListNotice = "会话已改变或移除"
		return
	}
	if target.notification != nil {
		a.presentNotificationTarget(*target.notification, session, session.Archived || workspace.Archived)
		return
	}
	if target.attention != nil {
		if notice := a.openAttentionTarget(a.home, *target.attention); notice != "" {
			a.sessionListNotice = notice
		}
		return
	}
	switch target.action {
	case "manage":
		a.openSessionActions(a.home, session.ID)
		return
	case "archive":
		if !workspace.Archived && a.model.Connected {
			a.confirmSessionArchive(a.home, session)
		}
		return
	}
	a.openSession(session.ID)
}
