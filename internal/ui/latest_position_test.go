package ui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestLatestControlMovesSmoothlyWithDraftHeight(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/reduced=%v", dark, reduced), func(t *testing.T) {
				v, ui := progressFollowFixture()
				ui.SetDark(dark)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				ui.Click("消息输入")
				ui.Scroll(800, 350, 0, -240)
				ui.Frame()
				ui.Advance(time.Second)
				for _, draft := range []string{strings.Repeat("增加输入行\n", 5), "短草稿"} {
					before, _ := ui.Find("回到最新")
					anchor, _ := ui.Find("进度 30")
					v.Model.Drafts[v.Model.DraftKey()] = draft
					ui.Frame()
					var positions []float32
					for frame := 0; frame < 36; frame++ {
						ui.Advance(time.Second / 60)
						button, ok := ui.Find("回到最新")
						dock, _ := ui.Find("会话底部面板")
						if !ok || math.Abs(float64(dock.Y-button.Y-button.H-12)) > .5 {
							t.Fatal("latest control loses its dock gap", button, dock)
						}
						positions = append(positions, button.Y)
					}
					end := positions[len(positions)-1]
					travel := math.Abs(float64(end - before.Y))
					if travel < 40 {
						t.Fatal("fixture did not move the dock", before.Y, end)
					}
					previous, movingFrames := before.Y, 0
					for _, y := range positions {
						step := math.Abs(float64(y - previous))
						if step > .1 {
							movingFrames++
						}
						if !reduced && step > travel/2 {
							t.Fatal("draft height snaps the latest indicator vertically", before.Y, end, positions)
						}
						previous = y
					}
					if !reduced && movingFrames < 4 || reduced && movingFrames > 1 {
						t.Fatal("wrong vertical motion for preference", reduced, movingFrames, positions)
					}
					after, _ := ui.Find("进度 30")
					if math.Abs(float64(after.Y-anchor.Y)) > 1 || !ui.Focused("消息输入") {
						t.Fatal("draft resizing changes reading position or typing focus", anchor, after)
					}
				}
			})
		}
	}
}

func TestLatestControlDraftHeightReversalKeepsCurrentPosition(t *testing.T) {
	v, ui := progressFollowFixture()
	ui.Click("消息输入")
	ui.Scroll(800, 350, 0, -240)
	ui.Frame()
	ui.Advance(time.Second)
	initial, _ := ui.Find("回到最新")
	for _, draft := range []string{strings.Repeat("增加输入行\n", 5), "短草稿", strings.Repeat("再次展开\n", 6), "保留草稿"} {
		ui.Frame()
		before, _ := ui.Find("回到最新")
		v.Model.Drafts[v.Model.DraftKey()] = draft
		ui.Frame()
		first, _ := ui.Find("回到最新")
		if math.Abs(float64(first.Y-before.Y)) > 1 {
			t.Fatal("reversing draft height restarts from another position", before, first)
		}
		ui.Advance(60 * time.Millisecond)
	}
	ui.Advance(time.Second)
	end, _ := ui.Find("回到最新")
	if math.Abs(float64(end.Y-initial.Y)) > .5 || !ui.Focused("消息输入") || v.Model.Drafts[v.Model.DraftKey()] != "保留草稿" {
		t.Fatal("reversed draft height fails to settle or loses draft/focus", initial, end)
	}
}

func TestLatestControlVerticalNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_LATEST_POSITION_RECORDING")
	if dir == "" {
		t.Skip("opt-in native latest position recording")
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", fmt.Sprintf("%s/%v", dir, dark))
			v, ui := progressFollowFixture()
			ui.SetDark(dark)
			ui.Click("消息输入")
			ui.Scroll(800, 350, 0, -240)
			ui.Frame()
			ui.Advance(time.Second)
			for frame := 0; frame < 180; frame++ {
				switch frame {
				case 15, 60, 120:
					v.Model.Drafts[v.Model.DraftKey()] = strings.Repeat("检查进度指示器随输入区平滑移动\n", 5)
				case 18, 90, 135:
					v.Model.Drafts[v.Model.DraftKey()] = "保留当前草稿"
				case 119:
					ui.SetPreferences(n.Preferences{ReduceMotion: true})
				}
				ui.Frame()
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
		})
	}
}
