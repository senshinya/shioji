package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestRunModelAndEffortPagesSlideIntoAndBackFromSelection(t *testing.T) {
	for _, kind := range []string{"模型", "推理强度"} {
		t.Run(kind, func(t *testing.T) {
			m := layoutModel()
			m.SessionID = ""
			m.Readiness.Models = []domain.Model{{ID: "a", Name: "Alpha", Default: true, Efforts: []string{"low", "medium", "high"}, DefaultEffort: "low"}, {ID: "b", Name: "Beta", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"}}
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			ui.Click("运行配置")
			ui.Advance(time.Second)
			if err := ui.Click(kind); err != nil {
				t.Fatal(err, ui.Texts())
			}
			choice := "Beta"
			if kind == "推理强度" {
				choice = "medium"
			}
			ui.Advance(60 * time.Millisecond)
			mid, ok := ui.Find(choice)
			if !ok {
				t.Fatal(ui.Texts())
			}
			ui.Advance(400 * time.Millisecond)
			end, _ := ui.Find(choice)
			if mid.X <= end.X+1 {
				t.Fatal("submenu did not enter from the right", kind, mid, end)
			}
			if err := ui.Click(choice); err != nil {
				t.Fatal(err)
			}
			ui.Advance(120 * time.Millisecond)
			back, _ := ui.Find("模型")
			ui.Advance(400 * time.Millisecond)
			settled, _ := ui.Find("模型")
			if back.X+back.W >= settled.X+settled.W-1 {
				t.Fatal("choosing an option did not slide back to main", kind, back, settled)
			}
			if v.runMenu != runMenuMain {
				t.Fatal("selection closed the whole run menu")
			}
		})
	}
}

func TestRunMenuRetainsAnchorWhenSelectionChangesSummaryWidth(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.Readiness.Models = []domain.Model{{ID: "a", Default: true, Efforts: []string{"low", "medium"}, DefaultEffort: "low"}}
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("运行配置")
	ui.Advance(time.Second)
	before, _ := ui.Find("运行配置菜单")
	ui.Click("推理强度")
	ui.Advance(400 * time.Millisecond)
	ui.Click("medium")
	ui.Advance(80 * time.Millisecond)
	mid, _ := ui.Find("运行配置菜单")
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("运行配置菜单")
	for _, r := range []n.Rect{mid, end} {
		if math.Abs(float64(r.X+r.W-before.X-before.W)) > 1 || math.Abs(float64(r.Y+r.H-before.Y-before.H)) > 1 {
			t.Fatal("summary text change moves the menu anchor", before, mid, end)
		}
	}
}
