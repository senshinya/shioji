package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestCompletedFooterShowsCopyEndTimeDurationAndUsageInOrder(t *testing.T) {
	m := layoutModel()
	ended := time.Date(2026, 11, 5, 23, 34, 56, 0, time.Local)
	duration, input, output, cached := int64(125900), int64(1800), int64(320), int64(640)
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "reply", TurnID: "t", Kind: "assistant", Text: "Reply.", Final: true}}
	d.Turns = []domain.Turn{{ID: "t", Status: "completed", CompletedAt: &ended, DurationMS: &duration, InputTokens: &input, OutputTokens: &output, CachedInputTokens: &cached}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	var right float32
	for _, label := range []string{"复制回复", "从这里分叉", "结束时间：23:34", "用时 2分5秒", "本轮用量"} {
		r, ok := ui.Find(label)
		if !ok || right > 0 && r.X-right < 7.5 {
			t.Fatalf("footer field missing or out of order: %s %+v", label, r)
		}
		right = r.X + r.W
	}
	if ui.HasText("本轮完成") || !ui.HasText("输入 1.8k · 输出 320 · 缓存 640") {
		t.Fatal("completed footer still uses completion label or loses usage")
	}
}

func TestCopyReplyPreservesMarkdownAndExcludesOtherTurnAndTools(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{
		{ID: "old", TurnID: "old", Kind: "assistant", Text: "Earlier reply.", Final: true},
		{ID: "user", TurnID: "t", Kind: "user", Text: "Do the work.", Final: true},
		{ID: "a", TurnID: "t", Kind: "assistant", Text: "First paragraph.", Final: true},
		{ID: "tool", TurnID: "t", Kind: "tool", Name: "command", Text: "private tool output", Final: true},
		{ID: "b", TurnID: "t", Kind: "assistant", Text: "**Result**\n\n```go\nreturn 1\n```", Final: true},
	}
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "ordinary draft"
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	ui.SetClipboard("previous clipboard")
	if err := ui.Click("复制回复"); err != nil {
		t.Fatal(err)
	}
	want := "First paragraph.\n\n**Result**\n\n```go\nreturn 1\n```"
	if got := ui.Clipboard(); got != want {
		t.Fatalf("wrong reply copied: %q", got)
	}
	if m.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("copy changes composer draft")
	}
}

func TestCompletedFooterLeavesMissingMetricsUnknown(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "reply", TurnID: "t", Kind: "assistant", Text: "Saved reply", Final: true}}
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if !ui.HasText("用时 —") || !ui.HasText("输入 — · 输出 — · 缓存 —") {
		t.Fatal("unknown metrics were hidden or fabricated")
	}
	if _, ok := ui.Find("结束时间：—"); !ok {
		t.Fatal("missing end-time placeholder")
	}
}

func TestCopyWithoutAssistantTextKeepsClipboard(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "tool", TurnID: "t", Kind: "tool", Name: "command", Text: "tool output", Final: true}}
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	ui.SetClipboard("keep clipboard")
	if err := ui.Click("复制回复"); err != nil {
		t.Fatal(err)
	}
	if ui.HasText("已复制") {
		t.Fatal("empty reply reports a successful copy")
	}
	if got := ui.Clipboard(); got != "keep clipboard" {
		t.Fatal("empty reply erased clipboard", got)
	}
}

func TestFooterCopyIconAlignsWithReplyInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		d := m.Details["s"]
		d.Items = []domain.Item{{ID: "reply", TurnID: "t", Kind: "assistant", Text: "Reply.", Final: true}}
		ended := time.Date(2026, 11, 5, 23, 34, 0, 0, time.Local)
		d.Turns = []domain.Turn{{ID: "t", Status: "completed", CompletedAt: &ended}}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		inkLeft := func(r n.Rect) int {
			im := ui.Image()
			bg := im.RGBAAt(int(r.X)-2, int(r.Y+r.H/2))
			for x := int(r.X); x < int(r.X+r.W); x++ {
				for y := int(r.Y); y < int(r.Y+r.H); y++ {
					p := im.RGBAAt(x, y)
					delta := int(p.R) - int(bg.R)
					if delta > 20 || delta < -20 {
						return x
					}
				}
			}
			t.Fatal("missing rendered ink", r)
			return 0
		}
		reply, _ := ui.Find("Reply.")
		copy, _ := ui.Find("复制回复")
		inkCenterY := func(r n.Rect) float32 {
			im := ui.Image()
			bg := im.RGBAAt(int(r.X)-2, int(r.Y+r.H/2))
			top, bottom := im.Bounds().Max.Y, -1
			for y := int(r.Y); y < int(r.Y+r.H); y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					p := im.RGBAAt(x, y)
					delta := int(p.R) - int(bg.R)
					if delta > 20 || delta < -20 {
						top = min(top, y)
						bottom = max(bottom, y)
					}
				}
			}
			if bottom < 0 {
				t.Fatal("missing footer ink", r)
			}
			return float32(top+bottom) / 2
		}
		endedText, _ := ui.Find("结束时间：23:34")
		vertical := inkCenterY(copy) - inkCenterY(endedText)
		if vertical > 1 || vertical < -1 {
			t.Fatal("copy icon is not vertically centered with end time", dark, vertical)
		}
		delta := inkLeft(copy) - inkLeft(reply)
		if delta > 2 || delta < -2 {
			t.Fatal("copy glyph is inset from message text", dark, delta, reply, copy)
		}
	}
}

func TestCopyReplyShowsTemporaryCheckAndRestartsItsTimer(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		m := layoutModel()
		d := m.Details["s"]
		d.Items = []domain.Item{{ID: "reply", TurnID: "t", Kind: "assistant", Text: "Copy this reply.", Final: true}}
		d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		before, _ := ui.Find("复制回复")
		if err := ui.Click("复制回复"); err != nil {
			t.Fatal(err)
		}
		if !ui.HasText("已复制") || ui.Clipboard() != "Copy this reply." {
			t.Fatal("copy gives no success feedback")
		}
		ui.Advance(1500 * time.Millisecond)
		if !ui.HasText("已复制") {
			t.Fatal("copy feedback disappears too early")
		}
		ui.Click("复制回复")
		ui.Advance(time.Second)
		if !ui.HasText("已复制") {
			t.Fatal("repeated copy does not extend feedback")
		}
		during, _ := ui.Find("复制回复")
		ui.Advance(1200 * time.Millisecond)
		after, _ := ui.Find("复制回复")
		if ui.HasText("已复制") {
			t.Fatal("copy icon does not restore after the delay")
		}
		if before != during || during != after {
			t.Fatal("copy feedback changes footer geometry", before, during, after)
		}
	}
}
