package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestLatestControlShowsWorkWhileTheOriginalStatusIsVisible(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := toolTailFixture(76)
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		ui.Scroll(1000, 300, 0, -8)
		ui.Frame()
		ui.Advance(time.Second)
		button, shown := ui.Find("回到最新")
		if !shown {
			t.Fatal("reading above the end has no latest control")
		}
		status, shown := ui.Find("最新运行状态")
		if !shown || status.Y < button.Y || status.Y+status.H > button.Y+button.H+1 || ui.HasText("最新消息") {
			t.Fatal("working latest control still depends on the transcript status leaving the viewport", ui.Texts())
		}
		if !ui.HasText("正在执行命令") {
			t.Fatal("latest control does not describe the active command")
		}
	}
}

func TestLatestControlTracksTheDockAndKeepsTypingFocus(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := progressFollowFixture()
		ui.SetDark(dark)
		ui.Click("消息输入")
		ui.Scroll(800, 350, 0, -240)
		ui.Frame()
		ui.Advance(time.Second)
		check := func() {
			t.Helper()
			button, ok := ui.Find("回到最新")
			dock, _ := ui.Find("会话底部面板")
			if !ok || !(ui.HasText("最新消息") || ui.HasText("进行中")) || button.H != 28 || button.W < 65 || button.W > 160 || math.Abs(float64(button.X+button.W/2-dock.X-dock.W/2)) > .5 || math.Abs(float64(dock.Y-button.Y-button.H-12)) > .5 {
				t.Fatal("latest control is not a compact pill centered above the dock", button, dock)
			}
		}
		check()
		// Check the actual Chinese/icon baseline on the ordinary latest pill.
		// The running variant is covered by the native tail/proxy checks.
		done := v.Model.Details["s"]
		done.Turns[0].Status = "completed"
		v.Model.Details["s"] = done
		ui.Frame()
		ui.Advance(time.Second)
		caption, _ := ui.Find("最新消息")
		inkCenter := func(left, right float32) float32 {
			t.Helper()
			im := ui.Image()
			bg := im.RGBAAt(int(caption.X+caption.W+5), int(caption.Y+caption.H/2))
			top, bottom := im.Bounds().Max.Y, -1
			for y := int(caption.Y); y < int(caption.Y+caption.H); y++ {
				for x := int(left); x < int(right); x++ {
					p := im.RGBAAt(x, y)
					if math.Abs(float64(int(p.R)-int(bg.R))) > 25 {
						top, bottom = min(top, y), max(bottom, y)
					}
				}
			}
			if bottom < 0 {
				t.Fatal("latest control has no visible icon or text")
			}
			return float32(top+bottom) / 2
		}
		if delta := inkCenter(caption.X, caption.X+14) - inkCenter(caption.X+19, caption.X+caption.W); math.Abs(float64(delta)) > 1 {
			t.Fatal("latest icon does not align with the Chinese ink", dark, delta)
		}
		ui.Type(strings.Repeat("增加输入行\n", 5))
		for frame := 0; frame < 24; frame++ {
			ui.Advance(time.Second / 60)
			check()
		}
		d := v.Model.Details["s"]
		d.Requests = []domain.PendingRequest{{ID: "question", Kind: "questions", State: "pending", Title: "需要回答", Questions: []domain.Question{{ID: "q", Text: "选择下一步", Custom: true, Options: []domain.Option{{Label: "继续"}, {Label: "等待"}}}}}}
		v.Model.Details["s"] = d
		for frame := 0; frame < 24; frame++ {
			ui.Advance(time.Second / 60)
			check()
		}
		if err := ui.Click("回到最新"); err != nil {
			t.Fatal(err)
		}
		if !ui.Focused("消息输入") {
			t.Fatal("latest control steals the native editor's focus")
		}
		ui.Advance(time.Second)
		if !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() {
			t.Fatal("latest control did not resume following")
		}
	}
}

func TestLatestControlScrollsSmoothlyAndTheReaderCanInterrupt(t *testing.T) {
	v, ui := progressFollowFixture()
	ui.Scroll(800, 350, 0, -240)
	ui.Frame()
	ui.Advance(time.Second)
	before, _ := ui.Find("进度 30")
	ui.Click("回到最新")
	first, _ := ui.Find("进度 30")
	if math.Abs(float64(first.Y-before.Y)) > 1 || v.lists["s"].AtEnd() {
		t.Fatal("latest jumps to the end instead of gliding", before, first)
	}
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("进度 30")
	if middle.Y >= before.Y-1 || v.lists["s"].AtEnd() {
		t.Fatal("latest does not interpolate the scroll", before, middle)
	}
	ui.Scroll(800, 350, 0, -120)
	ui.Frame()
	stopped, _ := ui.Find("进度 30")
	ui.Advance(time.Second)
	after, _ := ui.Find("进度 30")
	if v.lists["s"].FollowingEnd() || math.Abs(float64(after.Y-stopped.Y)) > 1 {
		t.Fatal("latest keeps scrolling after the reader takes over", stopped, after)
	}
}

func TestLatestControlFadesInAndOut(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := progressFollowFixture()
		d := v.Model.Details["s"]
		d.Turns[0].Status = "completed"
		v.Model.Details["s"] = d
		ui.Frame()
		ui.SetDark(dark)
		ui.Scroll(800, 350, 0, -240)
		ui.Frame()
		ui.Advance(50 * time.Millisecond)
		caption, _ := ui.Find("最新消息")
		entering := toolLabelContrast(ui.Image(), caption, dark)
		ui.Advance(200 * time.Millisecond)
		caption, _ = ui.Find("最新消息")
		settled := toolLabelContrast(ui.Image(), caption, dark)
		if entering >= settled-3 {
			t.Fatal("latest caption appears without fading in", dark, entering, settled)
		}
		ui.Click("回到最新")
		// Starting the end glide resumes following immediately; the control
		// fades out then, rather than waiting for the viewport to arrive.
		ui.Advance(50 * time.Millisecond)
		exiting := toolLabelContrast(ui.Image(), caption, dark)
		ui.Advance(200 * time.Millisecond)
		gone := toolLabelContrast(ui.Image(), caption, dark)
		if exiting <= gone+3 || exiting >= settled-3 || ui.HasText("最新消息") {
			t.Fatal("latest caption disappears without fading out", dark, settled, exiting, gone)
		}
	}
}
