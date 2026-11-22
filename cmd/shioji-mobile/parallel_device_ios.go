package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/mobileclient"
)

func (a *mobileApp) splitRoot() *mobileApp {
	if a.parallelOwner != nil {
		return a.parallelOwner
	}
	return a
}

func (a *mobileApp) openParallelSession(ref app.SessionLocation) error {
	if !a.wide || a.detail == nil {
		return fmt.Errorf("展开 iPad 会话区后再并排查看")
	}
	snapshot := a.sessionCatalog(ref.DeviceID).Snapshot
	scope := mobileclient.WorkspaceLocation{DeviceID: ref.DeviceID, WorkspaceID: ref.WorkspaceID}
	if !slices.ContainsFunc(mobileclient.BrowseSessionCatalog(snapshot, scope, false), func(entry mobileclient.SessionCatalogEntry) bool {
		return entry.Session.ID == ref.SessionID
	}) {
		return fmt.Errorf("所属设备的会话已变化，请重新选择")
	}
	if ref.DeviceID == a.model.Snapshot.DeviceID {
		if ref.SessionID == a.model.SessionID {
			return fmt.Errorf("该会话已在左栏显示")
		}
		if err := a.closeParallelDevice(); err != nil {
			return err
		}
		a.openParallel(ref.SessionID)
		return nil
	}

	if pane := a.parallelDevice; pane != nil && pane.endpoint.DeviceID == ref.DeviceID {
		pane.selectSession(ref.SessionID)
		return nil
	}
	device := a.savedDevice(ref.DeviceID)
	if device.Endpoint.DeviceID != ref.DeviceID {
		return fmt.Errorf("此执行设备已移除")
	}
	directory, err := a.deviceDirectory(device)
	if err != nil {
		return err
	}
	if directory == a.directory {
		return fmt.Errorf("两台设备使用了同一个状态目录")
	}
	signer, err := a.identity()
	if err != nil {
		return err
	}

	// Both panes share one UIKit surface and network node. Controller,
	// history cache, and draft writers belong to their respective devices.
	pane := &mobileApp{
		model: app.NewModel(), directory: directory, rootDirectory: a.rootDirectory,
		detail: a.detail, wide: true, parallelOwner: a,
		endpoint: device.Endpoint, savedDevices: a.savedDevices, signer: signer, tailnetNode: a.tailnetNode,
		windows:    map[*mygo.Window]bool{a.detail: true},
		pageDevice: map[*mygo.Window]uint64{a.detail: 0},
		history:    make(map[string]*ui.ListState),
	}
	pane.loadDeviceFiles()
	if pane.setupError != "" {
		return fmt.Errorf("无法读取所属设备的草稿：%s", pane.setupError)
	}
	observed := snapshot.ObservedAt
	snapshot.ObservedAt = time.Time{}
	pane.model.ApplySnapshot(snapshot)
	pane.model.Snapshot.ObservedAt = observed
	pane.model.Preferences = a.model.Preferences
	pane.model.SessionID, pane.model.WorkspaceID = ref.SessionID, ref.WorkspaceID

	if err := a.closeParallelDevice(); err != nil {
		return err
	}
	a.setParallel("")
	pane.stateWriter = pane.newDraftWriter()
	pane.catalogWriter = app.NewStateWriter(filepath.Join(directory, "catalog.json"))
	a.parallelDevice = pane
	pane.connect(device.Endpoint)
	a.refresh()
	return nil
}

func (a *mobileApp) closeParallelDevice() error {
	pane := a.parallelDevice
	if pane == nil {
		return nil
	}
	if editor := pane.parallelEditor; editor != nil && editor.Doc != nil && !editor.Doc.Composing() {
		editor.Publish(pane.model, editor.target.key)
	}
	if err := pane.flushDrafts(); err != nil {
		return fmt.Errorf("右侧会话的草稿尚未保存，请重试保存后再关闭")
	}
	if pane.model.Snapshot.DeviceID == pane.endpoint.DeviceID && pane.model.Connected {
		catalog := pane.sessionCatalog(pane.endpoint.DeviceID)
		catalog.Snapshot.Operations = nil
		a.sessionCatalogs[pane.endpoint.DeviceID] = catalog
	}
	pane.suspendParallelFind()
	pane.replaceReview(nil)
	a.parallelDevice, pane.controller = nil, nil
	if pane.cancel != nil {
		pane.cancel()
	}
	pane.releaseMarkdownImages("")
	pane.stateWriter.Close()
	pane.catalogWriter.Close()
	pane.stateWriter, pane.catalogWriter = nil, nil
	return nil
}
