package app

import (
	"path/filepath"
	"reflect"
	"shioji/internal/domain"
	"testing"
)

func navigationModel() *Model {
	m := NewModel()
	m.Snapshot.Workspaces = []domain.Workspace{{ID: "wa", DeviceID: "A", Name: "Tree"}, {ID: "wb", DeviceID: "B", Name: "Shioji"}}
	m.Snapshot.Sessions = []domain.Session{{ID: "a1", WorkspaceID: "wa"}, {ID: "a2", WorkspaceID: "wa"}, {ID: "a3", WorkspaceID: "wa"}, {ID: "b1", WorkspaceID: "wb"}, {ID: "b2", WorkspaceID: "wb"}}
	m.Drafts["home"], m.Drafts["session:a1"] = "home draft", "session draft"
	m.SessionID, m.WorkspaceID = "a1", "wa"
	m.ObserveNavigation()
	return m
}
func sessionIDs(sessions []domain.Session) []string {
	ids := []string{}
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids
}
func TestGlobalPinsMixDevicesAndUnpinRestoresOrdinaryPosition(t *testing.T) {
	m := navigationModel()
	a := SessionLocation{DeviceID: "A", WorkspaceID: "wa", SessionID: "a2"}
	b := SessionLocation{DeviceID: "B", WorkspaceID: "wb", SessionID: "b1"}
	for _, ref := range []SessionLocation{a, b} {
		if err := m.SetSessionPinned(ref, true); err != nil {
			t.Fatal(err)
		}
	}
	if got := sessionIDs(m.NavigationPins()); !reflect.DeepEqual(got, []string{"a2", "b1"}) {
		t.Fatal("global order", got)
	}
	if got := sessionIDs(m.NavigationSessions(m.Snapshot.Workspaces[0])); !reflect.DeepEqual(got, []string{"a1", "a3"}) {
		t.Fatal("pin duplicated in workspace", got)
	}
	if err := m.SetSessionPinned(a, false); err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(m.NavigationSessions(m.Snapshot.Workspaces[0])); !reflect.DeepEqual(got, []string{"a1", "a2", "a3"}) {
		t.Fatal("ordinary position lost", got)
	}
	if m.SessionID != "a1" || m.Drafts["home"] != "home draft" || m.Drafts["session:a1"] != "session draft" {
		t.Fatal("navigation changed active work")
	}
}
func TestNavigationMovesCannotChangeWorkspaceAndNewArrivalPrecedesManualOrder(t *testing.T) {
	m := navigationModel()
	a := SessionLocation{DeviceID: "A", WorkspaceID: "wa", SessionID: "a3"}
	if err := m.MoveNavigationSession(a, SessionLocation{DeviceID: "A", WorkspaceID: "wa", SessionID: "a1"}, false); err != nil {
		t.Fatal(err)
	}
	before := m.Navigation.Clone()
	if err := m.MoveNavigationSession(a, SessionLocation{DeviceID: "B", WorkspaceID: "wb", SessionID: "b1"}, false); err == nil {
		t.Fatal("cross-workspace drop accepted")
	}
	if !reflect.DeepEqual(before, m.Navigation) {
		t.Fatal("invalid drop changed layout")
	}
	if err := m.MoveNavigationSession(a, SessionLocation{DeviceID: "B", WorkspaceID: "wb"}, false); err == nil {
		t.Fatal("empty foreign workspace accepted the drop")
	}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "new", WorkspaceID: "wa"})
	m.ObserveNavigation()
	if got := sessionIDs(m.NavigationSessions(m.Snapshot.Workspaces[0])); !reflect.DeepEqual(got, []string{"new", "a3", "a1", "a2"}) {
		t.Fatal("arrival destroyed manual order", got)
	}
	m.Snapshot.Sessions[0].Status = "running"
	m.Snapshot.Sessions[0].Title = "Updated title"
	m.ObserveNavigation()
	if got := sessionIDs(m.NavigationSessions(m.Snapshot.Workspaces[0])); !reflect.DeepEqual(got, []string{"new", "a3", "a1", "a2"}) {
		t.Fatal("activity reordered navigation", got)
	}
}
func TestNavigationPreferencesSurviveRestartOfflineAndIdentityMismatch(t *testing.T) {
	m := navigationModel()
	b := SessionLocation{DeviceID: "B", WorkspaceID: "wb", SessionID: "b1"}
	if err := m.SetSessionPinned(b, true); err != nil {
		t.Fatal(err)
	}
	if err := m.MoveNavigationWorkspace("B", "wb", "A", "wa"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Navigation, restored.Navigation) {
		t.Fatal("layout not persisted", restored.Navigation)
	}
	restored.Snapshot.Workspaces = m.Snapshot.Workspaces[:1]
	restored.Snapshot.Sessions = m.Snapshot.Sessions[:3]
	restored.ObserveNavigation()
	if len(restored.Navigation.Pinned) != 1 {
		t.Fatal("missing device cleared pin")
	}
	restored.Snapshot = m.Snapshot
	restored.ObserveNavigation()
	if pins := sessionIDs(restored.NavigationPins()); !reflect.DeepEqual(pins, []string{"b1"}) {
		t.Fatal("offline pin did not return", pins)
	}
	if got := restored.NavigationWorkspaces(); got[0].ID != "wb" {
		t.Fatal("workspace order not restored", got)
	}
	restored.Snapshot.Workspaces[1].DeviceID = "replacement"
	if len(restored.NavigationPins()) != 0 {
		t.Fatal("pin crossed device identity")
	}
	if err := restored.SetSessionPinned(b, false); err == nil {
		t.Fatal("stale identity changed replacement")
	}
}
