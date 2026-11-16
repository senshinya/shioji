package ui

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestLatestControlTextInkIsCenteredInCapsule(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, timed := range []bool{false, true} {
			for _, phase := range []string{"command", "thinking", "tool", "compaction", "done", "offline"} {
				t.Run(fmt.Sprintf("dark=%v/timed=%v/%s", dark, timed, phase), func(t *testing.T) {
					v, ui := latestMotionFixture()
					if !timed {
						d := v.Model.Details["s"]
						d.Turns[0].StartedAt = nil
						v.Model.Details["s"] = d
					}
					ui.SetDark(dark)
					latestMotionPhase(v, phase)
					ui.Frame()
					ui.Advance(time.Second)
					button, _ := ui.Find("回到最新")
					label := v.runningActivity(v.Model.Details["s"].Turns[0]).label
					if phase == "done" {
						label = "最新消息"
					} else if phase == "compaction" {
						label = "正在压缩上下文"
					} else if phase == "offline" {
						label = "状态待确认"
					}
					r, ok := ui.Find(label)
					if !ok {
						t.Fatal("missing status text", label)
					}
					im := ui.Image()
					bg := im.RGBAAt(int(button.X+button.W/2), int(button.Y+2))
					top, bottom := im.Bounds().Max.Y, -1
					for y := int(max(r.Y, button.Y+2)); y < int(min(r.Y+r.H, button.Y+button.H-2)); y++ {
						for x := int(r.X); x < int(r.X+r.W); x++ {
							p := im.RGBAAt(x, y)
							if math.Abs(float64(int(p.R)-int(bg.R))) > 30 {
								top, bottom = min(top, y), max(bottom, y)
							}
						}
					}
					saveNativeSkillImage(t, ui, fmt.Sprintf("before-%v-%v-%s.png", dark, timed, phase))
					if bottom < top || math.Abs(float64(float32(top+bottom+1)/2-button.Y-button.H/2)) > 1 {
						t.Fatal("status text is not vertically centered in capsule", label, button, r, top, bottom)
					}
				})
			}
		}
	}
}
