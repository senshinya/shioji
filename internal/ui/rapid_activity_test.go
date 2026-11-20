package ui

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// A short command must not start a width excursion in an otherwise quiet
// progress row. This exercises the real native layout in both owning surfaces.
func TestShortCommandKeepsProgressCaptionStable(t *testing.T) {
	for _, kind := range []string{"footer", "tools"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dark=%v", kind, dark), func(t *testing.T) {
				v, ui := runningCaptionFixture(kind)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				before, _ := ui.Find("运行时长")
				v.runningProgress.label = "正在执行命令"
				ui.Frame()
				for frame := 0; frame < 5; frame++ {
					ui.Advance(time.Second / 60)
					clock, _ := ui.Find("运行时长")
					if math.Abs(float64(clock.X-before.X)) > 1 {
						t.Errorf("short command expands progress before it is worth presenting: before=%v current=%v", before, clock)
						break
					}
				}
				v.runningProgress.label = "进行中"
				ui.Frame()
				for frame := 0; frame < 30; frame++ {
					ui.Advance(time.Second / 60)
					clock, _ := ui.Find("运行时长")
					if math.Abs(float64(clock.X-before.X)) > 1 {
						t.Fatal("finished short command leaves a width excursion", before, clock)
					}
				}
			})
		}
	}
}

func TestTransientActivityCaptionDoesNotInterruptItsEntrance(t *testing.T) {
	for _, surface := range []string{"footer", "tools"} {
		for _, label := range []string{"正在执行命令", "正在执行工具", "正在思考"} {
			t.Run(surface+"/"+label, func(t *testing.T) {
				v, ui := runningCaptionFixture(surface)
				quiet, _ := ui.Find("运行时长")
				next := "进行中"
				if surface == "tools" {
					next = "正在回复"
				}
				set := func(phase string) {
					v.runningProgress.label = phase
					ui.Frame()
				}
				set(label)
				ui.Advance(80 * time.Millisecond)
				if ui.HasText(label) {
					t.Error("brief phase started a caption transition")
				}
				set("进行中")
				ui.Advance(400 * time.Millisecond)

				set(label)
				ui.Advance(210 * time.Millisecond)
				if !ui.HasText(label) {
					t.Fatal("sustained phase never started its entrance")
				}
				set(next)
				ui.Advance(175 * time.Millisecond)
				clock, _ := ui.Find("运行时长")
				if !ui.HasText(label) || clock.X < quiet.X+9 {
					t.Error("phase ending during its entrance interrupted the caption and clock", quiet, clock)
				}
				ui.Advance(370 * time.Millisecond)
				if !ui.HasText(label) {
					t.Error("caption did not remain readable after its entrance")
				}
				ui.Advance(650 * time.Millisecond)
				if !ui.HasText(next) || ui.HasText(label) {
					t.Fatal("settled caption did not catch up to the current phase")
				}
			})
		}
	}
}

func TestToolCaptionCoalescesBurstsAndReleasesCompletedActivity(t *testing.T) {
	for _, kind := range []string{"footer", "tools"} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reduced=%v", kind, reduced), func(t *testing.T) {
				v, ui := runningCaptionFixture(kind)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				ui.Frame()
				before, _ := ui.Find("运行时长")
				for burst := 0; burst < 3; burst++ {
					v.runningProgress.label = "正在执行命令"
					ui.Frame()
					ui.Advance(80 * time.Millisecond)
					v.runningProgress.label = "进行中"
					ui.Frame()
					ui.Advance(50 * time.Millisecond)
					clock, _ := ui.Find("运行时长")
					if math.Abs(float64(clock.X-before.X)) > 1 {
						t.Fatal("rapid bursts accumulate a pending caption", before, clock)
					}
				}
				v.runningProgress.label = "正在执行命令"
				ui.Frame()
				ui.Advance(160 * time.Millisecond)
				if ui.HasText("正在执行命令") {
					t.Fatal("tool caption promoted before sustained activity")
				}
				ui.Advance(820 * time.Millisecond)
				clock, _ := ui.Find("运行时长")
				if !ui.HasText("正在执行命令") || clock.X < before.X+20 {
					t.Fatal("sustained command never receives its full caption", ui.Texts(), before, clock)
				}
				v.runningProgress.label = "进行中"
				ui.Frame()
				if !ui.HasText("进行中") || ui.HasText("正在执行命令") {
					t.Fatal("completion retains an active command caption", ui.Texts())
				}
				ui.Advance(time.Second)
				clock, _ = ui.Find("运行时长")
				if math.Abs(float64(clock.X-before.X)) > 1 {
					t.Fatal("completed activity never reclaims caption width", before, clock)
				}
				v.runningProgress.label = "正在回复"
				ui.Frame()
				if !ui.HasText("正在回复") {
					t.Fatal("non-tool activity inherits the tool delay")
				}
			})
		}
	}
}

// Native production View; worker event timing is a fixture, with no agent or
// device mutation. Capture at 60 fps so sub-animation command bursts are visible.
func TestRapidActivityNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_RAPID_ACTIVITY_RECORDING")
	if dir == "" {
		t.Skip("opt-in native rapid activity evidence")
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", fmt.Sprintf("%s/%v", dir, dark))
			v := toolTailFixture(3)
			quiet := func() {
				d := v.Model.Details["s"]
				d.Items[len(d.Items)-1].Final = true
				v.Model.Details["s"] = d
			}
			quiet()
			ui := n.NewTester(v.View, 1200, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			for frame := 0; frame < 660; frame++ {
				switch frame {
				case 30, 48, 66, 150, 300:
					latestMotionPhase(v, "command")
				case 35, 53, 71, 225, 380:
					quiet()
				case 120:
					if err := ui.Click("3 次工具调用"); err != nil {
						t.Fatal(err)
					}
				case 240:
					if err := ui.Click("3 次工具调用"); err != nil {
						t.Fatal(err)
					}
				case 255, 267, 279:
					latestMotionPhase(v, "tool")
				case 260, 272, 284:
					quiet()
				case 330:
					if err := ui.Click("3 次工具调用"); err != nil {
						t.Fatal(err)
					}
				case 360:
					d := v.Model.Details["s"]
					d.Items = append(d.Items, domain.Item{ID: "fast-added", TurnID: "t", Kind: "tool", Presentation: "command", Name: "pwd", Text: "/workspace", Final: true})
					v.Model.Details["s"] = d
				case 390:
					d := v.Model.Details["s"]
					d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "检查完成，正在整理结果。"})
					v.Model.Details["s"] = d
				case 420:
					d := v.Model.Details["s"]
					sent := ui.Now()
					d.Items = append(d.Items, domain.Item{ID: "steer", TurnID: "t", Kind: "user", Text: "再检查一次状态交接。", SentAt: &sent, Final: true})
					v.Model.Details["s"] = d
				case 435:
					d := v.Model.Details["s"]
					d.Items = append(d.Items, domain.Item{ID: "after-steer", TurnID: "t", Kind: "tool", Presentation: "command", Name: "pwd", Status: "running"})
					v.Model.Details["s"] = d
				case 440:
					quiet()
				case 450:
					d := v.Model.Details["s"]
					d.Requests = []domain.PendingRequest{{ID: "question", TurnID: "t", SessionID: "s", Kind: "questions", State: "pending", Title: "需要回答", Questions: []domain.Question{{ID: "continue", Text: "继续检查吗？", Custom: true}}}}
					d.Items = append(d.Items, domain.Item{ID: "question", TurnID: "t", Kind: "request", Name: "需要回答", Status: "pending"})
					v.Model.Details["s"] = d
				case 480:
					d := v.Model.Details["s"]
					d.Requests = nil
					d.Items[len(d.Items)-1].Status = "completed"
					d.Items = append(d.Items, domain.Item{ID: "compaction", TurnID: "t", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文", Status: "running"})
					v.Model.Details["s"] = d
				case 510:
					quiet()
				case 530:
					d := v.Model.Details["s"]
					d.Items = append(d.Items, domain.Item{ID: "last-command", TurnID: "t", Kind: "tool", Presentation: "command", Name: "git status --short", Status: "running"})
					v.Model.Details["s"] = d
				case 535:
					quiet()
				case 540:
					d := v.Model.Details["s"]
					d.Items = append(d.Items, domain.Item{ID: "final-reply", TurnID: "t", Kind: "assistant", Text: "状态交接检查完成。"})
					v.Model.Details["s"] = d
				case 600:
					d := v.Model.Details["s"]
					d.Items[len(d.Items)-1].Final = true
					d.Turns[0].Status, d.Session.Status = "completed", "completed"
					v.Model.Details["s"] = d
				}
				ui.Frame()
				ui.Advance(time.Second / 60)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
		})
	}
}

func TestToolHandoffKeepsItsPresentedCaption(t *testing.T) {
	v := toolTailFixture(2)
	d := v.Model.Details["s"]
	d.Items[len(d.Items)-1].Final = true
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	latestMotionPhase(v, "command")
	ui.Frame()
	ui.Advance(80 * time.Millisecond)
	d = v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "整理检查结果。"})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	count := 0
	for _, label := range ui.Texts() {
		if label == "正在回复" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("outgoing tool caption adopted the new footer's phase", ui.Texts())
	}
}
