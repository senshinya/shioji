package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func navigationFixture() *app.Model {
	m := layoutModel()
	m.LocalDeviceID = "A"
	m.Snapshot.DeviceID = "A"
	m.Snapshot.Workspaces[0].DeviceID = "A"
	m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: "remote", DeviceID: "B", Name: "Remote workspace"})
	m.Connections = []app.DeviceConnection{{ID: "air", DeviceID: "B", Name: "MacBook Air"}}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "other", WorkspaceID: "w", Title: "Other session", Status: "completed"}, domain.Session{ID: "remote-session", WorkspaceID: "remote", Title: "Remote running", Status: "running"})
	m.Drafts["home"], m.Drafts["session:s"] = "home draft", "ordinary draft"
	return m
}
func TestGlobalPinMenuRetainsSelectionDraftAndWorkspaceFold(t *testing.T) {
	m := navigationFixture()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	if err := ui.RightClick("Remote running"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("置顶会话"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if m.SessionID != "s" || m.Drafts["home"] != "home draft" || m.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("pin navigated or changed drafts")
	}
	if _, ok := ui.Find("置顶"); !ok {
		t.Fatal("missing global pinned section")
	}
	pinned, _ := ui.Find("Remote running")
	workspace, _ := ui.Find("Remote workspace")
	if pinned.Y >= workspace.Y {
		t.Fatal("pin remains inside its workspace", pinned, workspace)
	}
	ui.Click("Remote workspace")
	ui.Advance(time.Second)
	if _, ok := ui.Find("Remote running"); !ok {
		t.Fatal("workspace folding hid global pin")
	}
	count := 0
	for _, text := range ui.Texts() {
		if text == "Remote running" {
			count++
		}
	}
	// One button label and one visible text label, both belonging to the same row.
	if count != 2 {
		t.Fatal("pinned row duplicated", count)
	}
	if err := ui.RightClick("Remote running"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("取消置顶"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText("置顶") || ui.HasText("Remote running") {
		t.Fatal("last pin did not return to collapsed workspace")
	}
}
func TestNavigationMenuAndKeyboardUseGlobalPinOrderAcrossDevices(t *testing.T) {
	m := navigationFixture()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	for _, title := range []string{"Layout", "Remote running"} {
		ui.RightClick(title)
		if err := ui.ChooseMenuItem("置顶会话"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
	}
	ui.RightClick("Remote running")
	if err := ui.ChooseMenuItem("上移一位"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if got := m.Navigation.Pinned; len(got) != 2 || got[0].DeviceID != "B" {
		t.Fatal("pin reorder remained workspace-scoped", got)
	}
	rowBox, _ := ui.Find("Remote running")
	ui.Move(rowBox.X+rowBox.W/2, rowBox.Y+rowBox.H/2)
	if err := ui.Click("Remote running"); err != nil {
		t.Fatal(err)
	}
	ui.Key(n.Alt, n.KeyDown)
	ui.Advance(time.Second)
	if got := m.Navigation.Pinned; got[0].SessionID != "s" {
		t.Fatal("keyboard did not reorder global section", got)
	}
}
func TestPinMovementHasIntermediatePositionWithoutReplayingText(t *testing.T) {
	m := navigationFixture()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	before, _ := ui.Find("Remote running")
	ui.RightClick("Remote running")
	if err := ui.ChooseMenuItem("置顶会话"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(65 * time.Millisecond)
	middle, _ := ui.Find("Remote running")
	ui.Advance(300 * time.Millisecond)
	after, _ := ui.Find("Remote running")
	if !(after.Y < middle.Y && middle.Y < before.Y) {
		t.Fatal("pin jumped instead of moving", before, middle, after)
	}
	if ui.HasText("无法保存排列") {
		t.Fatal("in-memory navigation incorrectly requires a writer")
	}
}
func TestNavigationWriteFailureRestoresLayoutAndKeepsDrafts(t *testing.T) {
	m := navigationFixture()
	m.ObserveNavigation()
	before := m.Navigation.Clone()
	root := t.TempDir()
	blocked := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	v := New(m, nil, filepath.Join(blocked, "client.json"))
	defer v.Saver.Close()
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.RightClick("Remote running")
	if err := ui.ChooseMenuItem("置顶会话"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		time.Sleep(time.Millisecond)
		ui.Advance(20 * time.Millisecond)
		if reflect.DeepEqual(m.Navigation, before) && strings.Contains(m.Error, "排列") {
			break
		}
	}
	if !reflect.DeepEqual(m.Navigation, before) || !strings.Contains(m.Error, "排列") {
		t.Fatal("failed save left optimistic order", m.Navigation, m.Error)
	}
	if m.Drafts["home"] != "home draft" || m.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("navigation rollback restored unrelated draft state")
	}
}

func TestNavigationMoveSkipsArchivedNeighbors(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		t.Run(direction, func(t *testing.T) {
			m := navigationFixture()
			m.Snapshot.Sessions = append(m.Snapshot.Sessions[:1], append([]domain.Session{{ID: "archived", WorkspaceID: "w", Title: "Archived", Status: "completed", Archived: true}}, m.Snapshot.Sessions[1:]...)...)
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1280, 800)
			ui.Advance(time.Second)
			if ui.HasText("Archived") {
				t.Fatal("fixture archive should be hidden")
			}
			if direction == "up" {
				if err := ui.RightClick("Other session"); err != nil {
					t.Fatal(err)
				}
				if err := ui.ChooseMenuItem("上移一位"); err != nil {
					t.Fatal(err)
				}
			} else {
				row, _ := ui.Find("Layout")
				ui.Move(row.X+60, row.Y+14)
				if err := ui.Click("Layout"); err != nil {
					t.Fatal(err)
				}
				ui.Key(n.Alt, n.KeyDown)
			}
			ui.Advance(300 * time.Millisecond)
			a, _ := ui.Find("Other session")
			b, _ := ui.Find("Layout")
			if a.Y >= b.Y {
				t.Fatal("hidden archive consumed a visible movement", direction, m.Navigation, m.Error)
			}
			if !m.Snapshot.Sessions[1].Archived {
				t.Fatal("reorder changed archive state")
			}
		})
	}
}

func TestNavigationSaveFailureRestoresLatestSuccessfulUnobservedWrite(t *testing.T) {
	m := navigationFixture()
	m.ObserveNavigation()
	dir := filepath.Join(t.TempDir(), "state")
	v := New(m, nil, filepath.Join(dir, "client.json"))
	defer v.Saver.Close()
	v.navigationChange(func(m *app.Model) error {
		return m.SetSessionPinned(app.SessionLocation{DeviceID: "A", WorkspaceID: "w", SessionID: "s"}, true)
	})
	confirmed := m.Navigation.Clone()
	data, err := m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Saver.Flush(data); err != nil {
		t.Fatal(err)
	}
	// The UI has not polled the successful first write yet.
	v.navigationChange(func(m *app.Model) error {
		return m.SetSessionPinned(app.SessionLocation{DeviceID: "B", WorkspaceID: "remote", SessionID: "remote-session"}, true)
	})
	m.Drafts["session:s"] = "a newer ordinary draft"
	if err := os.Remove(filepath.Join(dir, "client.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err = m.EncodeState()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Saver.Flush(data); err == nil {
		t.Fatal("expected second write failure")
	}
	saved, err := v.Saver.Result()
	if !v.syncNavigationSave(saved, err) {
		t.Fatal("save error not handled")
	}
	if !reflect.DeepEqual(m.Navigation, confirmed) {
		t.Fatal("rollback lost last successful arrangement", m.Navigation, confirmed)
	}
	if m.Drafts["session:s"] != "a newer ordinary draft" {
		t.Fatal("rollback replaced current draft")
	}
}
