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

func toolTailFixture(count int) *View {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	d.Items = []domain.Item{{ID: "u", TurnID: "t", Kind: "user", Text: "检查工具摘要和运行状态交接", Final: true}}
	for i := 0; i < count; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("call-", i), TurnID: "t", Kind: "tool", Presentation: "command", Name: fmt.Sprintf("go test ./internal/ui -run Check%d", i), Text: "ok   shioji/internal/ui", Status: "completed", Final: i != count-1})
	}
	d.Items[len(d.Items)-1].Status = "running"
	started := time.Now().Add(-75 * time.Second)
	d.Turns[0].StartedAt = &started
	v.Model.Details["s"] = d
	return v
}

func TestToolTailSummaryFollowsNativeTools(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := toolTailFixture(2)
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		title, titleOK := ui.Find("2 次工具调用")
		first, firstOK := ui.Find("go test ./internal/ui -run Check0")
		last, lastOK := ui.Find("go test ./internal/ui -run Check1")
		summary, summaryOK := ui.Find("工具组运行状态")
		if !titleOK || !firstOK || !lastOK || !summaryOK || title.Y >= first.Y || summary.Y < last.Y+last.H {
			t.Fatal("tool disclosure must have a count heading and trailing progress", title, first, last, summary)
		}
		phase, ok := ui.Find("正在执行命令")
		clock, clockOK := ui.Find("运行时长")
		if !ok || !clockOK || math.Abs(float64(phase.Y-summary.Y)) > 1 || math.Abs(float64(clock.Y-summary.Y)) > 1 {
			t.Fatal("trailing summary does not own live phase and elapsed time", summary, phase, clock)
		}
		if _, proxy := ui.Find("最新运行状态"); proxy {
			t.Fatal("visible trailing status also shows an offscreen proxy")
		}
		if err := ui.Click("2 次工具调用"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
		if ui.HasText("go test ./internal/ui -run Check0") || !ui.HasText("2 次工具调用") {
			t.Fatal("heading did not fold its tools", ui.Texts())
		}
	}
}

func TestToolTailOffscreenStatusReturnsWithoutTakingFocus(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := toolTailFixture(76)
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		ui.Click("消息输入")
		ui.Scroll(1000, 300, 0, -8)
		ui.Frame()
		ui.Advance(time.Second)
		if _, proxy := ui.Find("最新运行状态"); !proxy {
			t.Fatal("reading control omits current work while the original status is still visible")
		}
		ui.Scroll(1000, 300, 0, -280)
		ui.Frame()
		ui.Advance(time.Second)
		proxy, ok := ui.Find("最新运行状态")
		latest, buttonOK := ui.Find("回到最新")
		if !ok || !buttonOK || proxy.Y < latest.Y || proxy.Y+proxy.H > latest.Y+latest.H+1 {
			t.Fatal("reading tools has no live state in the existing latest control", proxy, latest)
		}
		if !ui.HasText("正在执行命令") {
			t.Fatal("offscreen control does not describe actual activity")
		}
		if err := ui.Click("回到最新"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
		if !v.lists["s"].FollowingEnd() || !ui.Focused("消息输入") {
			t.Fatal("activity proxy did not resume following while preserving typing focus")
		}
		if _, ok := ui.Find("最新运行状态"); ok {
			t.Fatal("activity proxy remains after returning to visible live summary")
		}
	}
}

func TestToolTailOffscreenProxyRespectsLifecycle(t *testing.T) {
	for _, state := range []string{"reply", "compaction", "waiting", "disconnected", "completed", "interrupted"} {
		t.Run(state, func(t *testing.T) {
			v := toolTailFixture(2)
			d := v.Model.Details["s"]
			var prefix []domain.Item
			for i := 0; i < 45; i++ {
				prefix = append(prefix, domain.Item{ID: fmt.Sprint("old-", i), Kind: "assistant", Text: "更早的阅读内容。", Final: true})
			}
			d.Items = append(prefix, d.Items...)
			want := ""
			switch state {
			case "reply":
				d.Items = append(d.Items, domain.Item{ID: "body", TurnID: "t", Kind: "assistant", Text: "正在输出新的回复。"})
				want = "正在回复"
			case "compaction":
				d.Items = append(d.Items, domain.Item{ID: "compact", TurnID: "t", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文"})
				want = "正在压缩上下文"
			case "waiting":
				d.Requests = []domain.PendingRequest{{ID: "q", TurnID: "t", SessionID: "s", Kind: "questions", State: "pending", Title: "需要回答", Questions: []domain.Question{{ID: "q1", Text: "继续检查吗？", Custom: true}}}}
			case "disconnected":
				v.Model.Connected = false

			default:
				d.Turns[0].Status = domain.ExecutionState(state)
				d.Session.Status = domain.ExecutionState(state)
			}
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			ui.Scroll(1000, 300, 0, -450)
			ui.Frame()
			ui.Advance(time.Second)
			proxy, shown := ui.Find("最新运行状态")
			if shown != (want != "") || shown && !ui.HasText(want) {
				t.Fatal("latest status contradicts the authoritative lifecycle", state, shown, ui.Texts())
			}
			if state == "disconnected" && (!ui.HasText("最新消息") || activityGreenIn(ui, proxy) || ui.HasText("运行时长")) {
				t.Fatal("disconnected proxy claims active work or invents a live timer")
			}
		})
	}
}

func TestToolTailDisclosureAnchorsTheTitle(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		v := largeToolDisclosureModel(76)
		d := v.Model.Details["s"]
		var prefix []domain.Item
		for i := 0; i < 30; i++ {
			prefix = append(prefix, domain.Item{ID: fmt.Sprint("earlier-", i), TurnID: "old", Kind: "assistant", Text: "更早的对话内容。", Final: true})
		}
		d.Items = append(prefix, d.Items...)
		// Leave real following content below the clicked control. Keeping it
		// near the top must not require synthetic trailing reading space.
		d.Items[len(d.Items)-1].Text = strings.Repeat("后续正文段落。\n\n", 30)
		v.Model.Details["s"] = d
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		ui.Advance(time.Second)
		for i, row := range v.transcriptRows(d.Items) {
			if row.group != "" {
				v.lists["s"].ScrollToRow(i+1, 300, n.Animation{Duration: 80 * time.Millisecond})
			}
		}
		ui.Frame()
		ui.Advance(time.Second)
		closed, _ := ui.Find("76 次工具调用")
		ui.Click("76 次工具调用")
		for i := 0; i < 16; i++ {
			ui.Advance(time.Second / 60)
			current, ok := ui.Find("76 次工具调用")
			if !ok || math.Abs(float64(current.Y-closed.Y)) > 1 {
				t.Fatal("opening does not hand the clicked place to its top title", closed, current)
			}
		}
		before, ok := ui.Find("76 次工具调用")
		if !ok {
			t.Fatal("expanded title did not remain visible")
		}
		if err := ui.Click("76 次工具调用"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 16; i++ {
			ui.Advance(time.Second / 60)
			label := "76 次工具调用"
			if v.toolMotions["tool-group:s/call0"] == nil {
				label = "76 次工具调用"
			}
			current, ok := ui.Find(label)
			if !ok || math.Abs(float64(current.Y-before.Y)) > 1 {
				t.Fatal("collapse lost its title anchor", before, current)
			}
		}
		ui.Advance(time.Second)
		if ui.HasText("large command 75") {
			t.Fatal("title disclosure did not hide its tools")
		}
	}
}

// A capture harness for the actual native View. The environment-controlled
// image helper is shared by the other native visual checks.
func TestToolTailNativeEvidence(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := toolTailFixture(8)
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, fmt.Sprintf("tail-expanded-%v.png", dark))
		ui.Click("8 次工具调用")
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, fmt.Sprintf("tail-collapsed-%v.png", dark))
		d := v.Model.Details["s"]
		d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "工具摘要保留在过程末尾，当前回复承接运行状态。"})
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, fmt.Sprintf("tail-reply-%v.png", dark))

		v = toolTailFixture(76)
		ui = n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, fmt.Sprintf("tail-many-%v.png", dark))
		ui.Scroll(1000, 300, 0, -380)
		ui.Move(1200, 790)
		ui.Frame()
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, fmt.Sprintf("tail-away-%v.png", dark))
	}
}

func TestToolTailNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_TOOL_TAIL_RECORDING")
	if dir == "" {
		t.Skip("set SHIOJI_TOOL_TAIL_RECORDING to capture native frames")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	v := toolTailFixture(8)
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(true)
	ui.Advance(time.Second)
	for frame := 0; frame < 600; frame++ {
		switch frame {
		case 60:
			if err := ui.Click("8 次工具调用"); err != nil {
				t.Fatal(err)
			}
		case 120:
			if err := ui.Click("8 次工具调用"); err != nil {
				t.Fatal(err)
			}
		case 180:
			if err := ui.Click("8 次工具调用"); err != nil {
				t.Fatal(err)
			}
		case 240:
			if err := ui.Click("8 次工具调用"); err != nil {
				t.Fatal(err)
			}
		case 300:
			d := v.Model.Details["s"]
			d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "工具摘要保留在过程末尾，当前回复承接运行状态。"})
			v.Model.Details["s"] = d
			ui.Frame()
		case 360:
			v = toolTailFixture(76)
			ui = n.NewTester(v.View, 1240, 820)
			ui.SetDark(true)
			ui.Advance(time.Second)
		case 390:
			ui.Scroll(1000, 300, 0, -380)
			ui.Frame()
		case 435:
			if err := ui.Click("回到最新"); err != nil {
				t.Fatal(err)
			}
		case 450:
			for i, row := range v.transcriptRows(v.Model.Details["s"].Items) {
				if row.toolTitle {
					v.lists["s"].ScrollToRow(i+1, 120, n.Animation{Duration: 80 * time.Millisecond})
					break
				}
			}
			ui.Frame()
		case 480:
			if err := ui.Click("76 次工具调用"); err != nil {
				t.Fatal(err)
			}
		case 540:
			if err := ui.Click("76 次工具调用"); err != nil {
				t.Fatal(err)
			}
		}
		ui.Move(1200, 790)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
		ui.Advance(time.Second / 60)
	}
}
