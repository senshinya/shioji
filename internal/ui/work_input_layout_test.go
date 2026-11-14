package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func childContinueFixture(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	v, ui := sessionWorkFixture(t)
	d := v.Model.Details["s"]
	d.Works[0].State = "completed"
	d.Works[0].CanContinue = true
	v.Model.Details["s"] = d
	if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	return v, ui
}

func TestChildContinuePromptIsCenteredAndOnlyLabelsTheField(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			_, ui := childContinueFixture(t)
			ui.SetDark(dark)
			ui.SetScale(2)
			ui.Advance(time.Second)
			frame, ok := ui.Find("子 Agent 输入区")
			if !ok {
				t.Fatal("missing input frame")
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("continue-%v-collapsed.png", dark))
			im := ui.Image()
			bg := im.RGBAAt(int((frame.X+frame.W/2)*2), int((frame.Y+frame.H/2)*2))
			top, bottom := im.Bounds().Max.Y, -1
			for x := int((frame.X + 6) * 2); x < int((frame.X+70)*2); x++ {
				for y := int((frame.Y + 3) * 2); y < int((frame.Y+frame.H-3)*2); y++ {
					p := im.RGBAAt(x, y)
					if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 36 {
						top = min(top, y)
						bottom = max(bottom, y)
					}
				}
			}
			if bottom < top {
				t.Fatal("continue prompt has no visible ink")
			}
			inkCenter := float64(top+bottom+1) / 4
			if delta := inkCenter - float64(frame.Y+frame.H/2); math.Abs(delta) > 1 {
				t.Errorf("collapsed prompt ink is off center by %.2f DIP", delta)
			}
			if err := ui.Click("继续执行"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			field, ok := ui.Find("子 Agent 消息输入")
			if !ok {
				t.Fatal("missing native field")
			}
			hint, ok := ui.Find("子 Agent 输入提示")
			if !ok {
				t.Fatal("missing continue placeholder")
			}
			if hint.Y < field.Y || hint.Y+hint.H > field.Y+field.H+.5 {
				t.Errorf("continue prompt is in footer instead of input: prompt=%v input=%v", hint, field)
			}
			count := 0
			for _, text := range ui.Texts() {
				if text == "继续执行" {
					count++
				}
			}
			if count != 0 {
				t.Errorf("continue prompt repeated %d times", count)
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("continue-%v-expanded.png", dark))
		})
	}
}

func TestChildContinuePromptMovesContinuouslyAndKeepsNativeEditing(t *testing.T) {
	v, ui := childContinueFixture(t)
	localHint := func() n.Rect {
		t.Helper()
		frame, _ := ui.Find("子 Agent 输入区")
		hint, ok := ui.Find("子 Agent 输入提示")
		if !ok {
			t.Fatal("missing shared input prompt")
		}
		hint.X -= frame.X
		hint.Y -= frame.Y
		return hint
	}
	start := localHint()
	ui.Click("继续执行")
	ui.Advance(60 * time.Millisecond)
	middle := localHint()
	ui.Advance(time.Second)
	end := localHint()
	if !(middle.Y > start.Y && middle.Y < end.Y) || middle.X <= start.X || middle.X >= end.X {
		t.Fatal("prompt jumps instead of moving into placeholder", start, middle, end)
	}
	if !ui.Focused("子 Agent 消息输入") {
		t.Fatal("opening did not focus native input")
	}
	ui.Click("收起子 Agent 输入")
	ui.Advance(60 * time.Millisecond)
	ui.Frame()
	closing := localHint()
	if closing.Y <= start.Y || closing.Y >= end.Y {
		t.Fatal("prompt did not move back while closing", start, closing, end)
	}
	// Reverse before collapse completes: the shared text must continue from here.
	v.workPanel("agents").detail("agent-a").input.open = true
	ui.Frame()
	reversed := localHint()
	if math.Abs(float64(closing.Y-reversed.Y)) > .1 {
		t.Fatal("rapid reversal jumped the prompt", closing, reversed)
	}
	ui.Advance(time.Second)
	ui.Click("子 Agent 消息输入")
	ui.Type("补充测试\n保留草稿")
	if got := v.workPanel("agents").detail("agent-a").input.draft; got != "补充测试\n保留草稿" {
		t.Fatal("native editing lost text", got)
	}
	ui.Click("收起子 Agent 输入")
	ui.Advance(time.Second)
	ui.Click("继续执行")
	ui.Advance(time.Second)
	if got := v.workPanel("agents").detail("agent-a").input.draft; got != "补充测试\n保留草稿" {
		t.Fatal("disclosure discarded child draft", got)
	}
	if v.Model.Drafts["session:s"] != "keep draft" {
		t.Fatal("child edit changed parent draft")
	}
}
