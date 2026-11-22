package mobileclient

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestSessionPinsMigrateOnceInDeviceOrder(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "session-pins.json")
	a := app.SessionLocation{DeviceID: "a", WorkspaceID: "w", SessionID: "s"}
	b := app.SessionLocation{DeviceID: "b", WorkspaceID: "w", SessionID: "s"}
	stateA, stateB := filepath.Join(root, "a.json"), filepath.Join(root, "b.json")
	if err := os.WriteFile(stateA, []byte(`{"navigation":{"pinned":[{"deviceId":"b","workspaceId":"w","sessionId":"s"},{"deviceId":"a","workspaceId":"w","sessionId":"s"},{"deviceId":"a","workspaceId":"w","sessionId":"s"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateB, []byte(`{"navigation":{"pinned":[{"deviceId":"b","workspaceId":"w","sessionId":"s"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := []LegacySessionNavigation{{DeviceID: "b", Path: stateB}, {DeviceID: "a", Path: stateA}}
	pins, err := ReadSessionPins(file, legacy)
	if err != nil || !slices.Equal(pins, []app.SessionLocation{b, a}) {
		t.Fatalf("migrated pins = %v, %v", pins, err)
	}
	if err := SaveSessionPins(file, nil); err != nil {
		t.Fatal(err)
	}
	pins, err = ReadSessionPins(file, legacy)
	if err != nil || len(pins) != 0 {
		t.Fatalf("unpinning resurrected legacy pins: %v, %v", pins, err)
	}
}

func TestFindUnarchivedSessionChecksFullOwnership(t *testing.T) {
	ref := app.SessionLocation{DeviceID: "a", WorkspaceID: "w", SessionID: "s"}
	snapshot := domain.Snapshot{
		DeviceID:   "a",
		Workspaces: []domain.Workspace{{ID: "w", DeviceID: "a"}},
		Sessions:   []domain.Session{{ID: "s", WorkspaceID: "w", Context: domain.LaunchContext{DeviceID: "a"}}},
	}
	if _, _, ok := FindUnarchivedSession(snapshot, ref); !ok {
		t.Fatal("valid session was hidden")
	}
	other := ref
	other.DeviceID = "b"
	if _, _, ok := FindUnarchivedSession(snapshot, other); ok {
		t.Fatal("matched colliding IDs on a different device")
	}
	snapshot.Sessions[0].Context.DeviceID = "b"
	if _, _, ok := FindUnarchivedSession(snapshot, ref); ok {
		t.Fatal("matched a session with the wrong execution device")
	}
	snapshot.Sessions[0].Context.DeviceID = "a"
	snapshot.Workspaces[0].Archived = true
	if _, _, ok := FindUnarchivedSession(snapshot, ref); ok {
		t.Fatal("matched a session in an archived workspace")
	}
	snapshot.Workspaces[0].Archived = false
	snapshot.Sessions[0].Archived = true
	if _, _, ok := FindUnarchivedSession(snapshot, ref); ok {
		t.Fatal("matched an archived session")
	}
}
