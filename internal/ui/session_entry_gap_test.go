package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// A session with a shorter dock must not inherit another session's trailing
// reserve, even for its first frame while the list already reports AtEnd.
func TestSessionEntryDoesNotInheritOtherDockSpace(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			for _, secondary := range []bool{false, true} {
				t.Run(fmt.Sprintf("dark=%v/reduced=%v/secondary=%v", dark, reduced, secondary), func(t *testing.T) {
					checkSessionEntryDockSpace(t, dark, reduced, secondary)
				})
			}
		}
	}
}

// Render the production native view at 30 fps, preserving the first painted
// entry frame that Tester.Frame would otherwise settle past. Fixtures supply
// cached history and drafts; no device, worker or installed app is changed.
func TestSessionEntryNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_SESSION_ENTRY_RECORDING")
	if dir == "" {
		t.Skip("opt-in native session-entry recording")
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprint(dark)))
			v, _ := progressFollowFixture()
			v = New(v.Model, nil, "")
			m := v.Model
			d := m.Details["s"]
			other := d.Session
			other.ID, other.Title = "other", "长草稿会话"
			m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
			m.Details[other.ID] = domain.SessionDetail{Session: other, Items: d.Items, Turns: d.Turns}
			m.Drafts["session:other"] = strings.Repeat("保留的多行草稿\n", 6)
			frame, paints, entry := 0, 0, false
			var ui *n.Tester
			ui = n.NewTester(func(c *n.Context) {
				if entry && paints == 1 {
					saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
				}
				n.Column(c).Fill().Children(func() { v.View(c) }).DrawOver(func(_ *n.Painter, _ n.Rect) {
					if entry {
						paints++
					}
				})
			}, 1200, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			for frame = 0; frame < 210; frame++ {
				entry, paints = false, 0
				switch frame {
				case 30, 150:
					v.navigateSession("other", "w")
					entry = true
				case 90, 155:
					v.navigateSession("s", "w")
					entry = true
				case 120:
					ui.Scroll(800, 350, 0, -180)
				}
				if entry {
					ui.Frame()
				}
				ui.Advance(time.Second / 30)
				if !entry || paints < 2 {
					saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
				}
			}
		})
	}
}

func checkSessionEntryDockSpace(t *testing.T, dark, reduced, secondary bool) {
	t.Helper()
	m := layoutModel()
	d := m.Details["s"]
	d.Session.Status = "running"
	m.Snapshot.Sessions[0].Status = "running"
	d.Items = nil
	for i := 0; i < 36; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("progress-", i), TurnID: "turn", Kind: "activity", Name: fmt.Sprint("进度 ", i), Final: true})
	}
	d.Turns = []domain.Turn{{ID: "turn", Status: "running"}}
	m.Details["s"] = d
	other := d.Session
	other.ID, other.Title = "other", "另一会话"
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
	m.Details[other.ID] = domain.SessionDetail{Session: other, Items: d.Items, Turns: d.Turns}
	m.Drafts["session:other"] = strings.Repeat("长草稿\n", 6)
	v := New(m, nil, "")
	pane := v
	type paint struct {
		row   n.Rect
		dock  float32
		atEnd bool
	}
	var frames []paint
	record := false
	var ui *n.Tester
	ui = n.NewTester(func(c *n.Context) {
		if record && len(frames) == 1 {
			saveNativeSkillImage(t, ui, fmt.Sprintf("entry-gap-first-%v-%v-%v.png", dark, reduced, secondary))
		}
		n.Column(c).Fill().Children(func() { v.View(c) }).DrawOver(func(_ *n.Painter, _ n.Rect) {
			if record {
				frames = append(frames, paint{pane.historyRowBounds["progress-35"], pane.dockHeight, pane.lists[pane.Model.SessionID].AtEnd()})
			}
		})
	}, 1200, 820)
	ui.SetDark(dark)
	ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
	ui.Advance(time.Second)
	if secondary {
		v.openParallel("other")
		ui.Frame()
		ui.Advance(time.Second)
		pane = v.secondary
	} else {
		v.navigateSession("other", "w")
		ui.Frame()
		ui.Advance(time.Second)
	}
	tall := pane.dockHeight
	if secondary {
		// Switch the right pane to a cold copy with no previously measured dock.
		target := d.Session
		target.ID, target.Title = "target", "目标会话"
		m.Snapshot.Sessions = append(m.Snapshot.Sessions, target)
		m.Details[target.ID] = domain.SessionDetail{Session: target, Items: d.Items, Turns: d.Turns}
		v.navigateSession("target", "w")
	} else {
		v.navigateSession("s", "w")
	}
	record = true
	ui.Frame()
	first, found := ui.Find("进度 35")
	dock, _ := ui.Find("会话底部面板")
	atEnd := pane.lists[pane.Model.SessionID].AtEnd()
	ui.Advance(time.Second)
	settled, _ := ui.Find("进度 35")
	saveNativeSkillImage(t, ui, fmt.Sprintf("entry-gap-settled-%v-%v-%v.png", dark, reduced, secondary))
	if tall-pane.dockHeight < 80 || !found || !atEnd {
		t.Fatal("fixture did not reproduce a tall-to-short dock at the scroll end", tall, dock, found, atEnd)
	}
	for i, frame := range frames {
		if frame.row.H == 0 || !frame.atEnd || math.Abs(float64(frame.row.Y-frames[len(frames)-1].row.Y)) > 1 {
			t.Fatal("session enters above its reachable end and then jumps down", "frame", i, frame, "last paint", frames[len(frames)-1], "settled", settled, "dock", dock, "previous dock", tall, "atEnd", atEnd, "after Frame", first)
		}
	}
}
