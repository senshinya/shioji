package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestToolPhaseChangeAtBottomKeepsReadingPosition(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := progressFollowFixture()
		ui.SetDark(dark)
		v.Model.Preferences.CollapseLatestTools = true
		d := v.Model.Details["s"]
		d.Items = append(d.Items, domain.Item{ID: "tool", TurnID: "turn", Kind: "tool", Presentation: "mcp", Name: "检查执行设备", Status: "completed", Final: true})
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		before, ok := ui.Find("进度 35")
		if !ok || !ui.HasText("进行中") {
			t.Fatal("fixture has no quiet tool status")
		}
		d.Items[len(d.Items)-1].Status, d.Items[len(d.Items)-1].Final = "running", false
		v.Model.Details["s"] = d
		ui.Frame()
		for frame := 0; frame < 60; frame++ {
			current, visible := ui.Find("进度 35")
			if !visible || math.Abs(float64(current.Y-before.Y)) > 1 {
				t.Errorf("status-only change moves existing content: frame=%d before=%+v after=%+v", frame, before, current)
				break
			}
			ui.Advance(time.Second / 60)
		}
	}
}

// A compact progress owner replaces the running status in the same output slot;
// changing its owner must not scroll existing content in either direction.
func TestProgressReplacementAtBottomKeepsReadingPosition(t *testing.T) {
	for _, kind := range []string{"tool", "compaction"} {
		for _, dark := range []bool{false, true} {
			for _, reduced := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/dark=%v/reduced=%v", kind, dark, reduced), func(t *testing.T) {
					v, ui := progressFollowFixture()
					ui.SetDark(dark)
					v.Model.Preferences.CollapseLatestTools = true
					ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
					ui.Advance(time.Second)
					ui.Scroll(800, 350, 0, 10000)
					ui.Frame()
					ui.Advance(time.Second)
					before, ok := ui.Find("进度 35")
					if !ok || !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() {
						t.Fatal("fixture did not reach the bottom")
					}
					if dir := os.Getenv("SHIOJI_TOOL_START_CAPTURE"); dir != "" {
						t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprintf("%s-dark-%v-reduced-%v", kind, dark, reduced)))
					}
					saveNativeSkillImage(t, ui, "frame-0000.png")
					d := v.Model.Details["s"]
					item := domain.Item{ID: "tool", TurnID: "turn", Kind: "tool", Presentation: "mcp", Name: "检查执行设备", Status: "running"}
					label := "正在执行工具"
					if kind == "compaction" {
						item = domain.Item{ID: "compact", TurnID: "turn", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文", Status: "running"}
						label = "正在压缩上下文"
					}
					d.Items = append(d.Items, item)
					v.Model.Details["s"] = d
					ui.Frame()
					worst := float32(0)
					for frame := 0; frame < 60; frame++ {
						current, visible := ui.Find("进度 35")
						if !visible {
							t.Fatal("progress replacement loses the previous reading marker", frame)
						}
						worst = max(worst, float32(math.Abs(float64(current.Y-before.Y))))
						saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame+1))
						ui.Advance(time.Second / 60)
					}
					if worst > 1 {
						t.Errorf("progress replacement moves visible history: %g DIP", worst)
					}
					if !ui.HasText(label) || !v.lists["s"].FollowingEnd() || !v.lists["s"].AtEnd() {
						t.Fatal("progress replacement did not settle at its live status")
					}
				})
			}
		}
	}
}
