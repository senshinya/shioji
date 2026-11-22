package main

import (
	"context"

	"github.com/egoist/mygo"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type attentionShortcutScan struct {
	cancel context.CancelFunc
}

type attentionShortcutFeedback struct {
	source  *mygo.Window
	message string
}

func (a *mobileApp) cancelAttentionShortcut() {
	if scan := a.attentionShortcut; scan != nil {
		a.attentionShortcut = nil
		scan.cancel()
	}
}

func (a *mobileApp) showAttentionShortcutFeedback(source *mygo.Window, message string) {
	a.attentionFeedback = attentionShortcutFeedback{source, message}
	source.Invalidate()
}

// Keep the same session order as the Mac shortcut, with requests ahead of replies.
func (entries attentionEntries) firstRequest(catalog domain.Snapshot) attentionTarget {
	bySession := make(map[string]mobileclient.DeviceRequest, len(entries.requests))
	for _, entry := range entries.requests {
		if _, exists := bySession[entry.Request.SessionID]; !exists {
			bySession[entry.Request.SessionID] = entry
		}
	}
	for _, session := range catalog.Sessions {
		entry, exists := bySession[session.ID]
		if !exists || session.Archived {
			continue
		}
		for _, workspace := range catalog.Workspaces {
			if workspace.ID == entry.WorkspaceID && workspace.DeviceID == entry.DeviceID && !workspace.Archived {
				return requestAttentionTarget(entry)
			}
		}
	}
	return attentionTarget{}
}

func (a *mobileApp) openNextAttention(source *mygo.Window) {
	if a.attentionShortcut != nil || a.sessionListOpening != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	scan := &attentionShortcutScan{cancel: cancel}
	a.attentionShortcut = scan
	m := a.model
	finish := func(target attentionTarget, partial bool) {
		if a.attentionShortcut != scan {
			return
		}
		if !a.pageCurrent(source) || !mygo.App.IsActive() {
			a.cancelAttentionShortcut()
			return
		}
		var current bool
		a.callUIKitResult("attentionSource", map[string]any{
			"window": source.NativeHandle(), "navigate": target.session != "",
		}, &current, func(err error) {
			if a.attentionShortcut != scan {
				return
			}
			a.cancelAttentionShortcut()
			if err != nil || !current || a.model != m || !a.pageCurrent(source) ||
				a.draftPage != nil && a.draftPage != source {
				return
			}
			if target.session == "" {
				message := "当前没有待处理事项"
				if partial {
					message = "部分设备暂不可用，未能核对全部待处理事项"
				}
				a.showAttentionShortcutFeedback(source, message)
				return
			}
			next := sessionListTarget{
				location:  app.SessionLocation{DeviceID: target.device, WorkspaceID: target.workspace, SessionID: target.session},
				attention: &target,
			}
			if draft := a.draftPage; draft != nil {
				draft.OnClosed(func() {
					if a.model == m && a.draftPage == nil {
						a.openSessionListTarget(next)
					}
				})
				if isTablet() {
					a.callUIKit("dismissForm", map[string]uintptr{"window": draft.NativeHandle()}, nil)
				} else {
					draft.Pop()
				}
				return
			}
			a.openSessionListTarget(next)
		})
	}

	var devices []savedMobileDevice
	for _, device := range a.savedDevices.Devices {
		if !a.preview && device.Endpoint.DeviceID != a.endpoint.DeviceID {
			devices = append(devices, device)
		}
	}
	if m.Connected {
		if request := a.currentAttentionEntries().firstRequest(m.Snapshot); request.session != "" {
			finish(request, false)
			return
		}
	}
	resolve := func(catalogs []mobileclient.DeviceCatalog, partial bool) {
		var firstReply attentionTarget
		if m.Connected {
			local := a.currentAttentionEntries()
			if request := local.firstRequest(m.Snapshot); request.session != "" {
				finish(request, false)
				return
			}
			if len(local.replies) != 0 {
				firstReply = replyAttentionTarget(local.replies[0])
			}
		}
		for _, catalog := range catalogs {
			if catalog.Snapshot.DeviceID == "" {
				partial = true
				continue
			}
			entries := a.remoteAttentionEntries(catalog)
			if request := entries.firstRequest(catalog.Snapshot); request.session != "" {
				finish(request, false)
				return
			}
			if firstReply.session == "" && len(entries.replies) != 0 {
				firstReply = replyAttentionTarget(entries.replies[0])
			}
		}
		finish(firstReply, partial || !m.Connected && a.endpoint.DeviceID != "")
	}
	if len(devices) == 0 {
		resolve(nil, false)
		return
	}
	signer, err := a.identity()
	if err != nil {
		resolve(nil, true)
		return
	}
	a.showAttentionShortcutFeedback(source, "正在检查其他设备…")
	route := a.routeDial()
	type readResult struct {
		index int
		value mobileclient.DeviceCatalog
	}
	results := make(chan readResult, len(devices))
	for index, device := range devices {
		go func() {
			read, stop := context.WithTimeout(ctx, sessionCatalogTimeout)
			defer stop()
			catalog, err := mobileclient.ReadDeviceCatalog(read, device, signer, route)
			if err != nil || catalog.Snapshot.DeviceID != device.Endpoint.DeviceID {
				catalog = mobileclient.DeviceCatalog{}
			}
			results <- readResult{index, catalog}
		}()
	}
	go func() {
		catalogs := make([]mobileclient.DeviceCatalog, len(devices))
		for range devices {
			select {
			case <-ctx.Done():
				return
			case result := <-results:
				catalogs[result.index] = result.value
			}
		}
		mygo.RunOnMain(func() {
			if a.attentionShortcut == scan && a.model == m {
				resolve(catalogs, false)
			}
		})
	}()
}
