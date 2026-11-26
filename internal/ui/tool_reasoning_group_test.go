package ui

import (
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestToolGroupMergesOnlyFollowingReasoningSummaries(t *testing.T) {
	const leadingSummary = "先查看会话记录。"
	const middleSummary = "确认 transcript.go 的渲染顺序。\n检查换行和图标对齐，再运行回归。"
	const trailingSummary = "核对测试结果后准备回复。"
	const command = "go test ./internal/ui"
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{
		{ID: "prompt", TurnID: "t", Kind: "user", Text: "检查会话记录的渲染顺序", Status: "sent"},
		{ID: "plan", TurnID: "t", Kind: "reasoning", Text: leadingSummary + "\n\n", Status: "completed", Final: true},
	}
	d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1180, 740)
	const scale = 2
	ui.SetScale(scale)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Advance(time.Second)
	if !ui.HasText("思考摘要") {
		t.Fatalf("leading reasoning summary did not appear standalone: %v", ui.Texts())
	}

	// Tool calls start a group and only merge subsequent reasoning summaries;
	// the earlier summary that already appeared remains standalone.
	d.Items = append(d.Items,
		domain.Item{ID: "read", TurnID: "t", Kind: "tool", Presentation: "files", Name: "读取 transcript.go", Status: "completed", Final: true},
		domain.Item{ID: "think", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Parts: []domain.ContentPart{{Kind: "text", Text: middleSummary + "\r\n \t\r\n"}}, Status: "completed", Final: true},
		domain.Item{ID: "request:run", TurnID: "t", Kind: "request", Presentation: domain.AutomaticApprovalPresentation, Text: "允许执行此命令？", Status: "resolved", Final: true},
		domain.Item{ID: "test", TurnID: "t", Kind: "tool", Presentation: "command", Name: command, Status: "completed", Final: true},
		domain.Item{ID: "wrapup", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: trailingSummary + "\n", Status: "running"},
	)
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "tool-reasoning-running.png")
	rows := v.transcriptRows(d.Items)
	groupCount := 0
	for _, r := range rows {
		if r.toolTitle {
			groupCount++
			if r.count != 2 {
				t.Errorf("group counts %d calls, want 2", r.count)
			}
		}
		if !r.toolTitle && !r.toolStatus && r.item.ID == "plan" && r.toolParent != "" {
			t.Errorf("leading reasoning was retroactively merged into a tool group: %+v", r)
		}
		if !r.toolTitle && !r.toolStatus && (r.item.ID == "think" || r.item.ID == "wrapup") && r.toolParent != "tool-group:s/read" {
			t.Errorf("following reasoning outside the tool group: %+v", r)
		}
	}
	if groupCount != 1 || !ui.HasText("2 次工具调用") || !ui.HasText("思考摘要") || ui.HasText(leadingSummary) || !ui.HasText(middleSummary) || !ui.HasText(trailingSummary) || ui.HasText("允许执行此命令？") {
		t.Fatalf("expected leading summary standalone and following summaries inside one tool group, got %d groups: %v", groupCount, ui.Texts())
	}
	for _, dark := range []bool{false, true} {
		ui.SetDark(dark)
		ui.Advance(time.Second)
		frame := ui.Image()
		measure := func(id string) (centerX, centerY, height float64) {
			box := v.historyRowBounds[id]
			background := frame.RGBAAt(int((box.X+toolTimelineInset-3)*scale), int((box.Y+10)*scale))
			icon := sidebarInkBounds(t, frame, n.Rect{X: box.X + toolTimelineInset, Y: box.Y, W: toolGlyphSlot, H: 20}, scale, background)
			return float64(icon.Min.X+icon.Max.X) / (2 * scale), float64(icon.Min.Y+icon.Max.Y)/(2*scale) - float64(box.Y), float64(icon.Dy()) / scale
		}
		x, y, h := measure("read")
		for _, id := range []string{"think", "wrapup"} {
			sx, sy, sh := measure(id)
			if math.Abs(sx-x) > 1 || math.Abs(sy-y) > 1 || math.Abs(sh-h) > 1 {
				t.Errorf("dark=%t: %s glyph center and height %.2f, %.2f, %.2f differ from the first tool line %.2f, %.2f, %.2f", dark, id, sx, sy, sh, x, y, h)
			}
		}
		if dark {
			saveNativeSkillImage(t, ui, "tool-reasoning-dark.png")
		}
	}
	ui.SetDark(false)
	before, ok := ui.Find(command)
	if !ok {
		t.Fatal("command after reasoning is missing")
	}
	d.Items[3].Parts[0].Text = middleSummary
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if after, ok := ui.Find(command); !ok || after.Y != before.Y {
		t.Fatalf("trailing line breaks moved the following command: %.1f → %.1f", before.Y, after.Y)
	}
	d.Items[6].Status, d.Items[6].Final = "completed", true
	d.Turns[0].Status = domain.ExecutionCompleted
	d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "已核对会话记录。", Final: true})
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "tool-reasoning-collapsed.png")
	if !m.ToolDisclosure("tool-group:s/read").Closed() || ui.HasText(middleSummary) || ui.HasText(trailingSummary) || !ui.HasText("思考摘要") {
		t.Fatal("assistant reply did not fold the tool group while keeping the leading summary standalone")
	}
	if err := ui.Click("思考摘要"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText(leadingSummary) {
		t.Fatal("standalone leading reasoning summary failed to expand independently")
	}
}
