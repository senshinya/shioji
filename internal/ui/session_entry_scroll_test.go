package ui

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"shioji/internal/app"
	"shioji/internal/domain"
)

// A latest-window receipt must not repeat the entry scroll after the reader
// has already taken control of the cached transcript.
func TestSessionEntryHydrationPreservesImmediateReaderScroll(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, distance := range []float32{12, 120} {
			t.Run(fmt.Sprintf("dark=%t/scroll=%g", dark, distance), func(t *testing.T) {
				v, ui := progressFollowFixture()
				m := v.Model
				m.WorkerMinor = 37
				v.Controller = app.NewController("")
				d := m.Details["s"]
				d.Session.Status = domain.ExecutionCompleted
				d.Turns[0].Status = domain.ExecutionCompleted
				for i := range d.Items {
					d.Items[i].Order = int64(i + 1)
				}
				d.History = &domain.HistoryWindow{First: 1, Last: 36}
				m.Details["s"] = d
				m.Snapshot.Sessions[0] = d.Session
				v.home()
				ui.Frame()
				ui.Advance(time.Second)
				ui.SetDark(dark)
				read := m.BeginHistoryRead("s")
				v.navigateSession("s", "w")
				ui.Frame()
				if !v.lists["s"].AtEnd() {
					t.Fatal("entry did not initially show the latest content")
				}
				ui.Scroll(800, 350, 0, -distance)
				ui.Frame()
				before, ok := ui.Find("进度 30")
				if !ok || v.lists["s"].FollowingEnd() {
					t.Fatal("immediate reader scroll did not leave bottom following")
				}
				capture := func(frame int) {
					if dir := os.Getenv("SHIOJI_ENTRY_SCROLL_CAPTURE"); dir != "" {
						t.Setenv("SHIOJI_SKILL_SNAPSHOTS", fmt.Sprintf("%s/%t-%g", dir, dark, distance))
						saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
					}
				}
				for frame := 0; frame < 60; frame++ {
					if frame == 10 {
						if err := m.MergeHistoryPage(d, read, true, domain.HistoryQuery{SessionID: "s"}); err != nil {
							t.Fatal(err)
						}
						read.Pending = false
						m.FoldToolHistory["s"] = true
						m.HistoryOpenings["s"] = true
					}
					ui.Frame()
					ui.Advance(time.Second / 30)
					capture(frame)
				}
				after, ok := ui.Find("进度 30")
				if !ok || math.Abs(float64(after.Y-before.Y)) > 1 || v.lists["s"].FollowingEnd() {
					t.Fatalf("hydration pulled the reader back to latest: before=%v after=%v following=%t", before, after, v.lists["s"].FollowingEnd())
				}
			})
		}
	}
}

func TestSessionEntryHydrationKeepsLatestWithoutReaderInterruption(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", cached), func(t *testing.T) {
			v, ui := progressFollowFixture()
			m := v.Model
			m.WorkerMinor = 37
			v.Controller = app.NewController("")
			d := m.Details["s"]
			for i := range d.Items {
				d.Items[i].Order = int64(i + 1)
			}
			d.History = &domain.HistoryWindow{First: 1, Last: 36}
			v.home()
			ui.Frame()
			if cached {
				m.Details["s"] = d
			} else {
				empty := d
				empty.Items = nil
				empty.History = &domain.HistoryWindow{Invalidated: true}
				m.Details["s"] = empty
			}
			read := m.BeginHistoryRead("s")
			v.navigateSession("s", "w")
			ui.Frame()
			ui.Advance(300 * time.Millisecond)
			if err := m.MergeHistoryPage(d, read, true, domain.HistoryQuery{SessionID: "s"}); err != nil {
				t.Fatal(err)
			}
			read.Pending = false
			m.FoldToolHistory["s"] = true
			m.HistoryOpenings["s"] = true
			ui.Frame()
			ui.Advance(time.Second)
			if !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() || !ui.HasText("进度 35") {
				t.Fatal("uninterrupted entry did not remain at the latest hydrated content")
			}
		})
	}
}
