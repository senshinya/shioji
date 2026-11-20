package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestWorkSourceLocateStaysWithinScrollableContent(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, turns := range []int{1, 8} {
			for _, tail := range []bool{true, false} {
				t.Run(fmt.Sprintf("reduced=%v/turns=%d/tail=%v", reduced, turns, tail), func(t *testing.T) {
					v, ui := sessionWorkFixture(t)
					ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
					d := v.Model.Details["s"]
					d.Items, d.Turns = nil, nil
					for i := 0; i < turns; i++ {
						id := fmt.Sprintf("turn-%d", i)
						d.Items = append(d.Items, domain.Item{ID: "reply-" + id, TurnID: id, Kind: "assistant", Text: fmt.Sprint("历史回复 ", i, strings.Repeat("保留原有阅读位置。", 20)), Final: true})
						d.Turns = append(d.Turns, domain.Turn{ID: id, Status: domain.ExecutionCompleted})
					}
					d.Items = append(d.Items, domain.Item{ID: "source", TurnID: d.Turns[turns-1].ID, Kind: "assistant", Text: "来源消息", Final: true})
					if !tail {
						for i := 0; i < 12; i++ {
							d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("suffix-", i), TurnID: d.Turns[turns-1].ID, Kind: "assistant", Text: fmt.Sprint("后续回复 ", i, strings.Repeat("继续保留后续对话。", 20)), Final: true})
						}
					}
					d.Works[0].TurnID = d.Turns[turns-1].ID
					d.Works[0].SourceItemID = "source"
					v.Model.Details["s"] = d
					ui.Frame()
					ui.Advance(time.Second)
					if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
						t.Fatal(err)
					}
					ui.Advance(time.Second)
					if err := ui.Click("定位来源"); err != nil {
						t.Fatal(err)
					}
					ui.Advance(time.Second)
					before, ok := ui.Find("来源消息")
					if !ok {
						t.Fatal("source was not brought into view")
					}
					if !tail && math.Abs(float64(before.Y-112)) > 1 {
						t.Errorf("source navigation used a different row than the rendered transcript: %+v", before)
					}
					saveNativeSkillImage(t, ui, fmt.Sprintf("source-turns-%d-reduced-%v-tail-%v-located.png", turns, reduced, tail))
					ui.Scroll(700, 300, 0, -1)
					ui.Frame()
					ui.Advance(400 * time.Millisecond)
					after, ok := ui.Find("来源消息")
					saveNativeSkillImage(t, ui, fmt.Sprintf("source-turns-%d-reduced-%v-tail-%v-wheel.png", turns, reduced, tail))
					if !ok || math.Abs(float64(after.Y-before.Y)) > 2 {
						t.Fatalf("one-pixel scroll jumps after source navigation: before=%+v after=%+v visible=%v", before, after, ok)
					}
				})
			}
		}
	}
}

func TestWorkSourceLocateUsesVisibleToolRow(t *testing.T) {
	for _, folded := range []bool{false, true} {
		for _, source := range []string{"tool-a", "tool-b"} {
			t.Run(fmt.Sprintf("folded=%v/source=%s", folded, source), func(t *testing.T) {
				v, ui := sessionWorkFixture(t)
				d := v.Model.Details["s"]
				d.Items, d.Turns = nil, nil
				for i := 0; i < 8; i++ {
					id := fmt.Sprint("earlier-", i)
					d.Turns = append(d.Turns, domain.Turn{ID: id, Status: domain.ExecutionCompleted})
					d.Items = append(d.Items, domain.Item{ID: id, TurnID: id, Kind: "assistant", Text: strings.Repeat("前轮历史。", 40), Final: true})
				}
				d.Turns = append(d.Turns, domain.Turn{ID: "t", Status: domain.ExecutionCompleted})
				d.Items = append(d.Items, domain.Item{ID: "tool-a", TurnID: "t", Kind: "tool", Presentation: "command", Name: "准备来源", Status: "completed", Final: true}, domain.Item{ID: "tool-b", TurnID: "t", Kind: "tool", Presentation: "command", Name: "启动子 Agent", Status: "completed", Final: true})
				for i := 0; i < 12; i++ {
					d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("later-", i), TurnID: "t", Kind: "assistant", Text: strings.Repeat("后续回复。", 40), Final: true})
				}
				d.Works[0].SourceItemID = source
				v.Model.Details["s"] = d
				group := "tool-group:s/tool-a"
				disclosure := app.ToolOpenedAfterProse
				if folded {
					disclosure = app.ToolClosedAfterProse
				}
				v.Model.SetToolDisclosure(group, disclosure)
				ui.Frame()
				ui.Advance(time.Second)
				if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				if err := ui.Click("定位来源"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				key := source
				if folded {
					key = group
				}
				bounds, ok := v.historyRowBounds[key]
				if !ok || math.Abs(float64(bounds.Y-112)) > 1 || bounds.H <= 0 {
					t.Fatal("source did not resolve to its visible tool row", key, bounds, ok)
				}
				ui.Scroll(700, 300, 0, -1)
				ui.Frame()
				ui.Advance(400 * time.Millisecond)
				after, ok := v.historyRowBounds[key]
				if !ok || math.Abs(float64(after.Y-bounds.Y)) > 2 {
					t.Fatal("tool source jumps on manual scrolling", bounds, after)
				}
			})
		}
	}
}
