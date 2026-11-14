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

func TestToolGroupClosesOnFollowingNonToolContent(t *testing.T) {
	for _, kind := range []string{"request", "user", "activity", "request_result", "assistant"} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reduced=%v", kind, reduced), func(t *testing.T) {
				m := layoutModel()
				d := m.Details["s"]
				d.Items = nil
				d.Turns = []domain.Turn{{ID: "t", Status: "running"}}
				m.Details["s"] = d
				v := New(m, nil, "")
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				d.Items = []domain.Item{{ID: "tool", TurnID: "t", Kind: "tool", Presentation: "command", Name: "正在执行的命令", Text: "工具输出", Status: "running"}}
				m.Details["s"] = d
				ui.Frame()
				ui.Advance(time.Second)
				if !ui.HasText("正在执行的命令") {
					t.Fatal("live tool was not expanded")
				}
				heading, _ := ui.Find("执行工具")
				next := domain.Item{ID: "next", TurnID: "t", Kind: kind, Text: "后续内容", Status: "pending", Final: true}
				if kind == "activity" {
					next.Presentation = "reasoning"
				}
				if kind == "request_result" {
					next.Parts = []domain.ContentPart{{Text: "已确认的答案"}}
				}
				d.Items = append(d.Items, next)
				m.Details["s"] = d
				ui.Frame()
				initial := v.historyRowBounds["next"]
				if !reduced {
					saveNativeSkillImage(t, ui, kind+"-arrival.png")
				}
				if !m.ToolDisclosure("tool-group:s/tool").Closed() {
					t.Error("non-tool content did not immediately begin closing the group")
				}
				ui.Advance(40 * time.Millisecond)
				middle := v.historyRowBounds["next"]
				ui.Advance(220 * time.Millisecond)
				end := v.historyRowBounds["next"]
				if !reduced {
					saveNativeSkillImage(t, ui, kind+"-settled.png")
					if initial.H > 0 && !(initial.Y > middle.Y+.5 && middle.Y > end.Y+.5) {
						t.Error("automatic collapse does not shrink smoothly", initial, middle, end)
					}
				}
				if ui.HasText("正在执行的命令") {
					t.Error("tool remains expanded after subsequent non-tool content")
				}
				after, _ := ui.Find("1 次工具调用")
				if math.Abs(float64(after.Y-heading.Y)) > .5 {
					t.Error("automatic collapse does not replace the top title with the trailing summary", heading, after)
				}
				if t.Failed() {
					return
				}
				if err := ui.Click("1 次工具调用"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(200 * time.Millisecond)
				d.Items[0].Text = "迟到的工具输出"
				d.Items[0].Final = true
				d.Items[0].Status = "completed"
				d.Items = append(d.Items, domain.Item{ID: "later", TurnID: "t", Kind: "assistant", Text: "之后的正文", Final: true})
				m.Details["s"] = d
				ui.Frame()
				ui.Advance(time.Second)
				if !ui.HasText("迟到的工具输出") {
					t.Fatal("later content overrides the explicit reopen")
				}
			})
		}
	}
}

// Record real native frames of the live transitions with normalized test data.
func TestToolBoundaryNativeEvidence(t *testing.T) {
	dir := os.Getenv("SHIOJI_TOOL_BOUNDARY_CAPTURE")
	if dir == "" {
		t.Skip("set SHIOJI_TOOL_BOUNDARY_CAPTURE")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	for _, kind := range []string{"request", "user"} {
		m := layoutModel()
		d := m.Details["s"]
		d.Items = nil
		d.Turns = []domain.Turn{{ID: "t", Status: "running"}}
		m.Details["s"] = d
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(true)
		ui.Advance(time.Second)
		d.Items = []domain.Item{{ID: "tool", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go test ./internal/ui", Text: "正在检查工具组折叠和会话间距…", Status: "running"}}
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(time.Second)
		for frame := 0; frame < 90; frame++ {
			if frame == 24 {
				item := domain.Item{ID: "next", TurnID: "t", Kind: kind, Text: "工具调用期间插入的消息", Status: "pending", Final: true}
				if kind == "request" {
					item.Text = "来自工具的请求"
				}
				d.Items = append(d.Items, item)
				m.Details["s"] = d
				ui.Frame()
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("%s-frame-%03d.png", kind, frame))
			ui.Advance(time.Second / 60)
		}
	}
}

func TestEmptyRequestReceiptDoesNotInflateTranscriptSpacing(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = []domain.Item{
				{ID: "body", TurnID: "t", Kind: "assistant", Text: "会话行使用共同基线。\n标题、指示器和菜单保持对齐。", Final: true},
				{ID: "first-tool", TurnID: "t", Kind: "tool", Name: "第一组工具", Status: "completed", Final: true},
				{ID: "first-source", TurnID: "t", Kind: "request", Text: "来自工具的请求一", Status: "answered", Final: true},
				{ID: "empty-receipt", TurnID: "t", Kind: "request_result", Text: "请求结果", Status: "answered", Final: true},
				{ID: "second-tool", TurnID: "t", Kind: "tool", Name: "第二组工具", Status: "completed", Final: true},
				{ID: "third-tool", TurnID: "t", Kind: "tool", Name: "第三个工具", Status: "completed", Final: true},
				{ID: "second-source", TurnID: "t", Kind: "request", Text: "来自工具的请求二", Status: "resolved", Final: true},
			}
			d.Turns = []domain.Turn{{ID: "t", Status: "interrupted"}}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.SetScale(2)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("spacing-dark-%v.png", dark))
			labels := []string{"1 次工具调用", "来自工具的请求一 · 已处理", "2 次工具调用", "来自工具的请求二 · 已结束", "回复信息"}
			var previous n.Rect
			for i, label := range labels {
				box, ok := ui.Find(label)
				if !ok {
					t.Fatal("missing transcript element", label)
				}
				if i > 0 && math.Abs(float64(box.Y-previous.Y-28)) > .5 {
					t.Error("adjacent metadata has uneven spacing", labels[i-1], label, previous, box)
				}
				previous = box
			}
			// The authoritative receipt remains in history even when it has no
			// visible answer. Real answer receipts must keep their ordered row.
			d.Items[3].Parts = []domain.ContentPart{{Text: "保留真实答案"}}
			m.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			answer, ok := ui.Find("保留真实答案")
			first, _ := ui.Find(labels[1])
			second, _ := ui.Find(labels[2])
			if !ok || answer.Y <= first.Y || answer.Y >= second.Y {
				t.Fatal("nonempty receipt lost its position", first, answer, second)
			}
		})
	}
}
