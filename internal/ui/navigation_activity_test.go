package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestUserMessageReorderAnimatesAndRetargetsFromItsCurrentPosition(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("reduced=%v/dark=%v", reduced, dark), func(t *testing.T) {
				m := navigationFixture()
				m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "third", WorkspaceID: "w", Title: "第三个会话", Status: "completed"})
				m.Preferences.ReduceMotion = reduced
				v := New(m, nil, "")
				ui := n.NewTester(v.View, 1280, 800)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				ui.SetDark(dark)
				ui.Advance(time.Second)
				first, _ := ui.Find("Layout")
				before, _ := ui.Find("第三个会话")
				promote := func(id string) {
					t.Helper()
					if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, Type: "submission", SessionID: id, Item: &domain.Item{ID: fmt.Sprint(m.Sequence), Kind: "user"}}); err != nil {
						t.Fatal(err)
					}
					ui.Frame()
				}
				promote("third")
				ui.Advance(65 * time.Millisecond)
				// Paint at the current clock before comparing a retarget at that
				// same instant; Advance may end between two display refreshes.
				ui.Frame()
				middle, _ := ui.Find("第三个会话")
				saveNativeSkillImage(t, ui, fmt.Sprintf("user-reorder-dark-%v-reduced-%v-middle.png", dark, reduced))
				if !reduced && !(middle.Y < before.Y-1 && middle.Y > first.Y+1) || reduced && math.Abs(float64(middle.Y-first.Y)) > .5 {
					t.Fatal("user message promotion jumps or ignores reduced motion", first, before, middle)
				}
				promote("other")
				continued, _ := ui.Find("第三个会话")
				if !reduced && math.Abs(float64(continued.Y-middle.Y)) > .5 {
					t.Fatal("second message restarts the movement from an old position", middle, continued)
				}
				ui.Advance(300 * time.Millisecond)
				saveNativeSkillImage(t, ui, fmt.Sprintf("user-reorder-dark-%v-reduced-%v-end.png", dark, reduced))
				top, _ := ui.Find("Other session")
				next, _ := ui.Find("第三个会话")
				if math.Abs(float64(top.Y-first.Y)) > .5 || next.Y <= top.Y || m.SessionID != "s" || m.Drafts["session:s"] != "ordinary draft" {
					t.Fatal("final order, selection or draft is wrong", top, next)
				}
			})
		}
	}
}

func TestUserMessagePromotionCancelsAStaleSidebarDrop(t *testing.T) {
	m := navigationFixture()
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "third", WorkspaceID: "w", Title: "第三个会话", Status: "completed"})
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	first, _ := ui.Find("Layout")
	row, _ := ui.Find("Other session")
	ui.Press(row.X+50, row.Y+row.H/2)
	ui.Move(first.X+50, first.Y+2)
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "third", Item: &domain.Item{ID: "new-during-drag", Kind: "user"}}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Release(first.X+50, first.Y+2)
	ui.Advance(300 * time.Millisecond)
	if got := m.NavigationSessions(m.Snapshot.Workspaces[0]); len(got) == 0 || got[0].ID != "third" {
		t.Fatal("a stale drop overwrote the incoming user-message order", got)
	}
}
