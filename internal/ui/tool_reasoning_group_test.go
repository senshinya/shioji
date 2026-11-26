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

func TestToolGroupMergesLeadingAndFollowingReasoningSummaries(t *testing.T) {
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
	beforeHeading, ok := ui.Find("思考摘要")
	if !ok {
		t.Fatal("standalone leading reasoning heading bounds missing")
	}

	// Tool calls merge the preceding same-turn reasoning summary in-place and
	// include following reasoning summaries in the same tool group.
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
		if !r.toolTitle && !r.toolStatus && (r.item.ID == "plan" || r.item.ID == "think" || r.item.ID == "wrapup") && r.toolParent != "tool-group:s/read" {
			t.Errorf("reasoning summary outside the tool group: %+v", r)
		}
	}
	if groupCount != 1 || !ui.HasText("2 次工具调用") || ui.HasText("思考摘要") || !ui.HasText(leadingSummary) || !ui.HasText(middleSummary) || !ui.HasText(trailingSummary) || ui.HasText("允许执行此命令？") {
		t.Fatalf("expected leading and following summaries inside one tool group, got %d groups: %v", groupCount, ui.Texts())
	}
	afterHeading, ok := ui.Find("2 次工具调用")
	if !ok {
		t.Fatal("merged tool group heading missing")
	}
	if math.Abs(float64(afterHeading.Y-beforeHeading.Y)) > 1 {
		t.Fatalf("merged tool group did not transform in-place: before=%.1f after=%.1f", beforeHeading.Y, afterHeading.Y)
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
		for _, id := range []string{"plan", "think", "wrapup"} {
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
	if !m.ToolDisclosure("tool-group:s/read").Closed() || ui.HasText(leadingSummary) || ui.HasText(middleSummary) || ui.HasText(trailingSummary) {
		t.Fatal("assistant reply did not fold the merged tool group")
	}
	if err := ui.Click("2 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText(leadingSummary) || !ui.HasText(middleSummary) || !ui.HasText(trailingSummary) {
		t.Fatal("merged tool group failed to reopen with leading and following summaries")
	}
}

func findGreenIndicatorDot(ui *n.Tester) (n.Rect, int) {
	count := 0
	for _, text := range ui.Texts() {
		if text == "进行中状态" {
			count++
		}
	}
	dot, ok := ui.Find("进行中状态")
	if !ok {
		return n.Rect{}, count
	}
	return dot, count
}

func TestLeadingReasoningInPlaceMorphKeepsGreenIndicatorPosition(t *testing.T) {
	started := time.Now().Add(-3 * time.Second)
	type scenario struct {
		name            string
		collapseLatest  bool
		initialText     string
		initialFinal    bool
		expandBefore    bool
		expectFrame0Dot string // "same" = exact same Y as before merge
		monotonicDown   bool
	}
	cases := []scenario{
		{
			name:            "empty-live-to-collapsed",
			collapseLatest:  true,
			initialText:     "",
			initialFinal:    false,
			expectFrame0Dot: "same",
		},
		{
			name:            "empty-live-to-expanded",
			collapseLatest:  false,
			initialText:     "",
			initialFinal:    false,
			expectFrame0Dot: "same",
			monotonicDown:   true,
		},
		{
			name:            "streaming-open-to-expanded",
			collapseLatest:  false,
			initialText:     "先核对会话历史，准备执行工具调用。",
			initialFinal:    false,
			expectFrame0Dot: "same",
			monotonicDown:   true,
		},
		{
			name:            "streaming-open-to-collapsed",
			collapseLatest:  true,
			initialText:     "先核对会话历史，准备执行工具调用。",
			initialFinal:    false,
			expectFrame0Dot: "same",
		},
		{
			name:            "settled-collapsed-to-expanded",
			collapseLatest:  false,
			initialText:     "先分析目标文件，随后发起读取。",
			initialFinal:    true,
			expectFrame0Dot: "same",
			monotonicDown:   true,
		},
		{
			name:           "settled-collapsed-to-collapsed",
			collapseLatest: true,
			initialText:    "先分析目标文件，随后发起读取。",
			initialFinal:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := layoutModel()
			m.Preferences.CollapseLatestTools = tc.collapseLatest
			d := m.Details["s"]
			d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started}}
			status := "running"
			if tc.initialFinal {
				status = "completed"
			}
			d.Items = []domain.Item{
				{ID: "prompt", TurnID: "t", Kind: "user", Text: "检查构建状态", Status: "sent", Order: 1},
				{ID: "r1", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: tc.initialText, Status: status, Final: tc.initialFinal, Order: 2, ActivityOrder: 2},
			}
			m.Details["s"] = d

			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1180, 740)
			ui.SetScale(2)
			ui.SetPreferences(n.Preferences{ReduceMotion: false})
			ui.Frame()
			ui.Advance(400 * time.Millisecond)

			beforeHeading, ok := v.historyRowBounds["r1"]
			if !ok || beforeHeading.H <= 0 {
				t.Fatalf("missing standalone reasoning row bounds before merge: %+v", v.historyRowBounds)
			}
			beforeDot, beforeCount := findGreenIndicatorDot(ui)
			if beforeCount != 1 {
				t.Fatalf("expected exactly 1 green indicator dot before merge, got %d: %v", beforeCount, ui.Texts())
			}
			if tc.initialText == "" && !tc.initialFinal {
				if thinkingRect, ok := ui.Find("正在思考"); !ok {
					t.Fatalf("missing empty live reasoning label before merge: %v", ui.Texts())
				} else if math.Abs(float64(thinkingRect.Y-beforeDot.Y)) > 1.0 {
					t.Fatalf("empty live reasoning label misaligned vertically with green dot: dot=%+v label=%+v", beforeDot, thinkingRect)
				}
			}

			// Tool call arrives in the same turn and merges the leading reasoning in-place.
			d.Items = append(d.Items, domain.Item{
				ID: "t1", TurnID: "t", Kind: "tool", Presentation: "command",
				Name: "go test ./internal/ui", Status: "running", Final: false,
				Order: 3, ActivityOrder: 3,
			})
			m.Details["s"] = d
			ui.Frame()

			const groupKey = "tool-group:s/t1"
			if v.toolArrivals == nil || v.toolArrivals.groupKeys[groupKey] != "r1" {
				t.Fatalf("merged group did not retain leading reasoning row key r1: %+v", v.toolArrivals)
			}
			frame0Heading, ok := v.historyRowBounds[groupKey]
			if !ok {
				t.Fatalf("missing merged group heading bounds on frame 0: %+v", v.historyRowBounds)
			}
			if math.Abs(float64(frame0Heading.Y-beforeHeading.Y)) > 0.5 {
				t.Fatalf("heading jumped on frame 0: before=%.2f frame0=%.2f", beforeHeading.Y, frame0Heading.Y)
			}

			frame0Dot, frame0Count := findGreenIndicatorDot(ui)
			if tc.expectFrame0Dot == "same" {
				if frame0Count != 1 {
					t.Fatalf("expected exactly 1 green indicator dot on frame 0, got %d: %v", frame0Count, ui.Texts())
				}
				if math.Abs(float64(frame0Dot.Y-beforeDot.Y)) > 0.5 || math.Abs(float64(frame0Dot.X-beforeDot.X)) > 0.5 {
					t.Fatalf("green indicator jumped on frame 0: before=(%.2f,%.2f) frame0=(%.2f,%.2f)",
						beforeDot.X, beforeDot.Y, frame0Dot.X, frame0Dot.Y)
				}
			}

			// Step through the full morph animation and verify heading stability,
			// green-dot monotonicity, and zero duplicate dots once settled.
			prevDotY := beforeDot.Y
			steps := []time.Duration{16 * time.Millisecond, 50 * time.Millisecond, 64 * time.Millisecond, 70 * time.Millisecond, 70 * time.Millisecond, 200 * time.Millisecond}
			for _, step := range steps {
				ui.Advance(step)
				h, ok := v.historyRowBounds[groupKey]
				if !ok {
					t.Fatalf("missing group heading bounds after +%v", step)
				}
				if math.Abs(float64(h.Y-beforeHeading.Y)) > 0.5 {
					t.Fatalf("heading drifted after +%v: before=%.2f now=%.2f", step, beforeHeading.Y, h.Y)
				}
				dot, dotCount := findGreenIndicatorDot(ui)
				if tc.expectFrame0Dot == "same" && tc.monotonicDown {
					if dotCount == 0 {
						t.Fatalf("green indicator disappeared after +%v", step)
					}
					if dot.Y < prevDotY-0.5 {
						t.Fatalf("green indicator moved upward after +%v: prev=%.2f now=%.2f", step, prevDotY, dot.Y)
					}
					prevDotY = dot.Y
				}
				if tc.name == "empty-live-to-collapsed" {
					if dotCount != 1 {
						t.Fatalf("expected exactly 1 green dot during %s after +%v, got %d", tc.name, step, dotCount)
					}
					if math.Abs(float64(dot.Y-beforeDot.Y)) > 0.5 {
						t.Fatalf("green indicator moved during %s after +%v: before=%.2f now=%.2f", tc.name, step, beforeDot.Y, dot.Y)
					}
				}
			}

			_, settledCount := findGreenIndicatorDot(ui)
			if settledCount != 1 {
				t.Fatalf("expected exactly 1 green indicator dot after settle, got %d: %v", settledCount, ui.Texts())
			}
			if !ui.HasText("1 次工具调用") {
				t.Fatalf("expected merged tool group heading after settle: %v", ui.Texts())
			}
			if ui.HasText("思考摘要") {
				t.Fatalf("standalone reasoning heading remained visible after merge: %v", ui.Texts())
			}
		})
	}
}

func TestMultipleLeadingReasoningAndReduceMotionMergeInPlace(t *testing.T) {
	started := time.Now().Add(-2 * time.Second)
	m := layoutModel()
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started}}
	d.Items = []domain.Item{
		{ID: "prompt", TurnID: "t", Kind: "user", Text: "检查多段思考合并", Status: "sent", Order: 1},
		{ID: "r1", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: "第一段分析。", Status: "completed", Final: true, Order: 2},
		{ID: "r2", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: "第二段分析。", Status: "completed", Final: true, Order: 3},
	}
	m.Details["s"] = d

	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1180, 740)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Frame()
	ui.Advance(time.Second)

	beforeR1, ok := v.historyRowBounds["r1"]
	if !ok {
		t.Fatal("missing r1 bounds before merge")
	}

	d.Items = append(d.Items, domain.Item{
		ID: "t1", TurnID: "t", Kind: "tool", Presentation: "files",
		Name: "读取 view.go", Status: "running", Final: false, Order: 4, ActivityOrder: 4,
	})
	m.Details["s"] = d
	ui.Frame()

	const groupKey = "tool-group:s/t1"
	if v.toolsAnimating {
		t.Fatal("ReduceMotion should not keep morph animations active")
	}
	afterHeading, ok := v.historyRowBounds[groupKey]
	if !ok {
		t.Fatal("missing merged group heading bounds")
	}
	if math.Abs(float64(afterHeading.Y-beforeR1.Y)) > 0.5 {
		t.Fatalf("multiple leading summaries did not merge in-place at r1 Y: before=%.2f after=%.2f", beforeR1.Y, afterHeading.Y)
	}
	if !ui.HasText("1 次工具调用") || !ui.HasText("第一段分析。") || !ui.HasText("第二段分析。") || ui.HasText("思考摘要") {
		t.Fatalf("expected both leading summaries merged into one tool group: %v", ui.Texts())
	}
}

func TestLeadingReasoningMorphKeepsGreenIndicatorStationaryWhenFollowingBottom(t *testing.T) {
	started := time.Now().Add(-4 * time.Second)
	for _, tc := range []struct {
		name           string
		collapseLatest bool
		initialText    string
		initialFinal   bool
	}{
		{name: "empty-live-to-collapsed", collapseLatest: true},
		{name: "streaming-open-to-expanded", collapseLatest: false, initialText: "先核对长对话底部的思考摘要，准备发起工具调用。", initialFinal: false},
		{name: "settled-collapsed-to-expanded", collapseLatest: false, initialText: "先核对长对话底部的思考摘要，准备发起工具调用。", initialFinal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := layoutModel()
			m.Preferences.CollapseLatestTools = tc.collapseLatest
			d := m.Details["s"]
			// Seed enough earlier turns so the transcript overflows the viewport
			// and follows the bottom.
			var items []domain.Item
			var turns []domain.Turn
			order := int64(1)
			for i := 1; i <= 12; i++ {
				tid := "old-" + string(rune('a'+i-1))
				turns = append(turns, domain.Turn{ID: tid, Status: domain.ExecutionCompleted})
				items = append(items,
					domain.Item{ID: "u-" + tid, TurnID: tid, Kind: "user", Text: "请帮我检查历史消息并确认布局高度。", Status: "sent", Order: order},
					domain.Item{ID: "a-" + tid, TurnID: tid, Kind: "assistant", Text: "已经检查完毕，没有异常。\n\n继续核对下一段内容。", Final: true, Order: order + 1},
				)
				order += 2
			}
			turns = append(turns, domain.Turn{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started})
			status := "running"
			if tc.initialFinal {
				status = "completed"
			}
			items = append(items,
				domain.Item{ID: "u-live", TurnID: "t", Kind: "user", Text: "开始检查当前构建", Status: "sent", Order: order},
				domain.Item{ID: "r1", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: tc.initialText, Status: status, Final: tc.initialFinal, Order: order + 1, ActivityOrder: order + 1},
			)
			d.Turns, d.Items = turns, items
			m.Details["s"] = d

			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1180, 640)
			ui.SetScale(2)
			ui.SetPreferences(n.Preferences{ReduceMotion: false})
			ui.Frame()
			ui.Advance(time.Second)

			beforeDot, beforeCount := findGreenIndicatorDot(ui)
			if beforeCount != 1 {
				t.Fatalf("expected 1 green dot before merge, got %d", beforeCount)
			}

			d.Items = append(d.Items, domain.Item{
				ID: "t1", TurnID: "t", Kind: "tool", Presentation: "command",
				Name: "go test ./internal/ui", Status: "running", Final: false,
				Order: order + 2, ActivityOrder: order + 2,
			})
			m.Details["s"] = d
			ui.Frame()

			checkDot := func(label string) {
				t.Helper()
				dot, count := findGreenIndicatorDot(ui)
				if count != 1 {
					t.Fatalf("%s: expected 1 green dot, got %d", label, count)
				}
				if math.Abs(float64(dot.Y-beforeDot.Y)) > 1.0 || math.Abs(float64(dot.X-beforeDot.X)) > 0.5 {
					t.Fatalf("%s: green indicator moved from (%.2f,%.2f) to (%.2f,%.2f)",
						label, beforeDot.X, beforeDot.Y, dot.X, dot.Y)
				}
			}
			checkDot("frame0")
			for _, step := range []time.Duration{16 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond, 300 * time.Millisecond} {
				ui.Advance(step)
				checkDot("+" + step.String())
			}
		})
	}
}

// Opt-in 60fps native recording demonstrating in-place leading reasoning ->
// tool group morphs and stationary green progress indicators.
func TestLeadingReasoningMorphNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_REASONING_MORPH_RECORDING")
	if dir == "" {
		t.Skip("opt-in native leading reasoning morph recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)

	const (
		fps   = 60
		step  = time.Second / fps
		width = 1120
		h     = 720
	)
	frame := 0
	capture := func(ui *n.Tester, count int) {
		for i := 0; i < count; i++ {
			ui.Frame()
			saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			frame++
			ui.Advance(step)
		}
	}

	// Scene 1: Expanded mode — streaming reasoning morphs in-place into an
	// expanded tool group, then appends a second tool and trailing reasoning.
	{
		started := time.Now().Add(-3 * time.Second)
		m := layoutModel()
		m.Preferences.CollapseLatestTools = false
		d := m.Details["s"]
		d.Session.Title = "场景 1 · 流式思考摘要原地变为展开工具组"
		m.Snapshot.Sessions[0] = d.Session
		d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started}}
		d.Items = []domain.Item{
			{ID: "u1", TurnID: "t", Kind: "user", Text: "检查工具组合并前置思考摘要的原地变形动画。", Status: "sent", Order: 1},
			{ID: "r1", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: "", Status: "running", Final: false, Order: 2, ActivityOrder: 2},
		}
		m.Details["s"] = d

		v := New(m, nil, "")
		ui := n.NewTester(v.View, width, h)
		ui.SetScale(2)
		ui.SetPreferences(n.Preferences{ReduceMotion: false})
		ui.Frame()
		ui.Advance(200 * time.Millisecond)

		// Empty live reasoning ("正在思考")
		capture(ui, 36)

		// Stream reasoning text in two chunks
		d.Items[1].Text = "先检查 transcriptRows 对前置思考摘要的分组边界，"
		m.Details["s"] = d
		capture(ui, 30)

		d.Items[1].Text = "先检查 transcriptRows 对前置思考摘要的分组边界，再确认原地变形时标题、时间线竖轨和绿色指示器行的衔接。"
		m.Details["s"] = d
		capture(ui, 42)

		// First tool arrives -> in-place morph!
		d.Items = append(d.Items, domain.Item{
			ID: "t1", TurnID: "t", Kind: "tool", Presentation: "files",
			Name: "读取 internal/ui/transcript.go", Status: "running", Final: false,
			Order: 3, ActivityOrder: 3,
		})
		m.Details["s"] = d
		capture(ui, 66)

		// First tool finishes, second tool arrives
		d.Items[1].Final = true
		d.Items[1].Status = "completed"
		d.Items[2].Final = true
		d.Items[2].Status = "completed"
		d.Items = append(d.Items, domain.Item{
			ID: "t2", TurnID: "t", Kind: "tool", Presentation: "command",
			Name: "go test ./internal/ui -run TestLeadingReasoning", Status: "running", Final: false,
			Order: 4, ActivityOrder: 4,
		})
		m.Details["s"] = d
		capture(ui, 54)

		// Trailing reasoning summary merges into the same group
		d.Items[3].Final = true
		d.Items[3].Status = "completed"
		d.Items = append(d.Items, domain.Item{
			ID: "r2", TurnID: "t", Kind: "activity", Presentation: "reasoning",
			Name: "思考摘要", Text: "回归测试全部通过，绿色指示器位置保持稳定。", Status: "running", Final: false,
			Order: 5, ActivityOrder: 5,
		})
		m.Details["s"] = d
		capture(ui, 54)
	}

	// Scene 1B: Same streaming reasoning flow, but with CollapseLatestTools = true.
	// Reasoning streams open, then folds smoothly into the collapsed tool group
	// while the green progress indicator rises into the heading.
	{
		started := time.Now().Add(-3 * time.Second)
		m := layoutModel()
		m.Preferences.CollapseLatestTools = true
		d := m.Details["s"]
		d.Session.Title = "场景 1B · 流式思考摘要 + 工具组默认折叠"
		m.Snapshot.Sessions[0] = d.Session
		d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started}}
		d.Items = []domain.Item{
			{ID: "u1", TurnID: "t", Kind: "user", Text: "开启工具组默认折叠时，流式思考摘要如何原地变成折叠工具组？", Status: "sent", Order: 1},
			{ID: "r1", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: "", Status: "running", Final: false, Order: 2, ActivityOrder: 2},
		}
		m.Details["s"] = d

		v := New(m, nil, "")
		ui := n.NewTester(v.View, width, h)
		ui.SetScale(2)
		ui.SetPreferences(n.Preferences{ReduceMotion: false})
		ui.Frame()
		ui.Advance(200 * time.Millisecond)

		capture(ui, 36)

		d.Items[1].Text = "先检查 transcriptRows 对前置思考摘要的分组边界，"
		m.Details["s"] = d
		capture(ui, 30)

		d.Items[1].Text = "先检查 transcriptRows 对前置思考摘要的分组边界，再确认开启默认折叠后思考摘要平滑收起为折叠工具组。"
		m.Details["s"] = d
		capture(ui, 42)

		// First tool arrives while CollapseLatestTools = true -> folds reasoning
		// smoothly into collapsed tool group heading.
		d.Items = append(d.Items, domain.Item{
			ID: "t1", TurnID: "t", Kind: "tool", Presentation: "files",
			Name: "读取 internal/ui/transcript.go", Status: "running", Final: false,
			Order: 3, ActivityOrder: 3,
		})
		m.Details["s"] = d
		capture(ui, 66)

		// Second tool increments the collapsed count smoothly
		d.Items[1].Final = true
		d.Items[1].Status = "completed"
		d.Items[2].Final = true
		d.Items[2].Status = "completed"
		d.Items = append(d.Items, domain.Item{
			ID: "t2", TurnID: "t", Kind: "tool", Presentation: "command",
			Name: "go test ./internal/ui -run TestLeadingReasoning", Status: "running", Final: false,
			Order: 4, ActivityOrder: 4,
		})
		m.Details["s"] = d
		capture(ui, 54)
	}

	// Scene 2: Collapsed preference — empty live reasoning morphs in-place
	// into a collapsed tool group with the green indicator anchored in place.
	{
		started := time.Now().Add(-2 * time.Second)
		m := layoutModel()
		m.Preferences.CollapseLatestTools = true
		d := m.Details["s"]
		d.Session.Title = "场景 2 · 空思考原地变为折叠工具组（绿点原地不动）"
		m.Snapshot.Sessions[0] = d.Session
		d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started}}
		d.Items = []domain.Item{
			{ID: "u1", TurnID: "t", Kind: "user", Text: "默认折叠工具组时，帮我运行后端单元测试。", Status: "sent", Order: 1},
			{ID: "r1", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: "", Status: "running", Final: false, Order: 2, ActivityOrder: 2},
		}
		m.Details["s"] = d

		v := New(m, nil, "")
		ui := n.NewTester(v.View, width, h)
		ui.SetScale(2)
		ui.SetPreferences(n.Preferences{ReduceMotion: false})
		ui.Frame()
		ui.Advance(200 * time.Millisecond)

		capture(ui, 45)

		d.Items = append(d.Items, domain.Item{
			ID: "t1", TurnID: "t", Kind: "tool", Presentation: "command",
			Name: "go test ./internal/store ./internal/app", Status: "running", Final: false,
			Order: 3, ActivityOrder: 3,
		})
		m.Details["s"] = d
		capture(ui, 72)

		d.Items[1].Final = true
		d.Items[1].Status = "completed"
		d.Items[2].Final = true
		d.Items[2].Status = "completed"
		d.Items = append(d.Items, domain.Item{
			ID: "t2", TurnID: "t", Kind: "tool", Presentation: "command",
			Name: "go test ./internal/mobileclient", Status: "running", Final: false,
			Order: 4, ActivityOrder: 4,
		})
		m.Details["s"] = d
		capture(ui, 60)
	}

	// Scene 3: Overflowing transcript following the bottom — expanded reasoning
	// morphs in-place while the bottom green indicator row stays rock-steady.
	{
		started := time.Now().Add(-5 * time.Second)
		m := layoutModel()
		m.Preferences.CollapseLatestTools = false
		d := m.Details["s"]
		d.Session.Title = "场景 3 · 长对话底部跟随（绿色指示器行位置不变）"
		m.Snapshot.Sessions[0] = d.Session
		var turns []domain.Turn
		var items []domain.Item
		order := int64(1)
		for i := 1; i <= 10; i++ {
			tid := fmt.Sprintf("old-%d", i)
			turns = append(turns, domain.Turn{ID: tid, Status: domain.ExecutionCompleted})
			items = append(items,
				domain.Item{ID: "u-" + tid, TurnID: tid, Kind: "user", Text: fmt.Sprintf("第 %d 轮：请检查历史消息与滚动锚点。", i), Status: "sent", Order: order},
				domain.Item{ID: "a-" + tid, TurnID: tid, Kind: "assistant", Text: "已核对完毕，滚动跟随与高度估算一致。\n\n继续下一项检查。", Final: true, Order: order + 1},
			)
			order += 2
		}
		turns = append(turns, domain.Turn{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started})
		items = append(items,
			domain.Item{ID: "u-live", TurnID: "t", Kind: "user", Text: "继续在长对话底部发起工具调用，验证绿点不抖动。", Status: "sent", Order: order},
			domain.Item{ID: "r1", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: "先核对长对话底部的思考摘要与视口跟随状态，准备调用工具检查测试结果。", Status: "running", Final: false, Order: order + 1, ActivityOrder: order + 1},
		)
		d.Turns, d.Items = turns, items
		m.Details["s"] = d

		v := New(m, nil, "")
		ui := n.NewTester(v.View, width, h)
		ui.SetScale(2)
		ui.SetPreferences(n.Preferences{ReduceMotion: false})
		ui.Frame()
		ui.Advance(time.Second)

		capture(ui, 45)

		d.Items = append(d.Items, domain.Item{
			ID: "t1", TurnID: "t", Kind: "tool", Presentation: "command",
			Name: "go test ./internal/ui -run TestLeadingReasoningMorph", Status: "running", Final: false,
			Order: order + 2, ActivityOrder: order + 2,
		})
		m.Details["s"] = d
		capture(ui, 75)

		d.Items[len(d.Items)-2].Final = true
		d.Items[len(d.Items)-2].Status = "completed"
		d.Items[len(d.Items)-1].Final = true
		d.Items[len(d.Items)-1].Status = "completed"
		d.Items = append(d.Items, domain.Item{
			ID: "t2", TurnID: "t", Kind: "tool", Presentation: "files",
			Name: "检查 internal/ui/view.go", Status: "running", Final: false,
			Order: order + 3, ActivityOrder: order + 3,
		})
		m.Details["s"] = d
		capture(ui, 60)
	}
}
