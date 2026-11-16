package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

// Opening cached history must position the first frame, even when the worker's
// latest-window response is delayed. Completion of that read is too late.
func TestSessionReopenPositionsCachedHistoryBeforeHydration(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, entry := range []string{"sidebar", "home", "selection"} {
			t.Run(fmt.Sprintf("dark=%t/%s", dark, entry), func(t *testing.T) {
				v, ui := progressFollowFixture()
				m := v.Model
				m.WorkerMinor = 37
				v.Controller = app.NewController("")
				d := m.Details["s"]
				d.Session.Status = domain.ExecutionCompleted
				d.Turns[0].Status = domain.ExecutionCompleted
				d.History = &domain.HistoryWindow{First: 1, Last: 36}
				m.Details["s"] = d
				m.Snapshot.Sessions[0] = d.Session
				ui.SetDark(dark)
				ui.Frame()
				v.lists["s"].ScrollTo(8, n.Start)
				ui.Frame()
				ui.Advance(time.Second)
				if v.lists["s"].AtEnd() {
					t.Fatal("fixture did not leave the latest position")
				}
				if entry == "sidebar" {
					other := d.Session
					other.ID, other.Title = "other", "另一会话"
					m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
					m.Details[other.ID] = domain.SessionDetail{Session: other}
					v.navigateSession(other.ID, "w")
				} else {
					v.home()
				}
				ui.Frame()
				ui.Advance(time.Second)
				m.HistoryReads["s"] = &app.HistoryRead{Pending: true}
				if entry == "selection" {
					m.SessionID = "s"
				} else {
					v.navigateSession("s", "w")
				}
				ui.Frame()
				firstAtEnd := v.lists["s"].AtEnd()
				latest, present := ui.Find("进度 35")
				dock, _ := ui.Find("会话底部面板")
				firstHasLatest := present && latest.Y >= 48 && latest.Y+latest.H <= dock.Y
				if dir := os.Getenv("SHIOJI_REOPEN_RECORDING"); dir != "" {
					t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprint(dark), entry))
					for frame := 0; frame < 60; frame++ {
						if frame == 30 {
							delete(m.HistoryReads, "s")
							m.HistoryOpenings["s"] = true
							ui.Frame()
						}
						ui.Advance(time.Second / 30)
						saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
					}
				}
				if !firstAtEnd || !firstHasLatest {
					t.Fatalf("cached session first shows an old reading position while hydration is pending: atEnd=%t latestVisible=%t", firstAtEnd, firstHasLatest)
				}
			})
		}
	}
}

func TestParallelReopenPositionsCachedHistoryBeforeHydration(t *testing.T) {
	v, ui := progressFollowFixture()
	m := v.Model
	other := m.Details["s"].Session
	other.ID, other.Title = "other", "另一会话"
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
	m.Details[other.ID] = domain.SessionDetail{Session: other}
	v.navigateSession(other.ID, "w")
	ui.Frame()
	v.openParallel("s")
	ui.Frame()
	ui.Advance(time.Second)
	v.secondary.lists["s"].ScrollTo(8, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	if v.secondary.lists["s"].AtEnd() {
		t.Fatal("fixture did not leave the latest position")
	}
	v.closeParallel()
	ui.Frame()
	v.openParallel("s")
	ui.Frame()
	if !v.secondary.lists["s"].AtEnd() {
		t.Fatal("reopened secondary pane restored its old reading position")
	}
	v.secondary.lists["s"].ScrollTo(8, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	before, _ := v.secondary.lists["s"].Visible()
	v.setSecondaryFocus(false)
	v.navigateSession("s", "w")
	ui.Frame()
	after, _ := v.secondary.lists["s"].Visible()
	if before != after || v.secondary.lists["s"].AtEnd() {
		t.Fatal("focusing an already visible secondary session reset its reading position")
	}
}

func TestCurrentSessionRefreshKeepsReadingPosition(t *testing.T) {
	v, ui := progressFollowFixture()
	v.lists["s"].ScrollTo(8, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	before := v.historyRowBounds["progress-9"]
	v.navigateSession("s", "w")
	ui.Frame()
	v.Model.ConnectionEpoch = "reconnected"
	ui.Frame()
	ui.Advance(time.Second)
	after := v.historyRowBounds["progress-9"]
	if v.lists["s"].AtEnd() || before.H == 0 || after != before {
		t.Fatal("refreshing the visible session moved the reader", before, after)
	}
}
