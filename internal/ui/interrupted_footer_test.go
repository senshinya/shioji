package ui

import (
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestInterruptedFooterKeepsStatusAndReplyActions(t *testing.T) {
	for _, status := range []domain.ExecutionState{domain.ExecutionInterrupted, domain.ExecutionAbandoned} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1440, 850} {
				t.Run(fmt.Sprintf("%s/dark=%v/width=%d", status, dark, width), func(t *testing.T) {
					m := layoutModel()
					d := m.Details["s"]
					ended := time.Date(2026, 11, 16, 15, 4, 5, 0, time.Local)
					duration, input, output := int64(125900), int64(1800), int64(320)
					d.Items = []domain.Item{
						{ID: "old", TurnID: "old", Kind: "assistant", Text: "Previous reply.", Final: true},
						{ID: "prompt", TurnID: "t", Kind: "user", Text: "继续检查"},
						{ID: "reply", TurnID: "t", Kind: "assistant", Text: "已完成的部分。\n\n**Still checking**"},
						{ID: "tool", TurnID: "t", Kind: "tool", Name: "command", Text: "private tool output", Final: true},
					}
					d.Turns = []domain.Turn{{ID: "t", Status: status, CompletedAt: &ended, DurationMS: &duration, InputTokens: &input, OutputTokens: &output}}
					d.Session.Status = status
					m.Details["s"] = d
					m.Snapshot.Sessions[0] = d.Session
					m.Drafts["session:s"] = "keep draft"
					ui := n.NewTester(New(m, nil, "").View, width, 900)
					ui.SetDark(dark)
					ui.Advance(time.Second)
					saveNativeSkillImage(t, ui, fmt.Sprintf("footer-%s-%v-%d.png", status, dark, width))
					for _, label := range []string{"复制回复", "从这里分叉", "结束时间：15:04", "用时 2分5秒", "本轮用量"} {
						if r, ok := ui.Find(label); !ok || r.W <= 0 || r.H <= 0 {
							t.Errorf("interrupted reply missing footer field %q", label)
						}
					}
					label := "已停止"
					if status == domain.ExecutionAbandoned {
						label = "已结束 · 结果未确认"
					}
					if !ui.HasText(label) || !ui.HasText("输入 1.8k · 输出 320 · 缓存 —") {
						t.Error("interrupted reply lost its status or reported usage", ui.Texts())
					}
					endedField, _ := ui.Find("结束时间：15:04")
					statusField, statusOK := ui.Find("回复状态：" + label)
					durationField, _ := ui.Find("用时 2分5秒")
					if !statusOK || statusField.X < endedField.X+endedField.W || durationField.X < statusField.X+statusField.W {
						t.Error("stopped status must follow end time and precede duration", endedField, statusField, durationField)
					}
					ui.SetClipboard("previous clipboard")
					if err := ui.Click("复制回复"); err != nil {
						t.Fatal(err)
					}
					if ui.Clipboard() != d.Items[2].Text || !ui.HasText("已复制") {
						t.Fatal("copy did not preserve the partial reply", ui.Clipboard())
					}
					if m.Drafts["session:s"] != "keep draft" {
						t.Fatal("copy changed the ordinary draft")
					}
				})
			}
		}
	}
}

func TestInterruptedFooterKeepsMissingMetricsUnknown(t *testing.T) {
	for _, status := range []domain.ExecutionState{domain.ExecutionInterrupted, domain.ExecutionAbandoned} {
		t.Run(string(status), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Children(func() { v.turnFooter(c, c.Theme(), domain.Turn{ID: "t", Status: status}) })
			}, 700, 120)
			if _, ok := ui.Find("结束时间：—"); !ok || !ui.HasText("用时 —") || !ui.HasText("输入 — · 输出 — · 缓存 —") {
				t.Fatal("interrupted reply hides or fabricates missing metrics", ui.Texts())
			}
		})
	}
}
