package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFileActivityShowsPathsAndPatchWithoutReadingLocalFile(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "f", Kind: "tool", Presentation: "files", Name: "编辑 registry.go", Final: true, Status: "completed", Files: []domain.FileChange{{Path: "/remote/device/registry.go", Kind: "update", Patch: "@@ -1 +1 @@\n-old\n+new"}}}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	if err := ui.Click("1 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(250 * time.Millisecond)
	if err := ui.Click("编辑 registry.go"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(250 * time.Millisecond)
	if _, ok := ui.Find("文件补丁：/remote/device/registry.go"); !ok || !ui.HasText("/remote/device/registry.go") {
		t.Fatal("structured file content lost")
	}
	if err := ui.Click("编辑 registry.go"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(250 * time.Millisecond)
	if _, ok := ui.Find("文件补丁：/remote/device/registry.go"); ok {
		t.Fatal("file disclosure did not close")
	}
}

func TestPublicSummaryDisclosurePreservesOrdinaryDraft(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "reason", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Parts: []domain.ContentPart{{Kind: "text", Text: "正在核对恢复顺序"}}}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "保留我的草稿"
	ui := n.NewTester(New(m, nil, "").View, 950, 650)
	if ui.HasText("正在核对恢复顺序") {
		t.Fatal("summary is not initially compact")
	}
	if err := ui.Click("思考摘要"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("正在核对恢复顺序") || m.Drafts["session:s"] != "保留我的草稿" {
		t.Fatal("summary disclosure lost content or draft")
	}
}

func TestReasoningSummaryHidesTrailingNewlines(t *testing.T) {
	const summary = "检查约束。\n\n核对历史。"
	const next = "摘要之后的回复"
	for _, structured := range []bool{false, true} {
		t.Run(fmt.Sprintf("parts=%t", structured), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = []domain.Item{
				{ID: "reason", Kind: "activity", Presentation: "reasoning", Name: "思考摘要"},
				{ID: "reply", Kind: "assistant", Text: next, Final: true},
			}
			setSummary := func(text string, final bool) {
				item := &d.Items[0]
				item.Final = final
				if structured {
					item.Parts = []domain.ContentPart{{Kind: "text", Text: text}}
				} else {
					item.Text = text
				}
				m.Details["s"] = d
			}
			setSummary(summary, false)
			ui := n.NewTester(New(m, nil, "").View, 950, 650)
			ui.SetDark(true)
			ui.SetScale(2)
			if err := ui.Click("思考摘要"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			before, ok := ui.Find(next)
			if !ok {
				t.Fatal("missing reply after summary")
			}
			for _, update := range []struct {
				text  string
				final bool
			}{
				{summary + "\n", false},
				{summary + "\r\n \t\r\n\n", true},
			} {
				setSummary(update.text, update.final)
				ui.Frame()
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("reasoning-parts-%t-final-%t.png", structured, update.final))
				after, found := ui.Find(next)
				_, sameText := ui.Find(summary)
				if !found || !sameText || after.Y < before.Y-1 || after.Y > before.Y+1 {
					t.Errorf("trailing newlines changed visible text or reply position: final=%t text=%q y=%.1f → %.1f", update.final, ui.Texts(), before.Y, after.Y)
				}
			}
		})
	}
}

func TestNarrativePlanUsesInspectorWithoutInventingTaskSteps(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	json.Unmarshal([]byte(`{"turnId":"t","narrative":"先验证设备，再检查会话。"}`), &d.Plan)
	m.Details["s"] = d
	m.Inspectors["s"] = true
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if !ui.HasText("先验证设备，再检查会话。") {
		t.Fatal("plan narrative absent from inspector")
	}
	if len(d.Plan.Steps) != 0 {
		t.Fatal("prose was converted to task steps")
	}
}

func TestActivityDisclosureKeepsClickedHeadingAtReadingPosition(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < 24; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: "已检查恢复过程与请求生命周期。"})
	}
	d.Items = append(d.Items, domain.Item{ID: "summary", Kind: "activity", Presentation: "reasoning", Name: "较长思考摘要", Parts: []domain.ContentPart{{Kind: "text", Text: strings.Repeat("检查下一条事件。\n", 12)}}})
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 950, 650)
	before, ok := ui.Find("较长思考摘要")
	if !ok {
		t.Fatal("heading missing")
	}
	if err := ui.Click("较长思考摘要"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(220 * time.Millisecond)
	after, ok := ui.Find("较长思考摘要")
	if !ok || after.Y-before.Y > 1 || before.Y-after.Y > 1 {
		t.Fatal("clicked heading moved during disclosure", before, after)
	}
}

func TestEmptyReasoningDoesNotCreateAnUnexpandableRow(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{
		{ID: "a", TurnID: "t", Kind: "assistant", Text: "好", Final: true},
		{ID: "empty", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Final: true},
		{ID: "b", TurnID: "t", Kind: "assistant", Text: "好", Final: true},
		{ID: "trailing", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Final: true},
	}
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	rows := v.transcriptRows(d.Items)
	if ui.HasText("思考摘要") || len(rows) != 2 || rows[0].item.ID != "a" || rows[1].item.ID != "b" {
		t.Fatal("empty summary rendered or actual distinct messages deduplicated", ui.Texts(), rows)
	}
	rows = withTurnFooters(rows, map[string]domain.Turn{"t": d.Turns[0]})
	if len(rows) != 3 || rows[2].footer != "t" {
		t.Fatal("hiding trailing empty reasoning lost the receipt", rows)
	}
}

func TestStandaloneReasoningShowsOnlyThinkingThenMorphsInPlace(t *testing.T) {
	const chunk1 = "先核对现有状态机和渲染顺序，确认流式更新与高度动画的衔接方式。"
	const chunk2 = "再验证多段换行在收到下一段内容后恢复输出，并在结束时自动折叠。"
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprintf("reduceMotion=%t", reduced), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			started := time.Now().Add(-3 * time.Second)
			d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started}}
			d.Items = []domain.Item{
				{ID: "prompt", TurnID: "t", Kind: "user", Text: "检查一下问题", Status: "sent", Final: true},
				{ID: "reason", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Status: "running", ActivityOrder: 1},
			}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1180, 740)
			ui.SetScale(2)
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)

			if !ui.HasText("正在思考") || ui.HasText("思考摘要") || slices.Contains(ui.Texts(), "进行中") {
				t.Fatalf("empty running reasoning should show only 正在思考: %v", ui.Texts())
			}

			// Streaming reasoning expands as 思考摘要 -> indented streamed body -> 正在思考 at bottom.
			d.Items[1].Text = chunk1 + "\n\n\n"
			m.Details["s"] = d
			ui.Frame()
			if !reduced {
				ui.Advance(32 * time.Millisecond)
				if ui.HasText(chunk1) {
					t.Fatalf("first chunk should stream progressively rather than popping in at once: texts=%v", ui.Texts())
				}
			}
			ui.Advance(time.Second)
			heading, hasHeading := ui.Find("思考摘要")
			body, hasBody := ui.Find(chunk1)
			thinking, hasThinking := ui.Find("正在思考")
			if !hasHeading || !hasBody || !hasThinking || slices.Contains(ui.Texts(), "进行中") {
				t.Fatalf("expected expanded 思考摘要 + indented body + bottom 正在思考: texts=%v", ui.Texts())
			}
			if body.X <= heading.X+16 {
				t.Fatalf("streaming reasoning body should be indented under 思考摘要: heading=%+v body=%+v", heading, body)
			}
			if !(heading.Y < body.Y && body.Y+body.H <= thinking.Y+1) {
				t.Fatalf("expected vertical order 思考摘要 < body < 正在思考: heading=%.1f body=[%.1f,%.1f] thinking=%.1f", heading.Y, body.Y, body.Y+body.H, thinking.Y)
			}
			for _, txt := range ui.Texts() {
				if strings.HasPrefix(txt, chunk1) && (strings.HasSuffix(txt, "\n") || strings.HasSuffix(txt, "\r")) {
					t.Fatalf("streaming reasoning exposed trailing newline: %q", txt)
				}
			}

			// A second chunk reveals the held-back intermediate newlines plus
			// the new content while continuing to withhold the new trailing newlines.
			d.Items[1].Text = chunk1 + "\n\n\n" + chunk2 + "\n\n\n"
			m.Details["s"] = d
			ui.Frame()
			if !reduced {
				for step := 0; step < 30; step++ {
					ui.Advance(16 * time.Millisecond)
					for _, txt := range ui.Texts() {
						if strings.HasPrefix(txt, chunk1) && (strings.HasSuffix(txt, "\n") || strings.HasSuffix(txt, "\r")) {
							t.Fatalf("step %d exposed trailing newline during multi-paragraph stream: %q", step, txt)
						}
					}
				}
			}
			ui.Advance(time.Second)
			wantFull := chunk1 + "\n\n\n" + chunk2
			fullBody, ok := ui.Find(wantFull)
			thinkingAfter, hasThinkingAfter := ui.Find("正在思考")
			if !ok || !hasThinkingAfter || fullBody.Y+fullBody.H > thinkingAfter.Y+1 {
				t.Fatalf("second chunk with intermediate newlines not shown above 正在思考: body=%+v thinking=%+v texts=%v", fullBody, thinkingAfter, ui.Texts())
			}
			if !reduced {
				saveNativeSkillImage(t, ui, "reasoning-streaming-expanded-light.png")
				ui.SetDark(true)
				ui.Advance(100 * time.Millisecond)
				saveNativeSkillImage(t, ui, "reasoning-streaming-expanded-dark.png")
				ui.SetDark(false)
				ui.Advance(100 * time.Millisecond)
			}

			// Completing reasoning automatically collapses the indented body
			// while keeping 思考摘要 at the same position.
			d.Items[1].Status = "completed"
			d.Items[1].Final = true
			m.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			if !ui.HasText("思考摘要") || ui.HasText("正在思考") || ui.HasText("已完成") || ui.HasText(wantFull) || ui.HasText(chunk1) {
				t.Fatalf("completed reasoning did not auto-collapse into summary without 已完成: %v", ui.Texts())
			}
			settled, ok := ui.Find("思考摘要")
			if !ok || math.Abs(float64(settled.Y-heading.Y)) > 1 {
				t.Fatalf("settled 思考摘要 did not stay in place: %.1f -> %.1f", heading.Y, settled.Y)
			}
			if !reduced {
				saveNativeSkillImage(t, ui, "reasoning-auto-collapsed.png")
			}
			if err := ui.Click("思考摘要"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			reopened, ok := ui.Find(wantFull)
			if !ok || reopened.X <= settled.X+16 {
				t.Fatalf("clicking settled 思考摘要 did not reveal indented full body: settled=%+v body=%+v", settled, reopened)
			}
		})
	}
}

func TestEmptyRunningReasoningMorphsInPlaceWhenCompletedWithSummary(t *testing.T) {
	const summary = "先核对 Codex 空思考项在完成时补齐摘要的过渡。"
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprintf("reduceMotion=%t", reduced), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			started := time.Now().Add(-4 * time.Second)
			d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning, StartedAt: &started}}
			d.Items = []domain.Item{
				{ID: "prompt", TurnID: "t", Kind: "user", Text: "检查一下问题", Status: "sent", Final: true},
				{ID: "reason", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Status: "running", ActivityOrder: 1},
			}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1180, 740)
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)

			if v.runningReasoning != "reason" {
				t.Fatalf("empty standalone running reasoning should own live status, got runningReasoning=%q", v.runningReasoning)
			}
			before, ok := ui.Find("正在思考")
			if !ok || ui.HasText("思考摘要") || ui.HasText("已完成") {
				t.Fatalf("expected only 正在思考 while empty reasoning runs: %v", ui.Texts())
			}

			// Codex delivers summary text on item/completed while turning Final=true.
			d.Items[1].Parts = []domain.ContentPart{{Kind: "text", Text: summary}}
			d.Items[1].Status = "completed"
			d.Items[1].Final = true
			m.Details["s"] = d
			ui.Frame()

			if !reduced {
				// Frame 0 must not immediately snap the next footer to full height
				// with a duplicate green dot while the reasoning row begins morphing.
				if footer, found := ui.Find("回复信息"); found && footer.H >= 20 && activityGreenIn(ui, footer) {
					t.Fatalf("next running footer popped in at full height on frame 0: %+v", footer)
				}
				sawMidMorph := false
				for step := 0; step < 8; step++ {
					ui.Advance(30 * time.Millisecond)
					heading, found := ui.Find("思考摘要")
					if !found {
						t.Fatalf("step %d: missing 思考摘要 during morph: %v", step, ui.Texts())
					}
					if math.Abs(float64(heading.Y-before.Y)) > 1 {
						t.Fatalf("step %d: 思考摘要 jumped vertically during morph: %.1f -> %.1f", step, before.Y, heading.Y)
					}
					if step < 4 && activityGreenIn(ui, v.historyRowBounds["reason"]) {
						sawMidMorph = true
					}
				}
				if !sawMidMorph {
					t.Fatal("expected live thinking dot to fade smoothly during in-place morph")
				}
			}

			ui.Advance(time.Second)
			if !ui.HasText("思考摘要") || ui.HasText("正在思考") || ui.HasText("已完成") || ui.HasText(summary) {
				t.Fatalf("completed reasoning did not settle cleanly without 已完成: %v", ui.Texts())
			}
			settled, ok := ui.Find("思考摘要")
			if !ok || math.Abs(float64(settled.Y-before.Y)) > 1 {
				t.Fatalf("settled 思考摘要 moved from live thinking row: %.1f -> %.1f", before.Y, settled.Y)
			}
			if err := ui.Click("思考摘要"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !ui.HasText(summary) {
				t.Fatalf("clicking settled 思考摘要 did not reveal body: %v", ui.Texts())
			}
		})
	}
}

func TestCompletedReasoningSummaryOmitsCompletedLabel(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionCompleted}}
	d.Items = []domain.Item{
		{ID: "done", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: "已完成思考。", Status: "completed", Final: true},
		{ID: "stopped", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "中断思考摘要", Text: "被中断的思考。", Status: "interrupted", Final: true},
	}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 950, 650)
	ui.Advance(time.Second)
	if !ui.HasText("思考摘要") {
		t.Fatalf("missing completed reasoning heading: %v", ui.Texts())
	}
	if ui.HasText("已完成") {
		t.Fatalf("completed reasoning summary should not display 已完成: %v", ui.Texts())
	}
	if !ui.HasText("中断思考摘要") || !ui.HasText("已中断") {
		t.Fatalf("non-completed reasoning summary should retain its status: %v", ui.Texts())
	}
}

func TestStandaloneReasoningManualCollapseDuringStreamingRespected(t *testing.T) {
	const chunk1 = "第一段正在思考的摘要内容。"
	const chunk2 = "第二段补充内容。"
	m := layoutModel()
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning}}
	d.Items = []domain.Item{
		{ID: "prompt", TurnID: "t", Kind: "user", Text: "检查", Status: "sent", Final: true},
		{ID: "reason", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: chunk1 + "\n\n", Status: "running", ActivityOrder: 1},
	}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1180, 740)
	ui.Advance(time.Second)
	if !ui.HasText("思考摘要") || !ui.HasText(chunk1) || !ui.HasText("正在思考") {
		t.Fatalf("live reasoning should start expanded above 正在思考: %v", ui.Texts())
	}
	if err := ui.Click("思考摘要"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText(chunk1) || !ui.HasText("正在思考") {
		t.Fatalf("clicking live 思考摘要 should collapse body while keeping 正在思考: %v", ui.Texts())
	}
	d.Items[1].Text = chunk1 + "\n\n" + chunk2 + "\n\n"
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText(chunk1) || ui.HasText(chunk2) {
		t.Fatalf("later chunks should not override manual collapse during streaming: %v", ui.Texts())
	}
	d.Items[1].Status, d.Items[1].Final = "completed", true
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("思考摘要") || ui.HasText(chunk1) {
		t.Fatalf("completed reasoning should remain collapsed: %v", ui.Texts())
	}
	if err := ui.Click("思考摘要"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText(chunk1 + "\n\n" + chunk2) {
		t.Fatalf("reopening after completion did not show full summary: %v", ui.Texts())
	}
}

func TestStandaloneReasoningWithoutSummaryLeavesNoRow(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning}}
	d.Items = []domain.Item{
		{ID: "prompt", TurnID: "t", Kind: "user", Text: "检查", Status: "sent", Final: true},
		{ID: "reason", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: " \n\t ", Status: "running", ActivityOrder: 1},
	}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1180, 740)
	ui.Advance(time.Second)
	if !ui.HasText("正在思考") || ui.HasText("思考摘要") {
		t.Fatalf("whitespace-only running reasoning should show only 正在思考: %v", ui.Texts())
	}
	d.Items[1].Status, d.Items[1].Final = "completed", true
	d.Turns[0].Status = domain.ExecutionCompleted
	d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "已完成。", Final: true})
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("思考摘要") || ui.HasText("正在思考") || !ui.HasText("已完成。") {
		t.Fatalf("empty reasoning summary should not leave a collapsed row: %v", ui.Texts())
	}
}

func TestEmptyReasoningDoesNotSplitConsecutiveTools(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	rows := v.transcriptRows([]domain.Item{
		{ID: "a", TurnID: "t", Kind: "tool", Final: true},
		{ID: "empty", TurnID: "t", Kind: "activity", Presentation: "reasoning", Final: true},
		{ID: "b", TurnID: "t", Kind: "tool", Final: true},
	})
	groups := 0
	for _, row := range rows {
		if row.group != "" {
			groups++
			if row.count != 2 {
				t.Fatal("invisible reasoning split the tool group", rows)
			}
		}
	}
	if groups != 1 {
		t.Fatal("tools did not stay in a single group", rows)
	}
}

func TestToolGroupRespectsCollapsePreferenceAndStreamsWhenExpanded(t *testing.T) {
	const chunk1 = "工具组默认折叠时不主动展开，展开后应平滑流式显示当前思考摘要。"
	const chunk2 = "后续新增段落继续流式输出，同时隐藏末尾多余换行。"
	m := layoutModel()
	// Follow the default preference: keep latest tool groups collapsed unless expanded.
	m.Preferences.CollapseLatestTools = true
	d := m.Details["s"]
	d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning}}
	d.Items = []domain.Item{
		{ID: "prompt", TurnID: "t", Kind: "user", Text: "检查工具组默认折叠设置", Status: "sent", Final: true},
	}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1180, 740)
	ui.Advance(200 * time.Millisecond)

	// Tool call + streaming reasoning arrive while CollapseLatestTools is enabled.
	d.Items = append(d.Items,
		domain.Item{ID: "t1", TurnID: "t", Kind: "tool", Presentation: "files", Name: "读取 internal/ui/transcript.go", Status: "completed", Final: true, ActivityOrder: 1},
		domain.Item{ID: "r1", TurnID: "t", Kind: "activity", Presentation: "reasoning", Name: "思考摘要", Text: chunk1 + "\n\n\n", Status: "running", ActivityOrder: 2},
	)
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(200 * time.Millisecond)

	key := "tool-group:s/t1"
	if !m.ToolDisclosure(key).Closed() || ui.HasText(chunk1) || !ui.HasText("1 次工具调用") {
		t.Fatalf("tool group should stay collapsed under CollapseLatestTools=true: closed=%t texts=%v", m.ToolDisclosure(key).Closed(), ui.Texts())
	}

	// User manually expands the tool group while thinking is still live.
	if err := ui.Click("1 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(32 * time.Millisecond)
	if m.ToolDisclosure(key).Closed() {
		t.Fatal("clicking collapsed tool group did not open it")
	}
	if ui.HasText(chunk1) {
		t.Fatalf("live reasoning inside newly expanded tool group should stream progressively rather than popping in at once: texts=%v", ui.Texts())
	}
	ui.Advance(time.Second)
	if !ui.HasText(chunk1) {
		t.Fatalf("expanded tool group did not finish streaming first reasoning chunk: texts=%v", ui.Texts())
	}
	for _, txt := range ui.Texts() {
		if strings.HasPrefix(txt, chunk1) && (strings.HasSuffix(txt, "\n") || strings.HasSuffix(txt, "\r")) {
			t.Fatalf("expanded tool group exposed trailing newline: %q", txt)
		}
	}

	// Next chunk streams into the open tool group with intermediate newlines restored.
	d.Items[2].Text = chunk1 + "\n\n\n" + chunk2 + "\n\n\n"
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(32 * time.Millisecond)
	wantFull := chunk1 + "\n\n\n" + chunk2
	if ui.HasText(wantFull) {
		t.Fatalf("second chunk inside expanded tool group should stream progressively: texts=%v", ui.Texts())
	}
	ui.Advance(time.Second)
	if !ui.HasText(wantFull) {
		t.Fatalf("second chunk with intermediate newlines missing inside expanded tool group: texts=%v", ui.Texts())
	}
}

func TestFileAndMCPToolsKeepTheExpandedRowsConcise(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{
		{ID: "edit", Kind: "tool", Presentation: "files", Name: "Edit", Final: true,
			Files: []domain.FileChange{{Path: "/workspace/main.go", Kind: "update", Patch: "@@ -1 +1 @@\n-before\n+after\n"}}},
		{ID: "separator", Kind: "assistant", Text: "继续核对结果。", Final: true},
		{ID: "mcp", Kind: "tool", Presentation: "mcp", Name: "核对服务", Text: `{"verbose":true}`, Final: true,
			Parts: []domain.ContentPart{{Kind: "text", Text: "检查通过。"}, {Kind: "code", Label: "结构化结果", Text: `{"ok":true}`}, {Kind: "resource", Label: "report.json", Text: "resource://report"}}},
	}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			for _, item := range d.Items {
				n.Column(c.Key(item.ID)).FillWidth().Children(func() { v.item(c, c.Theme(), item) })
			}
		})
	}, 620, 540)
	if err := ui.Click("Edit main.go"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	if _, ok := ui.Find("文件补丁：/workspace/main.go"); !ok || slices.Contains(ui.Texts(), "/workspace/main.go") {
		t.Fatal("file name remained an extra row", ui.Texts())
	}
	if err := ui.Click("核对服务"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	if !ui.HasText("检查通过。") || ui.HasText("结构化结果") || ui.HasText("resource://report") || ui.HasText(`{"verbose":true}`) {
		t.Fatal("technical metadata dominated the result", ui.Texts())
	}
	if err := ui.Click("查看原始结果"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	if !ui.HasText("resource://report") || !ui.HasText("结构化结果") || !ui.HasText("参数") {
		t.Fatal("the compact view lost the raw result", ui.Texts())
	}
}

func TestCommandAndToolDisclosureShareTheSameHeadingGeometry(t *testing.T) {
	var reference n.Rect
	for i, item := range []domain.Item{
		{ID: "mcp", Kind: "tool", Presentation: "mcp", Name: "Read report", Text: "result", Final: true},
		{ID: "command", Kind: "tool", Presentation: "command", Name: "Read report", Text: "result", Final: true,
			Parts: []domain.ContentPart{{Kind: "text", Label: "工作目录", Text: "/legacy/workspace"}}},
		{ID: "historical", Kind: "tool", Name: "Read report", Text: "result", Final: true},
	} {
		v := New(layoutModel(), nil, "")
		v.Model.Collapsed[v.commandOutputKey(item.ID, "")] = true
		ui := n.NewTester(func(c *n.Context) {
			n.Column(c).Fill().Padding(12).Children(func() { v.item(c, c.Theme(), item) })
		}, 620, 300)
		heading, ok := ui.Find("Read report")
		if !ok || i > 0 && reference != heading {
			t.Fatalf("tool and command heading differ: ordinary=%+v command=%+v", reference, heading)
		}
		reference = heading
		ui.Move(heading.X+heading.W-2, heading.Y+heading.H/2)
		ui.Advance(200 * time.Millisecond)
		if err := ui.Click("Read report"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
		if !ui.HasText("result") || ui.HasText("/legacy/workspace") {
			t.Fatal("disclosure did not keep the command output concise", ui.Texts())
		}
	}
}
