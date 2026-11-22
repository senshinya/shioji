package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type mobileSessionRow struct {
	session   domain.Session
	workspace domain.Workspace
	pin       app.SessionLocation
	address   string
	requests  int
	inGroup   bool
	cached    bool
	inputAt   time.Time
	dateLabel string
}

func (a *mobileApp) pinFile() string {
	return filepath.Join(a.rootDirectory, "session-pins.json")
}

func (a *mobileApp) loadSessionPins() {
	var legacy []mobileclient.LegacySessionNavigation
	for _, device := range a.savedDevices.Devices {
		directory, err := a.deviceDirectory(device)
		if err != nil {
			a.pinLoadError = err
			a.sessionListNotice = "无法读取置顶会话：" + err.Error()
			return
		}
		legacy = append(legacy, mobileclient.LegacySessionNavigation{DeviceID: device.Endpoint.DeviceID, Path: filepath.Join(directory, "state.json")})
	}
	a.sessionPins, a.pinLoadError = mobileclient.ReadSessionPins(a.pinFile(), legacy)
	if a.pinLoadError != nil {
		a.sessionListNotice = "无法读取置顶会话：" + a.pinLoadError.Error()
	}
}

func (a *mobileApp) pinnedRows() []mobileSessionRow {
	var rows []mobileSessionRow
	for _, pin := range a.sessionPins {
		catalog := a.sessionCatalog(pin.DeviceID)
		session, workspace, visible := mobileclient.FindUnarchivedSession(catalog.Snapshot, pin)
		if !visible || !a.filter.Includes(workspace) {
			continue
		}
		requests := 0
		for _, request := range catalog.Snapshot.PendingRequests {
			if request.SessionID == session.ID && pendingRequest(request) {
				requests++
			}
		}
		row := mobileSessionRow{session: session, workspace: workspace, pin: pin, address: catalog.Label(), requests: requests, cached: pin.DeviceID != a.endpoint.DeviceID && catalog.RequestedAt.IsZero() && !a.preview}
		if !slices.ContainsFunc(rows, func(existing mobileSessionRow) bool { return existing.pin == pin }) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (a *mobileApp) saveSessionPins(next []app.SessionLocation) error {
	if !a.preview {
		if a.rootDirectory == "" {
			return fmt.Errorf("设备存储不可用")
		}
		if a.pinLoadError != nil {
			return a.pinLoadError
		}
		if err := mobileclient.SaveSessionPins(a.pinFile(), next); err != nil {
			return err
		}
	}
	a.sessionPins = next
	a.sessionListNotice = ""
	a.refresh()
	return nil
}

func (a *mobileApp) setSessionPin(ref app.SessionLocation, pinned bool) error {
	if a.isSessionPinned(ref) == pinned {
		return nil
	}
	next := slices.Clone(a.sessionPins)
	if pinned {
		if _, _, ok := mobileclient.FindUnarchivedSession(a.sessionCatalog(ref.DeviceID).Snapshot, ref); !ok {
			return fmt.Errorf("会话归属已改变或已归档")
		}
		next = append(next, ref)
	} else {
		next = slices.DeleteFunc(next, func(pin app.SessionLocation) bool { return pin == ref })
	}
	return a.saveSessionPins(next)
}

func (a *mobileApp) pinCommands(ref app.SessionLocation) []contextMenuCommand {
	rows := a.pinnedRows()
	index := slices.IndexFunc(rows, func(row mobileSessionRow) bool { return row.pin == ref })
	var commands []contextMenuCommand
	for _, move := range []struct {
		id, title, symbol string
		offset            int
	}{{"moveUp", "上移", "arrow.up", -1}, {"moveDown", "下移", "arrow.down", 1}} {
		if other := index + move.offset; index >= 0 && other >= 0 && other < len(rows) {
			commands = append(commands, contextMenuCommand{ID: move.id, Title: move.title, Symbol: move.symbol, run: func() {
				next := slices.Clone(a.sessionPins)
				i, j := slices.Index(next, ref), slices.Index(next, rows[other].pin)
				if i >= 0 && j >= 0 && (j-i)*move.offset > 0 {
					next[i], next[j] = next[j], next[i]
					a.pinFeedback(a.saveSessionPins(next))
				}
			}})
		}
	}
	return append(commands, contextMenuCommand{ID: "unpin", Title: "取消置顶", Symbol: "pin.slash",
		run: func() { a.pinFeedback(a.setSessionPin(ref, false)) }})
}

func (a *mobileApp) pinFeedback(err error) {
	if err != nil {
		a.sessionListNotice = "无法保存置顶会话：" + err.Error()
		a.home.Invalidate()
	}
}

func (a *mobileApp) isSessionPinned(ref app.SessionLocation) bool {
	return slices.Contains(a.sessionPins, ref)
}

func (row mobileSessionRow) metadata() string {
	parts := []string{row.session.GitBranch}
	if !row.inGroup {
		parts = []string{row.workspace.Name, row.session.GitBranch, row.address}
	}
	if row.cached {
		parts = append(parts, "上次读取")
	}
	return strings.Join(slices.DeleteFunc(parts, func(part string) bool { return part == "" }), " · ")
}
