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

func TestUserSubmissionPromotesOnlyItsWorkspaceWithoutLoadingHistory(t *testing.T) {
	m := navigationModel()
	pin := SessionLocation{DeviceID: "B", WorkspaceID: "wb", SessionID: "b1"}
	m.SetSessionPinned(pin, true)
	event := domain.Event{Sequence: 1, Type: "submission", SessionID: "a3", Item: &domain.Item{ID: "new-message", Kind: "user", Text: "new question"}}
	if err := m.ApplyEvent(event); err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(m.NavigationSessions(m.Snapshot.Workspaces[0])); !reflect.DeepEqual(got, []string{"a3", "a1", "a2"}) {
		t.Fatal("new user message did not promote its session", got)
	}
	if got := sessionIDs(m.NavigationPins()); !reflect.DeepEqual(got, []string{"b1"}) || m.NavigationWorkspaces()[0].ID != "wa" {
		t.Fatal("new message changed pins or workspace order", got)
	}
	if m.SessionID != "a1" || m.Drafts["session:a1"] != "session draft" || m.Drafts["home"] != "home draft" {
		t.Fatal("promotion changed selection or drafts")
	}
	// A manual move remains until the next genuinely new user submission.
	m.MoveNavigationSession(SessionLocation{"A", "wa", "a1"}, SessionLocation{"A", "wa", "a3"}, false)
	before := m.Navigation.Clone()
	m.ApplyEvent(event) // Replayed sequence.
	for _, item := range []domain.Item{{ID: "new-message", Kind: "user", Status: "sent"}, {ID: "reply", Kind: "assistant"}} {
		if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, Type: "item.upsert", SessionID: "a3", Item: &item}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, Type: "submission", SessionID: "a3", Operation: &domain.Operation{ID: "waiting-input", SessionID: "a3", State: "accepted"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, Type: "queue", SessionID: "a3", Queue: &domain.QueueState{SessionID: "a3", Revision: 1, Items: []domain.QueueItem{{ID: "waiting-input"}}}}); err != nil {
		t.Fatal(err)
	}
	m.ApplySnapshot(m.Snapshot)
	m.Details["a3"] = domain.SessionDetail{Items: []domain.Item{*event.Item}}
	m.ObserveNavigation()
	if !reflect.DeepEqual(before, m.Navigation) {
		t.Fatal("reply, replay or history hydration reordered sessions")
	}
	path := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil || !reflect.DeepEqual(restored.Navigation, m.Navigation) {
		t.Fatal("promoted order was not persisted", err)
	}
}

func TestBackgroundDeviceUserSubmissionPromotesItsOwnSession(t *testing.T) {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B"}}
	for _, device := range []string{"A", "B"} {
		key := "local"
		if device == "B" {
			key = "air"
		}
		m.ApplyDeviceUpdate(key, func(p *Model) {
			p.ApplySnapshot(domain.Snapshot{DeviceID: device, Generation: device, Workspaces: []domain.Workspace{{ID: "w" + device, DeviceID: device}}, Sessions: []domain.Session{{ID: device + "1", WorkspaceID: "w" + device}, {ID: device + "2", WorkspaceID: "w" + device}}})
		})
	}
	m.WorkspaceID, m.SessionID = "wA", "A1"
	m.ObserveNavigation()
	m.ApplyDeviceUpdate("air", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "B2", Item: &domain.Item{ID: "remote-input", Kind: "user"}}); err != nil {
			t.Fatal(err)
		}
	})
	if got := sessionIDs(m.NavigationSessions(m.Snapshot.Workspaces[1])); !reflect.DeepEqual(got, []string{"B2", "B1"}) {
		t.Fatal("background device promotion was lost at the projection boundary", got)
	}
	if m.SessionID != "A1" || m.WorkspaceID != "wA" {
		t.Fatal("background promotion navigated away from the current conversation")
	}
}

func TestUserSubmissionKeepsGlobalPinOrderAndPromotesTheUnpinnedPosition(t *testing.T) {
	m := navigationModel()
	ref := SessionLocation{"A", "wa", "a3"}
	m.SetSessionPinned(SessionLocation{"B", "wb", "b1"}, true)
	m.SetSessionPinned(ref, true)
	pins := m.Navigation.Clone().Pinned
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "a3", Item: &domain.Item{ID: "pinned-input", Kind: "user"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pins, m.Navigation.Pinned) {
		t.Fatal("user activity reordered or removed global pins")
	}
	m.SetSessionPinned(ref, false)
	if got := sessionIDs(m.NavigationSessions(m.Snapshot.Workspaces[0])); !reflect.DeepEqual(got, []string{"a3", "a1", "a2"}) {
		t.Fatal("unpin lost the latest user-message position", got)
	}
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
