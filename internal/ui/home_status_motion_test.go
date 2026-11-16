package ui

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// Losing the native content identity at receipt/hydration must not fade an
// already visible status row out and restart its entrance from zero opacity.
func TestHomeStatusHandoffKeepsPaintedStatus(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		dark, hydrated, reduce bool
	}{{name: "light"}, {name: "dark", dark: true}, {name: "early-detail", hydrated: true}, {name: "reduced", dark: true, reduce: true}} {
		t.Run(tt.name, func(t *testing.T) {
			v, ui, job := sendFixture(t, true)
			ui.SetDark(tt.dark)
			ui.SetPreferences(n.Preferences{ReduceMotion: tt.reduce})
			ui.Advance(time.Second)
			in := job.Input.(domain.Submission)
			session := domain.Session{ID: "ready", WorkspaceID: in.WorkspaceID, Status: domain.ExecutionStarting}
			if dir := os.Getenv("SHIOJI_HOME_STATUS_CAPTURE"); dir != "" {
				t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, tt.name))
			}
			frame := 0
			capture := func() {
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
				frame++
			}
			settle := func() {
				if os.Getenv("SHIOJI_HOME_STATUS_CAPTURE") == "" {
					ui.Advance(500 * time.Millisecond)
					return
				}
				for i := 0; i < 30; i++ {
					capture()
					ui.Advance(time.Second / 60)
				}
			}
			settle()
			for _, stage := range []struct{ before, after string }{{"正在发送", "正在准备会话"}, {"正在准备会话", "进行中"}} {
				before, ok := ui.Find(stage.before)
				if !ok {
					t.Fatal("missing status before handoff", stage.before, ui.Texts())
				}
				area := image.Rect(int(before.X)-12, int(before.Y)-2, int(before.X)+160, int(before.Y+before.H)+2)
				inkBefore := homeStatusInk(ui.Image(), area)
				paintBefore := copyFrame(ui)
				if stage.after == "正在准备会话" {
					v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, session)
					if tt.hydrated {
						v.Model.Details[session.ID] = domain.SessionDetail{Session: session}
					}
					ack, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: session.ID, State: domain.OperationAccepted})
					job.Apply(v.Model, ack, nil)
				} else {
					started := ui.Now()
					session.Status = domain.ExecutionRunning
					v.Model.Details[session.ID] = domain.SessionDetail{Session: session,
						Items: []domain.Item{{ID: "input:" + in.OperationID, TurnID: "t", Kind: "user", Text: in.Text, Status: "sent", Final: true}},
						Turns: []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started}}}
					v.Model.ApplyOperation(domain.Operation{ID: in.OperationID, SessionID: session.ID, State: domain.OperationConfirmed})
				}
				ui.Frame()
				after, ok := ui.Find(stage.after)
				if !ok || before.X != after.X || before.Y != after.Y {
					t.Errorf("%s → %s moved its anchor: before=%+v after=%+v", stage.before, stage.after, before, after)
				}
				// The old words should still be painted when replacement starts.
				// Later frames may narrow to the shorter running label.
				paintFirst := ui.Image()
				inkFirst := homeStatusInk(paintFirst, area)
				if inkBefore < 1000 || inkFirst < 1000 || !tt.reduce && inkFirst < inkBefore*8/10 {
					t.Errorf("%s → %s blinks at handoff: before=%d first=%d", stage.before, stage.after, inkBefore, inkFirst)
				}
				delta := 0
				for y := area.Min.Y; y < area.Max.Y; y++ {
					for x := area.Min.X; x < area.Max.X; x++ {
						a, b := paintBefore.RGBAAt(x, y), paintFirst.RGBAAt(x, y)
						delta += abs(int(a.R)-int(b.R)) + abs(int(a.G)-int(b.G)) + abs(int(a.B)-int(b.B))
					}
				}
				if !tt.reduce && delta > inkBefore*15/100 {
					t.Errorf("%s → %s replaces painted content immediately: delta=%d before=%d", stage.before, stage.after, delta, inkBefore)
				}
				if stage.after == "进行中" && !tt.reduce {
					clock, ok := ui.Find("运行时长")
					if !ok {
						t.Fatal("authoritative running turn has no clock")
					}
					clockArea := image.Rect(int(clock.X), int(clock.Y), int(clock.X+clock.W), int(clock.Y+clock.H))
					if ink := homeStatusInk(paintFirst, clockArea); ink > 100 {
						t.Errorf("new clock flashes beside the outgoing preparation label: ink=%d", ink)
					}
					tail := image.Rect(int(before.X+before.W)-11, area.Min.Y, int(before.X+before.W), area.Max.Y)
					oldTail := homeStatusInk(paintBefore, tail)
					// Preserve the outgoing final glyph while the short label fades
					// in; changing the shader box must not slice it off midway.
					for i := 0; i < 2; i++ {
						capture()
						ui.Advance(time.Second / 60)
					}
					if tailInk := homeStatusInk(ui.Image(), tail); tailInk < oldTail/3 {
						t.Errorf("outgoing preparation label clipped before fading: old=%d at33ms=%d", oldTail, tailInk)
					}
				}
				settle()
				if stage.after == "进行中" {
					clock, ok := ui.Find("运行时长")
					area := image.Rect(int(clock.X), int(clock.Y), int(clock.X+clock.W), int(clock.Y+clock.H))
					if !ok || homeStatusInk(ui.Image(), area) < 500 {
						t.Error("settled running status has no painted clock")
					}
				}
			}
			if !ui.Focused("消息输入") {
				t.Fatal("status handoff lost composer focus")
			}
		})
	}
}

func homeStatusInk(im *image.RGBA, area image.Rectangle) int {
	bg := im.RGBAAt(area.Max.X, area.Min.Y)
	ink := 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			p := im.RGBAAt(x, y)
			ink += abs(int(p.R)-int(bg.R)) + abs(int(p.G)-int(bg.G)) + abs(int(p.B)-int(bg.B))
		}
	}
	return ink
}
