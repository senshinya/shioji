package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestIndividualToolCollapseKeepsConversationWithinScrollRange(t *testing.T) {
	for _, presentation := range []string{"search", "command"} {
		for _, reduced := range []bool{false, true} {
			name := fmt.Sprintf("%s-%v", presentation, reduced)
			t.Run(name, func(t *testing.T) {
				m := layoutModel()
				d := m.Details["s"]
				d.Items = append(toolHistoryModelItems()[:24],
					domain.Item{ID: "earlier-call", TurnID: "t", Kind: "tool", Presentation: "command", Name: "Earlier command", Final: true},
					domain.Item{ID: "call", TurnID: "t", Kind: "tool", Presentation: presentation, Name: "Inspect files", Text: strings.Repeat("One result line\n", 7), Status: "completed", Final: true},
					domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "Final reply", Final: true},
				)
				d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
				m.Details["s"] = d
				v := New(m, nil, "")
				ui := n.NewTester(v.View, 1440, 700)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				ui.Advance(time.Second)
				if err := ui.Click("2 次工具调用"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				ui.Scroll(1300, 390, 0, 2000)
				position := func() float32 {
					ui.Frame()
					r, ok := ui.Find("Final reply")
					if !ok {
						t.Fatal("final reply is outside the viewport")
					}
					return r.Y
				}
				clickTool := func() {
					if err := ui.Click("Inspect files"); err != nil {
						t.Fatal(err)
					}
					ui.Advance(time.Second)
				}
				end := position()
				ui.Scroll(1300, 390, 0, -40)
				ui.Frame()
				clickTool()
				if !ui.HasText("One result line") {
					t.Fatal("individual tool output did not open")
				}
				ui.Scroll(1300, 390, 0, 2000)
				if openedEnd := position(); math.Abs(float64(openedEnd-end)) > 1 {
					t.Fatalf("did not reach the bottom with output open: %.1f, want %.1f", openedEnd, end)
				}
				clickTool()
				collapsed := position()
				saveNativeSkillImage(t, ui, name+"-collapsed.png")
				ui.Scroll(1300, 390, 0, -6)
				after := position()
				saveNativeSkillImage(t, ui, name+"-after-wheel.png")
				if math.Abs(float64(collapsed-end)) > 1 || math.Abs(float64(after-collapsed-6)) > 1 {
					t.Fatalf("collapsed output moved the reply outside the scroll range: end %.1f, collapsed %.1f, after scrolling up 6 DIP %.1f", end, collapsed, after)
				}
			})
		}
	}
}

func TestExpandedToolGroupStartsWithCollapsedTools(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v := toolTailFixture(1)
			v.Model.Preferences = app.NewModel().Preferences
			d := v.Model.Details["s"]
			d.Items = append(d.Items, domain.Item{ID: "search", TurnID: "t", Kind: "tool", Presentation: "search", Name: "搜索文件", Text: "搜索结果正文", Status: "running"})
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			if err := ui.Click("2 次工具调用"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("tool-items-dark-%v.png", dark))
			if !ui.HasText("go test ./internal/ui -run Check0") || !ui.HasText("搜索文件") {
				t.Fatal("opening the group did not reveal tool headings")
			}
			if ui.HasText("ok   shioji/internal/ui") || ui.HasText("搜索结果正文") {
				t.Fatal("opening the group also opened individual tool bodies")
			}
			for _, label := range []string{"go test ./internal/ui -run Check0", "搜索文件"} {
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
			}
			if !ui.HasText("ok   shioji/internal/ui") || !ui.HasText("搜索结果正文") {
				t.Fatal("manual tool expansion did not reveal output")
			}
			ui.Click("执行工具")
			ui.Advance(time.Second)
			ui.Click("2 次工具调用")
			ui.Advance(time.Second)
			if !ui.HasText("ok   shioji/internal/ui") || !ui.HasText("搜索结果正文") {
				t.Fatal("reopening the group lost manual tool choices")
			}
			d.Items = append(d.Items, domain.Item{ID: "next", TurnID: "t", Kind: "tool", Presentation: "command", Name: "next command", Text: "new command output", Status: "running"})
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			if !ui.HasText("next command") || ui.HasText("new command output") {
				t.Fatal("new tool in an open group did not start collapsed")
			}
		})
	}
}

// Production native renderer and pointer input with normalized worker items;
// this records UI behavior without connecting to a live worker or model.
func TestToolItemDisclosureNativeRecording(t *testing.T) {
	root := os.Getenv("SHIOJI_TOOL_ITEM_RECORDING")
	if root == "" {
		t.Skip("opt-in native tool disclosure recording")
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(root, fmt.Sprint(dark)))
			v := toolTailFixture(1)
			v.Model.Preferences = app.NewModel().Preferences
			d := v.Model.Details["s"]
			d.Items = append(d.Items, domain.Item{ID: "search", TurnID: "t", Kind: "tool", Presentation: "search", Name: "搜索文件", Text: "找到两个匹配文件。", Status: "running"})
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			click := func(label string) {
				t.Helper()
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			for frame := 0; frame < 300; frame++ {
				switch frame {
				case 15:
					click("2 次工具调用")
				case 45:
					click("go test ./internal/ui -run Check0")
				case 75, 105, 108:
					click("搜索文件")
				case 135:
					d.Items = append(d.Items, domain.Item{ID: "next", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go vet ./internal/ui", Text: "新增工具默认保持折叠。", Status: "running"})
					v.Model.Details["s"] = d
					ui.Frame()
				case 165:
					click("go vet ./internal/ui")
				case 195:
					click("执行工具")
				case 225:
					click("3 次工具调用")
				case 255:
					d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "检查完成，工具详情可按需展开。", Final: true})
					v.Model.Details["s"] = d
					ui.Frame()
				}
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
		})
	}
}
