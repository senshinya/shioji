package ui

import (
	"fmt"
	"reflect"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestWorkspaceDragPreviewSettlesWhilePointerIsStationary(t *testing.T) {
	for _, following := range []bool{false, true} {
		for _, offset := range []float32{2, 14, 16, 28, 35, 60} {
			for _, down := range []bool{false, true} {
				t.Run(fmt.Sprintf("offset=%g/down=%v/following=%v", offset, down, following), func(t *testing.T) {
					m := navigationFixture()
					if following {
						m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: "last", DeviceID: "A", Name: "Last workspace"})
					}
					v := New(m, nil, "")
					ui := n.NewTester(v.View, 1280, 800)
					ui.Advance(time.Second)
					before := m.Navigation.Clone()
					from, _ := ui.Find("Remote workspace")
					to, _ := ui.Find("Example")
					if down {
						from, to = to, from
					}
					ui.Press(from.X+60, from.Y+14)
					ui.Move(to.X+60, to.Y+offset)
					ui.Advance(300 * time.Millisecond)
					if v.navigationDrag == nil || v.navigationDrag.phase != navigationMoving {
						t.Fatal("drag did not stay active")
					}
					settled := v.navigationDrag.preview.Clone()
					for frame := 0; frame < 60; frame++ {
						ui.Advance(16 * time.Millisecond)
						if !reflect.DeepEqual(settled, v.navigationDrag.preview) {
							t.Fatalf("stationary pointer changed preview at frame %d: %v -> %v", frame, settled, v.navigationDrag.preview)
						}
					}
					if !reflect.DeepEqual(before, m.Navigation) {
						t.Fatal("preview was persisted while held")
					}
					ui.Release(to.X+60, to.Y+offset)
					ui.Advance(300 * time.Millisecond)
					if !reflect.DeepEqual(settled, m.Navigation) {
						t.Fatal("drop did not commit the settled preview", settled, m.Navigation)
					}
				})
			}
		}
	}
}

func TestNavigationDragUsesVisibleInsertionAfterLongListScroll(t *testing.T) {
	m := navigationFixture()
	for i := 0; i < 500; i++ {
		m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: fmt.Sprint("history-", i), WorkspaceID: "w", Title: fmt.Sprint("History ", i), Status: "completed"})
	}
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	list, _ := ui.Find("工作区与会话")
	ui.Scroll(list.X+70, list.Y+list.H/2, 0, 100000)
	ui.Advance(100 * time.Millisecond)
	if _, ok := ui.Find("Remote running"); !ok {
		t.Fatal("did not scroll to list end")
	}
	from, ok := ui.Find("History 499")
	if !ok {
		t.Fatal("last local row not rendered")
	}
	to, ok := ui.Find("History 498")
	if !ok {
		t.Fatal("target local row not rendered")
	}
	ui.Press(from.X+60, from.Y+14)
	ui.Move(to.X+60, to.Y+2)
	ui.Advance(80 * time.Millisecond)
	ui.Release(to.X+60, to.Y+2)
	ui.Advance(300 * time.Millisecond)
	got := m.NavigationSessions(m.Snapshot.Workspaces[0])
	tail := got[len(got)-2:]
	if tail[0].ID != "history-499" || tail[1].ID != "history-498" {
		t.Fatal("virtual scroll moved before the wrong row", tail[0].ID, tail[1].ID)
	}
}

func TestNavigationDragMakesRoomAndOnlyDropCommits(t *testing.T) {
	m := navigationFixture()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	before := m.Navigation.Clone()
	first, _ := ui.Find("Layout")
	last, _ := ui.Find("Other session")
	ui.Press(last.X+60, last.Y+last.H/2)
	ui.Move(first.X+60, first.Y+2)
	ui.Advance(65 * time.Millisecond)
	if !reflect.DeepEqual(before, m.Navigation) {
		t.Fatal("preview was persisted before release")
	}
	mid, _ := ui.Find("Layout")
	if mid.Y <= first.Y+.5 {
		t.Fatal("sibling did not make room", first, mid)
	}
	ui.Release(first.X+60, first.Y+2)
	ui.Advance(300 * time.Millisecond)
	if got := m.NavigationSessions(m.Snapshot.Workspaces[0]); got[0].ID != "other" {
		t.Fatal("drop did not apply order", got)
	}
	if m.SessionID != "s" || m.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("drag navigated or changed draft")
	}
}
func TestNavigationDragPinsGloballyAndUnpinsOnlyIntoOwningWorkspace(t *testing.T) {
	m := navigationFixture()
	m.ObserveNavigation()
	m.SetSessionPinned(app.SessionLocation{DeviceID: "A", WorkspaceID: "w", SessionID: "s"}, true)
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	remote, _ := ui.Find("Remote running")
	heading, _ := ui.Find("置顶")
	ui.Press(remote.X+60, remote.Y+remote.H/2)
	ui.Move(heading.X+60, heading.Y+3)
	ui.Advance(220 * time.Millisecond)
	if len(m.Navigation.Pinned) != 1 {
		t.Fatal("pin preview changed model")
	}
	ui.Release(heading.X+60, heading.Y+3)
	ui.Advance(300 * time.Millisecond)
	if len(m.Navigation.Pinned) != 2 || m.Navigation.Pinned[0].DeviceID != "B" {
		t.Fatal("cross-device pin drop failed", m.Navigation.Pinned)
	}
	remote, _ = ui.Find("Remote running")
	owner, _ := ui.Find("Remote workspace")
	ui.Press(remote.X+60, remote.Y+remote.H/2)
	ui.Move(owner.X+50, owner.Y+8)
	ui.Advance(50 * time.Millisecond)
	ui.Release(owner.X+50, owner.Y+8)
	ui.Advance(300 * time.Millisecond)
	if len(m.Navigation.Pinned) != 1 || len(m.NavigationSessions(m.Snapshot.Workspaces[1])) != 1 {
		t.Fatal("unpin did not restore owner", m.Navigation)
	}
	if m.Snapshot.Sessions[2].WorkspaceID != "remote" {
		t.Fatal("drag changed execution ownership")
	}
}
func TestNavigationDragCancelsOutsideEscapeForeignAndCatalogChange(t *testing.T) {
	for _, kind := range []string{"escape", "outside", "foreign", "catalog"} {
		t.Run(kind, func(t *testing.T) {
			m := navigationFixture()
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1280, 800)
			ui.Advance(time.Second)
			before := m.Navigation.Clone()
			row, _ := ui.Find("Other session")
			target, _ := ui.Find("工作区")
			ui.Press(row.X+60, row.Y+14)
			ui.Move(target.X+40, target.Y+4)
			ui.Advance(50 * time.Millisecond)
			x, y := target.X+40, target.Y+4
			switch kind {
			case "escape":
				ui.Key(0, n.KeyEscape)
			case "outside":
				x, y = 900, 600
				ui.Move(x, y)
			case "foreign":
				r, _ := ui.Find("Remote workspace")
				x, y = r.X+50, r.Y+7
				ui.Move(x, y)
			case "catalog":
				m.Snapshot.Workspaces[0].DeviceID = "replacement"
				ui.Frame()
			}
			ui.Release(x, y)
			ui.Advance(300 * time.Millisecond)
			if kind == "catalog" {
				if len(m.Navigation.Pinned) != 0 {
					t.Fatal("stale identity became pinned")
				}
			} else if !reflect.DeepEqual(before, m.Navigation) {
				t.Fatal("canceled drag saved preview", kind, m.Navigation)
			}
		})
	}
}
func TestEmptyGlobalPinDropAndWorkspaceDragPreservePinOrder(t *testing.T) {
	m := navigationFixture()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	if ui.HasText("置顶") {
		t.Fatal("empty section consumes space")
	}
	row, _ := ui.Find("Remote running")
	target, _ := ui.Find("工作区")
	ui.Press(row.X+60, row.Y+14)
	ui.Move(target.X+40, target.Y+5)
	ui.Advance(70 * time.Millisecond)
	ui.Release(target.X+40, target.Y+5)
	ui.Advance(300 * time.Millisecond)
	if len(m.Navigation.Pinned) != 1 {
		t.Fatal("first global drop failed")
	}
	pins := append([]app.SessionLocation(nil), m.Navigation.Pinned...)
	a, _ := ui.Find("Example")
	b, _ := ui.Find("Remote workspace")
	ui.Press(b.X+50, b.Y+b.H/2)
	ui.Move(a.X+50, a.Y+2)
	ui.Advance(70 * time.Millisecond)
	ui.Release(a.X+50, a.Y+2)
	ui.Advance(300 * time.Millisecond)
	if got := m.NavigationWorkspaces(); got[0].ID != "remote" {
		t.Fatal("workspace group did not reorder", got)
	}
	if !reflect.DeepEqual(pins, m.Navigation.Pinned) {
		t.Fatal("workspace move changed global pins")
	}
	if m.SessionID != "s" {
		t.Fatal("workspace drag opened another session")
	}
}

// New events may change status while held; that must not be mistaken for a new drag.
func TestNavigationDragAllowsActivityUpdateWithoutChangingAcceptedContext(t *testing.T) {
	m := navigationFixture()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	first, _ := ui.Find("Layout")
	row, _ := ui.Find("Other session")
	ui.Press(row.X+60, row.Y+14)
	ui.Move(first.X+60, first.Y+2)
	m.Snapshot.Sessions[0].Status = domain.ExecutionState("running")
	ui.Frame()
	ui.Release(first.X+60, first.Y+2)
	ui.Advance(300 * time.Millisecond)
	if got := m.NavigationSessions(m.Snapshot.Workspaces[0]); got[0].ID != "other" || got[1].Status != "running" {
		t.Fatal("activity canceled order or reverted runtime", got)
	}
}

func TestNavigationDragReducedMotionSettlesWithoutResidualShift(t *testing.T) {
	m := navigationFixture()
	m.Preferences.ReduceMotion = true
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Advance(time.Second)
	first, _ := ui.Find("Layout")
	source, _ := ui.Find("Other session")
	ui.Press(source.X+60, source.Y+14)
	ui.Move(first.X+60, first.Y+2)
	ui.Advance(time.Millisecond)
	ui.Release(first.X+60, first.Y+2)
	ui.Frame()
	a, _ := ui.Find("Other session")
	b, _ := ui.Find("Layout")
	if a.Y >= b.Y || len(v.navigationGeometry.offsets) != 0 {
		t.Fatal("reduced motion left displaced rows", a, b)
	}
}
