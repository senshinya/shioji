package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
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
