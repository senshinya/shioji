package mobileclient

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestWorkspaceOrderPersistsAcrossDevicesAndPromotesNewInput(t *testing.T) {
	a, b := WorkspaceLocation{"A", "shared"}, WorkspaceLocation{"B", "shared"}
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	catalog := func(scope WorkspaceLocation, ids ...string) domain.Snapshot {
		out := domain.Snapshot{DeviceID: scope.DeviceID, Workspaces: []domain.Workspace{{ID: scope.WorkspaceID, DeviceID: scope.DeviceID}},
			SessionInputTimes: map[string]time.Time{}}
		for _, id := range ids {
			out.Sessions = append(out.Sessions, domain.Session{ID: id, WorkspaceID: scope.WorkspaceID})
			out.SessionInputTimes[id] = start
		}
		return out
	}
	local, remote := catalog(a, "one", "pinned", "two"), catalog(b, "one", "foreign")
	var order NavigationOrder
	order, _ = order.Observe(local)
	order, _ = order.Observe(remote)
	order, err := order.ReorderWorkspaces([]WorkspaceLocation{a, b}, []WorkspaceLocation{b, a})
	if err != nil {
		t.Fatal(err)
	}
	order, err = order.ReorderSessions(a, []string{"one", "two"}, []string{"two", "one"})
	if err != nil || !slices.Equal(order.SessionIDs(a), []string{"two", "pinned", "one"}) {
		t.Fatal("reordering visible sessions changed the hidden position", order, err)
	}
	data, err := json.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	var restored NavigationOrder
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	local.Sessions[0].Status = domain.ExecutionRunning
	if _, changed := restored.Observe(local); changed {
		t.Fatal("reply status alone changed the stored order")
	}
	local.SessionInputTimes["one"] = start.Add(time.Minute)
	restored, changed := restored.Observe(local)
	if !changed || !slices.Equal(restored.SessionIDs(a), []string{"one", "two", "pinned"}) ||
		!slices.Equal(restored.SessionIDs(b), []string{"one", "foreign"}) ||
		!slices.Equal(restored.Locations(), []WorkspaceLocation{b, a}) {
		t.Fatal("new input did not promote only its own session", restored)
	}
	local.SessionInputTimes["one"] = start
	if _, changed := restored.Observe(local); changed {
		t.Fatal("a replayed catalog displaced the latest input")
	}
	if _, err := restored.ReorderSessions(a, []string{"two", "one"}, []string{"one", "two"}); err == nil {
		t.Fatal("an editor with stale ordering could overwrite newer activity")
	}
	if _, err := restored.ReorderSessions(a, []string{"one", "two"}, []string{"one", "foreign"}); err == nil {
		t.Fatal("session reordering allowed a foreign identity")
	}
}

func TestReorderVisiblePreservesMissingSlotsAndRejectsDuplicateRows(t *testing.T) {
	full := []string{"a", "temporarily unavailable", "b", "c"}
	next, err := ReorderVisible(full, []string{"a", "b", "c"}, []string{"b", "c", "a"})
	if err != nil || !slices.Equal(next, []string{"b", "temporarily unavailable", "c", "a"}) ||
		!slices.Equal(full, []string{"a", "temporarily unavailable", "b", "c"}) {
		t.Fatal(next, full, err)
	}
	if _, err := ReorderVisible(full, []string{"a", "b", "c"}, []string{"a", "a", "c"}); err == nil {
		t.Fatal("a malformed response could duplicate an item")
	}
}
