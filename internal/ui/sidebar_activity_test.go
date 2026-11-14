package ui

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestSidebarRunningIndicatorPulsesWithoutRebuilding(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduce := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/reduce=%v", dark, reduce), func(t *testing.T) {
				m := navigationFixture()
				v := New(m, nil, "")
				// Background sessions have snapshot state without loaded history.
				s := m.Snapshot.Sessions[2]
				r := navigationRow{session: s, workspace: m.Snapshot.Workspaces[1], presence: 1}
				builds := 0
				ui := n.NewTester(func(c *n.Context) {
					builds++
					theme := applyAppearance(c, m.Preferences)
					n.Column(c).Fill().Padding(20).Background(theme.Background).Children(func() { v.navigationSessionRow(c, theme, r) })
				}, 300, 90)
				ui.SetDark(dark)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduce})
				ui.Advance(1400 * time.Millisecond)
				dot, ok := ui.Find("会话运行状态：" + s.ID)
				if !ok {
					t.Fatal("background running indicator is missing")
				}
				before := contextPixels(ui, dot)
				count := builds
				ui.Advance(700 * time.Millisecond)
				if same := bytes.Equal(before, contextPixels(ui, dot)); same != reduce {
					t.Error("sidebar indicator does not honor active/reduced-motion animation", reduce, same)
				}
				if after, _ := ui.Find("会话运行状态：" + s.ID); after != dot || builds != count {
					t.Error("indicator animation moves the row or rebuilds the view", dot, after, count, builds)
				}
				m.Details[s.ID] = domain.SessionDetail{Requests: []domain.PendingRequest{{ID: "question", State: "pending"}}}
				ui.Frame()
				ui.Advance(300 * time.Millisecond)
				waiting, ok := ui.Find("会话等待状态：" + s.ID)
				before = contextPixels(ui, waiting)
				ui.Advance(700 * time.Millisecond)
				if !ok || !bytes.Equal(before, contextPixels(ui, waiting)) || ui.FramePending() {
					t.Error("waiting indicator still pulses")
				}
				delete(m.Details, s.ID)
				r.session.Status = "completed"
				ui.Frame()
				ui.Advance(700 * time.Millisecond)
				if _, ok := ui.Find("会话运行状态：" + s.ID); ok || ui.FramePending() {
					t.Error("completed session keeps its running animation")
				}
			})
		}
	}
}
