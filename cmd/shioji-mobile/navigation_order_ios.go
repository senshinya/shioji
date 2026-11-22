package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

type navigationOrderRow struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

func (a *mobileApp) orderFile() string {
	return filepath.Join(a.rootDirectory, "navigation-order.json")
}

func (a *mobileApp) loadNavigationOrder() {
	raw, err := os.ReadFile(a.orderFile())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		err = json.Unmarshal(raw, &a.navigationOrder)
	}
	if err != nil {
		a.navigationOrder = nil
		a.navigationOrderLoadError = err
		a.sessionListNotice = "无法读取列表顺序"
	}
}

func (a *mobileApp) saveNavigationOrder(order mobileclient.NavigationOrder) error {
	if !a.preview {
		if a.rootDirectory == "" {
			return errors.New("设备存储不可用")
		}
		if a.navigationOrderLoadError != nil {
			return a.navigationOrderLoadError
		}
		data, err := json.Marshal(order)
		if err != nil {
			return err
		}
		if err := app.WriteState(a.orderFile(), data); err != nil {
			return err
		}
	}
	a.navigationOrder = order
	return nil
}

func (a *mobileApp) observeNavigationOrder(catalog domain.Snapshot) {
	if a.preview || a.rootDirectory == "" || a.navigationOrderLoadError != nil {
		return
	}
	if order, changed := a.navigationOrder.Observe(catalog); changed {
		if err := a.saveNavigationOrder(order); err != nil {
			a.sessionListNotice = "无法保存列表顺序"
		}
	}
}

// Seed the displayed rows even in a preview or while using a cached catalog.
func (a *mobileApp) currentNavigationOrder() mobileclient.NavigationOrder {
	order := a.navigationOrder
	for _, catalog := range a.browseCatalogs() {
		order, _ = order.Observe(catalog.Snapshot)
	}
	return order
}

func (a *mobileApp) orderedWorkspaces(catalogs []mobileclient.DeviceCatalog) []sessionWorkspaceChoice {
	choices := sessionWorkspaceChoices(catalogs)
	positions := navigationRanks(a.navigationOrder.Locations())
	slices.SortStableFunc(choices, func(a, b sessionWorkspaceChoice) int {
		return navigationRank(positions, a.location) - navigationRank(positions, b.location)
	})
	return choices
}

func navigationRanks[T comparable](items []T) map[T]int {
	rank := make(map[T]int, len(items))
	for i, item := range items {
		rank[item] = i
	}
	return rank
}

func navigationRank[T comparable](ranks map[T]int, item T) int {
	if rank, ok := ranks[item]; ok {
		return rank
	}
	return len(ranks)
}

func (a *mobileApp) arrangeWorkspaces() {
	workspaces := a.orderedWorkspaces(a.browseCatalogs())
	var rows []navigationOrderRow
	var before []mobileclient.WorkspaceLocation
	for _, workspace := range workspaces {
		rows = append(rows, navigationOrderRow{Title: workspace.name, Detail: workspace.device})
		before = append(before, workspace.location)
	}
	a.presentNavigationOrdering("排列工作区", rows, func(indices []int) error {
		catalogs := a.browseCatalogs()
		valid := make(map[mobileclient.WorkspaceLocation]bool)
		for _, workspace := range sessionWorkspaceChoices(catalogs) {
			valid[workspace.location] = true
		}
		after := make([]mobileclient.WorkspaceLocation, len(indices))
		for i, index := range indices {
			after[i] = before[index]
			if !valid[after[i]] {
				return errors.New("工作区已变化，请重新排列")
			}
		}
		order, err := a.currentNavigationOrder().ReorderWorkspaces(before, after)
		if err != nil {
			return err
		}
		if err := a.saveNavigationOrder(order); err != nil {
			return err
		}
		a.setSessionGrouping(groupByWorkspace, true)
		return nil
	})
}

func (a *mobileApp) arrangeableSessions(scope mobileclient.WorkspaceLocation) []mobileclient.SessionCatalogEntry {
	catalog := a.sessionCatalog(scope.DeviceID)
	sessions := mobileclient.BrowseSessionCatalog(catalog.Snapshot, scope, false)
	return slices.DeleteFunc(sessions, func(entry mobileclient.SessionCatalogEntry) bool {
		ref := app.SessionLocation{DeviceID: scope.DeviceID, WorkspaceID: scope.WorkspaceID, SessionID: entry.Session.ID}
		return a.isSessionPinned(ref)
	})
}

func (a *mobileApp) arrangeSessions(scope mobileclient.WorkspaceLocation) {
	sessions := a.arrangeableSessions(scope)
	ranks := navigationRanks(a.currentNavigationOrder().SessionIDs(scope))
	slices.SortStableFunc(sessions, func(a, b mobileclient.SessionCatalogEntry) int {
		return navigationRank(ranks, a.Session.ID) - navigationRank(ranks, b.Session.ID)
	})
	var rows []navigationOrderRow
	var before []string
	for _, entry := range sessions {
		rows = append(rows, navigationOrderRow{Title: entry.Session.Title})
		before = append(before, entry.Session.ID)
	}
	a.presentNavigationOrdering("排列会话", rows, func(indices []int) error {
		after := make([]string, len(indices))
		current := a.sessionCatalog(scope.DeviceID).Snapshot
		for i, index := range indices {
			after[i] = before[index]
			ref := app.SessionLocation{DeviceID: scope.DeviceID, WorkspaceID: scope.WorkspaceID, SessionID: after[i]}
			if _, _, ok := mobileclient.FindUnarchivedSession(current, ref); !ok || a.isSessionPinned(ref) {
				return errors.New("会话已变化，请重新排列")
			}
		}
		order, err := a.currentNavigationOrder().ReorderSessions(scope, before, after)
		if err != nil {
			return err
		}
		if err := a.saveNavigationOrder(order); err != nil {
			return err
		}
		if a.filter == scope {
			a.setSessionGrouping(groupByManual, false)
		}
		return nil
	})
}

func (a *mobileApp) arrangePins() {
	var rows []navigationOrderRow
	var before []app.SessionLocation
	for _, entry := range a.pinnedRows() {
		rows = append(rows, navigationOrderRow{
			Title: entry.session.Title, Detail: strings.Trim(entry.workspace.Name+" · "+entry.address, " ·"),
		})
		before = append(before, entry.pin)
	}
	a.presentNavigationOrdering("排列置顶会话", rows, func(indices []int) error {
		after := make([]app.SessionLocation, len(indices))
		for i, index := range indices {
			after[i] = before[index]
			if _, _, ok := mobileclient.FindUnarchivedSession(a.sessionCatalog(after[i].DeviceID).Snapshot, after[i]); !ok {
				return errors.New("置顶会话已变化，请重新排列")
			}
		}
		pins, err := mobileclient.ReorderVisible(a.sessionPins, before, after)
		if err != nil {
			return err
		}
		return a.saveSessionPins(pins)
	})
}

func (a *mobileApp) presentNavigationOrdering(title string, rows []navigationOrderRow, apply func([]int) error) {
	if a.navigationOrderingOpen || len(rows) < 2 {
		return
	}
	a.navigationOrderingOpen = true
	a.cancelSessionListOpening()
	args := map[string]any{"home": a.home.NativeHandle(), "title": title, "rows": rows}
	go func() {
		var indices []int
		err := uikit.Call(context.Background(), "reorderNavigation", args, &indices)
		mygo.RunOnMain(func() {
			a.navigationOrderingOpen = false
			if err == nil && len(indices) == 0 {
				return
			}
			if err == nil {
				if len(indices) != len(rows) || slices.ContainsFunc(indices, func(i int) bool { return i < 0 || i >= len(rows) }) {
					err = errors.New("列表顺序已变化，请重新排列")
				} else {
					a.sessionListNotice = ""
					err = apply(indices)
				}
			}
			if err != nil {
				a.sessionListNotice = "无法保存列表顺序：" + err.Error()
			}
			a.refresh()
		})
	}()
}
