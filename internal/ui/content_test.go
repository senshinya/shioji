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
	const summary = "先核对现有状态机和渲染顺序。"
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
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)

			if !ui.HasText("正在思考") || ui.HasText("思考摘要") || slices.Contains(ui.Texts(), "进行中") {
				t.Fatalf("empty running reasoning should show only 正在思考: %v", ui.Texts())
			}
			beforeEmpty, ok := ui.Find("正在思考")
			if !ok {
				t.Fatal("missing 正在思考 before summary text arrives")
			}

			// Streaming reasoning text while still thinking must keep showing
			// only 正在思考 at the exact same Y position without 思考摘要/进行中.
			d.Items[1].Text = summary + "\n"
			m.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			if !ui.HasText("正在思考") || ui.HasText("思考摘要") || slices.Contains(ui.Texts(), "进行中") || ui.HasText(summary) {
				t.Fatalf("streaming reasoning showed duplicate or expanded summary: %v", ui.Texts())
			}
			whileStreaming, ok := ui.Find("正在思考")
			if !ok {
				t.Fatal("missing 正在思考 while streaming summary")
			}
			if math.Abs(float64(whileStreaming.Y-beforeEmpty.Y)) > 1 {
				t.Fatalf("taking over live reasoning row moved vertically: %.1f -> %.1f", beforeEmpty.Y, whileStreaming.Y)
			}

			// Completing reasoning with a summary morphs the same row in-place
			// into the collapsed 思考摘要 disclosure, then yields live progress below.
			d.Items[1].Status = "completed"
			d.Items[1].Final = true
			m.Details["s"] = d
			ui.Frame()
			for step := 0; step < 6; step++ {
				ui.Advance(35 * time.Millisecond)
				heading, found := ui.Find("思考摘要")
				if !found {
					t.Fatalf("step %d: missing 思考摘要 during in-place transition: %v", step, ui.Texts())
				}
				if math.Abs(float64(heading.Y-whileStreaming.Y)) > 1 {
					t.Fatalf("step %d: 思考摘要 jumped vertically during morph: %.1f -> %.1f", step, whileStreaming.Y, heading.Y)
				}
			}
			ui.Advance(time.Second)
			if !ui.HasText("思考摘要") || ui.HasText("正在思考") || ui.HasText("已完成") || ui.HasText(summary) {
				t.Fatalf("completed reasoning did not settle into collapsed summary without 已完成: %v", ui.Texts())
			}
			settled, ok := ui.Find("思考摘要")
			if !ok || math.Abs(float64(settled.Y-whileStreaming.Y)) > 1 {
				t.Fatalf("settled 思考摘要 did not stay in place: %.1f -> %.1f", whileStreaming.Y, settled.Y)
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
