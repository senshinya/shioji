package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestAgentActivityShowsEventWithoutDisclosureOrRecordStatus(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, event := range []string{"已开始", "有新活动", "已完成", "已中断"} {
			t.Run(fmt.Sprintf("%v/%s", dark, event), func(t *testing.T) {
				m := layoutModel()
				d := m.Details["s"]
				d.Items = []domain.Item{{ID: "child-event", Kind: "activity", Presentation: "agents", Name: "tooltip_review", Text: "代理" + event, Status: "completed", Final: true}}
				m.Details["s"] = d
				v := New(m, nil, "")
				ui := n.NewTester(v.View, 950, 650)
				ui.SetDark(dark)
				label := "tooltip_review " + event
				if !ui.HasText(label) || slices.Contains(ui.Texts(), "代理"+event) || slices.Contains(ui.Texts(), "已完成") {
					t.Fatal("agent event is hidden behind its record status", ui.Texts())
				}
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
				ui.Advance(250 * time.Millisecond)
				if _, exists := m.Collapsed["activity:s/child-event"]; exists || !ui.HasText(label) {
					t.Fatal("agent status created disclosure state")
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("agent-event-%s-dark-%v.png", event, dark))
			})
		}
	}
}

func TestAgentActivityNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_AGENT_ACTIVITY_RECORDING")
	if dir == "" {
		t.Skip("opt-in native component recording")
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprint(dark)))
			v := New(layoutModel(), nil, "")
			items := []domain.Item{{ID: "started", Kind: "activity", Presentation: "agents", Name: "tooltip_review", Text: "代理已开始", Status: "completed", Final: true}}
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Padding(24).Gap(8).Background(c.Theme().Background).Children(func() {
					for _, item := range items {
						n.Column(c.Key(item.ID)).FillWidth().Children(func() { v.item(c, c.Theme(), item) })
					}
				})
			}, 520, 220)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			for frame := 0; frame < 150; frame++ {
				if frame == 30 {
					items = append(items, domain.Item{ID: "active", Kind: "activity", Presentation: "agents", Name: "tooltip_review", Text: "代理有新活动", Status: "completed", Final: true})
				}
				if frame == 75 {
					items = append(items, domain.Item{ID: "done", Kind: "activity", Presentation: "agents", Name: "tooltip_review", Text: "代理已完成", Status: "completed", Final: true})
				}
				ui.Frame()
				if frame == 100 {
					if err := ui.Click("tooltip_review 有新活动"); err != nil {
						t.Fatal(err)
					}
				}
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
		})
	}
}
