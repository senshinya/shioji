package ui

import (
	"fmt"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestTurnUsageIsStaticOnHoverAndClick(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			input, output, cached := int64(882383), int64(3932), int64(847616)
			turn := domain.Turn{ID: "t", Status: "completed", InputTokens: &input, OutputTokens: &output, CachedInputTokens: &cached}
			ui := n.NewTester(func(c *n.Context) {
				theme := applyAppearance(c, v.Model.Preferences)
				n.Column(c).Fill().Padding(24).Background(theme.Background).Children(func() { v.turnFooter(c, theme, turn) })
			}, 680, 220)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			usage, ok := ui.Find("本轮用量")
			if !ok || !ui.HasText("输入 882.4k · 输出 3.9k · 缓存 847.6k") {
				t.Fatal("usage summary missing")
			}
			before := copyFrame(ui)
			saveNativeSkillImage(t, ui, fmt.Sprintf("usage-%v-rest.png", dark))
			ui.Move(usage.X+usage.W/2, usage.Y+usage.H/2)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("usage-%v-hover.png", dark))
			unchanged := func(stage string) {
				t.Helper()
				after := ui.Image()
				for y := 0; y < 220; y++ {
					for x := 0; x < 680; x++ {
						if before.RGBAAt(x, y) != after.RGBAAt(x, y) {
							t.Errorf("%s changes static usage pixels", stage)
							return
						}
					}
				}
			}
			unchanged("hover")
			if ui.Cursor() != n.CursorDefault {
				t.Error("usage hover still changes the cursor", ui.Cursor())
			}
			if err := ui.Click("本轮用量"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("usage-%v-click.png", dark))
			unchanged("click")
			if ui.HasText("输入 882383") || ui.HasText("输出 3932") || ui.Focused("本轮用量") {
				t.Error("usage still exposes an interactive control or details popup")
			}
		})
	}
}

func TestInterleavedLateOutputHasOneFooterPerTurn(t *testing.T) {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	d.Items = []domain.Item{{ID: "first", Kind: "assistant", TurnID: "old", Text: "旧轮正文", Final: true}, {ID: "second", Kind: "assistant", TurnID: "t", Text: "当前正文", Final: true}, {ID: "late", Kind: "assistant", TurnID: "old", Text: "旧轮迟到输出", Final: true}, {ID: "tool", Kind: "tool", TurnID: "t", Name: "当前轮工具", Final: true}}
	turns := map[string]domain.Turn{"old": {ID: "old", Status: "completed"}, "t": {ID: "t", Status: "running"}}
	v.Model.Details["s"] = d
	rows := withTurnFooters(v.transcriptRows(d.Items), turns)
	count := map[string]int{}
	for _, row := range rows {
		if row.footer != "" {
			count[row.footer]++
		}
	}
	if count["old"] != 1 || count["t"] != 1 {
		t.Fatal("interleaved turns create duplicate native footer identities", count)
	}
}

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

func TestInsertedInputKeepsNativeReplyFooterOnEachSide(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			m := layoutModel()
			if dark {
				m.Preferences.Appearance = "dark"
			}
			m.WorkerMinor = 9
			m.Readiness.Capabilities.Fork = true
			d := m.Details["s"]
			d.Session.ThreadID = "thread"
			start := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
			inserted, ended := start.Add(80*time.Second), start.Add(3*time.Minute)
			duration, input := ended.Sub(start).Milliseconds(), int64(100)
			items := []domain.Item{
				{ID: "prompt", TurnID: "t", Kind: "user", Text: "Original question", Status: "sent", SentAt: &start},
				{ID: "a", TurnID: "t", Kind: "assistant", Text: "**First part**", Final: true},
				{ID: "tool", TurnID: "t", Kind: "tool", Name: "command", Text: "Tool output", Final: true},
				{ID: "b", TurnID: "t", Kind: "assistant", Text: "More before insertion", Final: true},
				{ID: "steer", TurnID: "t", Kind: "user", Text: "Inserted question", Status: "dispatching", SentAt: &inserted},
				{ID: "c", TurnID: "t", Kind: "assistant", Text: "Reply after insertion", Final: true},
			}
			d.Items = items[:4]
			d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &start}}
			d.Session.Status = domain.ExecutionRunning
			m.Details["s"], m.Snapshot.Sessions[0] = d, d.Session
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1080, 900)
			ui.SetDark(dark)
			record := os.Getenv("SHIOJI_REPLY_FOOTER_RECORDING") != ""
			frame := 0
			pause := func(frames int) {
				t.Helper()
				if !record {
					ui.Advance(time.Duration(frames) * (time.Second / 30))
					return
				}
				for range frames {
					ui.Advance(time.Second / 30)
					saveNativeSkillImage(t, ui, fmt.Sprintf("inserted-reply-%v-%04d.png", dark, frame))
					frame++
				}
			}
			pause(24)
			d.Items = items[:5]
			m.Details["s"] = d
			ui.Frame()
			pause(21)
			if ui.HasText("轮次中途无法分叉") {
				t.Fatal("an unconfirmed insertion created a reply footer")
			}

			d.Items[4].Status = "sent"
			m.Details["s"] = d
			ui.Frame()
			pause(30)
			saveNativeSkillImage(t, ui, fmt.Sprintf("inserted-reply-live-%v.png", dark))
			if !ui.HasText("轮次中途无法分叉") || !ui.HasText("进行中") {
				t.Fatal("the reply footer and running status must coexist after an accepted insertion", ui.Texts())
			}
			d.Items = items
			m.Details["s"] = d
			ui.Frame()
			pause(24)

			d.Turns[0] = domain.Turn{ID: "t", Status: domain.ExecutionCompleted, StartedAt: &start, CompletedAt: &ended, DurationMS: &duration, InputTokens: &input}
			d.Session.Status = domain.ExecutionCompleted
			m.Details["s"], m.Snapshot.Sessions[0] = d, d.Session
			ui.Frame()
			pause(30)
			saveNativeSkillImage(t, ui, fmt.Sprintf("inserted-reply-completed-%v.png", dark))

			first := v.historyRowBounds["reply-before:steer"]
			last := v.historyRowBounds["turn-footer:t"]
			if first.H <= 0 || first.Y <= v.historyRowBounds["b"].Y || first.Y >= v.historyRowBounds["steer"].Y || last.Y <= v.historyRowBounds["c"].Y {
				t.Fatal("reply footers must follow the replies on each side of the inserted message", first, last)
			}
			cutoff, cutoffOK := ui.Find("结束时间：12:01")
			fork, forkOK := ui.Find("轮次中途无法分叉")
			terminalFork, terminalForkOK := ui.Find("从这里分叉")
			if !cutoffOK || !forkOK || !terminalForkOK || fork.X >= cutoff.X || terminalFork.Y <= fork.Y || !ui.HasText("用时 1分20秒") || !ui.HasText("用时 3分0秒") {
				t.Fatal("missing segment metrics or fork affordance", ui.Texts())
			}
			var usageCount int
			for _, text := range ui.Texts() {
				if text == "本轮用量" {
					usageCount++
				}
			}
			if usageCount != 1 || !ui.HasText("输入 100 · 输出 — · 缓存 —") {
				t.Fatal("turn usage must appear only after the terminal reply", ui.Texts())
			}
			ui.Move(fork.X+fork.W/2, fork.Y+fork.H/2)
			pause(36)
			if !ui.HasText("只支持在轮次边界分叉") {
				t.Fatal("disabled fork has no explanation")
			}
			if err := ui.Click("复制回复"); err != nil {
				t.Fatal(err)
			}
			if got := ui.Clipboard(); got != "**First part**\n\nMore before insertion" {
				t.Fatal("copy crossed the insertion boundary", got)
			}
			pause(24)
			copy, _ := ui.Find("复制回复")
			ui.ClickAt(copy.X+copy.W/2, terminalFork.Y+terminalFork.H/2)
			if got := ui.Clipboard(); got != "Reply after insertion" {
				t.Fatal("copy after the insertion included earlier text", got)
			}
			pause(30)
		})
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
