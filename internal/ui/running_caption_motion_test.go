package ui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func runningCaptionFixture(kind string) (*View, *n.Tester) {
	v := toolTailFixture(1)
	v.runningGroup = "tools"
	ui := n.NewTester(func(c *n.Context) {
		if v.runningProgress == nil {
			v.runningProgress = &runningProgress{started: c.Now(), label: "进行中"}
		}
		n.Column(c).Fill().Padding(16).Background(c.Theme().Background).Children(func() {
			if kind == "tools" {
				v.toolGroupTitle(c, c.Theme(), transcriptRow{group: "tools", count: 1, item: domain.Item{TurnID: "t"}}, 0)
			} else {
				v.progressStatus(c, c.Theme(), runningActivity{label: v.runningProgress.label, started: v.Model.Details["s"].Turns[0].StartedAt}, v.runningProgress)
			}
		})
	}, 600, 80)
	ui.Advance(time.Second)
	return v, ui
}

func TestConversationProgressMovesSmoothlyAsReplyGrows(t *testing.T) {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	d.Items = []domain.Item{{ID: "body", TurnID: "t", Kind: "assistant", Text: "初始回复。"}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	before, ok := ui.Find("回复信息")
	if !ok {
		t.Fatal("fixture has no live progress")
	}
	d.Items[0].Text += "\n\n" + strings.Repeat("新增回复内容。\n\n", 3)
	v.Model.Details["s"] = d
	ui.Frame()
	first, _ := ui.Find("回复信息")
	if math.Abs(float64(first.Y-before.Y)) > 1 {
		t.Fatal("streamed reply immediately jumps live progress", before, first)
	}
	previous, largest := first.Y, float32(0)
	for frame := 0; frame < 90; frame++ {
		ui.Advance(time.Second / 60)
		r, _ := ui.Find("回复信息")
		largest = max(largest, float32(math.Abs(float64(r.Y-previous))))
		previous = r.Y
	}
	if previous-before.Y < 40 || largest > 12 {
		t.Fatal("reply growth moves progress in discrete line-height steps", before.Y, previous, largest)
	}
}

func TestConversationProgressMovesSmoothlyWhenToolIsAdded(t *testing.T) {
	v := toolTailFixture(1)
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	before, _ := ui.Find("工具组运行状态")
	d := v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "second", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go vet ./...", Text: "checking", Status: "running"})
	v.Model.Details["s"] = d
	ui.Frame()
	first, _ := ui.Find("工具组运行状态")
	if math.Abs(float64(first.Y-before.Y)) > 1 {
		t.Fatal("new tool immediately jumps the live summary", before, first)
	}
	previous, largest, moving := first.Y, float32(0), 0
	for frame := 0; frame < 36; frame++ {
		ui.Advance(time.Second / 60)
		r, _ := ui.Find("工具组运行状态")
		step := float32(math.Abs(float64(r.Y - previous)))
		largest = max(largest, step)
		if step > .1 {
			moving++
		}
		previous = r.Y
	}
	if previous-before.Y < 20 || moving < 4 || largest > (previous-before.Y)/2 {
		t.Fatal("new tool jumps rather than moving the live summary", before.Y, previous, moving, largest)
	}
}

func TestConversationProgressMovesSmoothlyWhenFirstToolArrives(t *testing.T) {
	v := toolTailFixture(1)
	d := v.Model.Details["s"]
	d.Items = d.Items[:1]
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	before, _ := ui.Find("进行中状态")
	d.Items = append(d.Items, domain.Item{ID: "first", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go vet ./...", Text: "checking", Status: "running"})
	v.Model.Details["s"] = d
	ui.Frame()
	first, _ := ui.Find("进行中状态")
	if math.Abs(float64(first.Y-before.Y)) > 1 {
		t.Fatal("first tool jumps the live progress slot", before, first)
	}
	previous, largest, moving := first.Y, float32(0), 0
	for frame := 0; frame < 36; frame++ {
		ui.Advance(time.Second / 60)
		r, _ := ui.Find("进行中状态")
		step := float32(math.Abs(float64(r.Y - previous)))
		largest = max(largest, step)
		if step > .1 {
			moving++
		}
		previous = r.Y
	}
	if previous-before.Y < 20 || moving < 4 || largest > (previous-before.Y)/2 {
		t.Fatal("first tool moves live progress in discrete steps", before.Y, previous, moving, largest)
	}
}

func TestConversationProgressNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_PROGRESS_RECORDING")
	if dir == "" {
		t.Skip("opt-in native conversation progress recording")
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
			// A quiet all-finished group mounts folded by default; explicitly
			// open it before recording phase changes in both disclosure states.
			if !ui.HasText("go test ./internal/ui -run Check0") {
				if err := ui.Click("3 次工具调用"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
			}
			for frame := 0; frame < 300; frame++ {
				switch frame {
				case 15, 78, 84, 165:
					latestMotionPhase(v, "tool")
				case 45, 81, 135:
					quiet()
				case 60:
					d := v.Model.Details["s"]
					d.Items = append(d.Items, domain.Item{ID: "added-tool", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go vet ./...", Text: "检查代码", Status: "running"})
					v.Model.Details["s"] = d
				case 75, 87:
					latestMotionPhase(v, "command")
				case 105:
					if err := ui.Click("4 次工具调用"); err != nil {
						t.Fatal(err)
					}
				case 195:
					if err := ui.Click("4 次工具调用"); err != nil {
						t.Fatal(err)
					}
				case 225:
					d := v.Model.Details["s"]
					d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "检查完成，正在整理结果。"})
					v.Model.Details["s"] = d
				case 240:
					d := v.Model.Details["s"]
					d.Items[len(d.Items)-1].Text += "\n\n" + strings.Repeat("新增正文时，进度指示器随实际高度移动。\n\n", 3)
					v.Model.Details["s"] = d
				case 255:
					d := v.Model.Details["s"]
					d.Turns[0].Status, d.Session.Status = "completed", "completed"
					d.Items[len(d.Items)-1].Final = true
					v.Model.Details["s"] = d
				}
				ui.Frame()
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
		})
	}
}

func TestConversationProgressCaptionCrossfadesAndMovesClockSmoothly(t *testing.T) {
	for _, kind := range []string{"footer", "tools"} {
		t.Run(kind, func(t *testing.T) {
			v, ui := runningCaptionFixture(kind)
			before, _ := ui.Find("运行时长")
			v.runningProgress.label = "正在执行命令"
			ui.Frame()
			ui.Advance(180 * time.Millisecond)
			first, _ := ui.Find("运行时长")
			ui.Advance(60 * time.Millisecond)
			middle, _ := ui.Find("运行时长")
			ui.Advance(time.Second)
			end, _ := ui.Find("运行时长")
			if math.Abs(float64(first.X-before.X)) > 1 || middle.X <= before.X+1 || middle.X >= end.X-1 {
				t.Error("phase growth jumps the adjacent clock", before, first, middle, end)
			}
			r, _ := ui.Find("正在执行命令")
			pixels := ui.Image()
			v.runningProgress.label = "正在执行工具"
			ui.Frame()
			ui.Advance(180 * time.Millisecond)
			firstPixels := latestTextDifference(pixels, ui.Image(), r)
			ui.Advance(80 * time.Millisecond)
			middlePixels := ui.Image()
			ui.Advance(time.Second)
			endPixels := latestTextDifference(pixels, ui.Image(), r)
			if endPixels < 500 || firstPixels > endPixels/10 || latestTextDifference(middlePixels, ui.Image(), r) < 100 {
				t.Fatal("conversation phase text replaces immediately", firstPixels, endPixels)
			}
		})
	}
}
