package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func runOptionsFixture(t *testing.T, dark bool) (*View, *n.Tester) {
	t.Helper()
	m := layoutModel()
	m.Readiness.Plan = true
	m.Readiness.Models[0].Fast = true
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(dark)
	ui.Advance(time.Second)
	if e := ui.Click("运行配置"); e != nil {
		t.Fatal(e)
	}
	ui.Advance(time.Second)
	return v, ui
}

func TestRunOptionRowsUseTheFullMenuWidthAndKeepTheMenuOpen(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := runOptionsFixture(t, dark)
			panel, _ := ui.Find("运行配置菜单")
			plan, ok := ui.Find("Plan 开关")
			fast, fastOK := ui.Find("Fast 开关")
			ui.Move(plan.X+plan.W/2, plan.Y+plan.H/2)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("run-options-hover-%v.png", dark))
			if !ok || !fastOK || plan.W < panel.W-10 || fast.W < panel.W-10 {
				t.Fatal("Plan/Fast hover rows do not share the menu inset", panel, plan, fast)
			}
			ui.ClickAt(plan.X+plan.W-12, plan.Y+plan.H/2)
			ui.Advance(time.Second)
			if !v.Model.Config().Plan || v.runMenu == runMenuClosed {
				t.Fatal("whole-row Plan interaction did not persist in place")
			}
			if e := ui.Click("Fast 开关"); e != nil {
				t.Fatal(e)
			}
			ui.Advance(time.Second)
			if !v.Model.Config().Fast || !v.Model.Config().Plan {
				t.Fatal("Fast/Plan cannot be independently enabled")
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("run-options-enabled-%v.png", dark))
			ui.Click("运行配置")
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("run-options-trigger-%v.png", dark))
		})
	}
}

func TestHistoryPagingDoesNotAddNormalLoadingRowsOrSpacing(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 2, Last: 3, Older: true, Newer: true}
	d.Items = []domain.Item{{ID: "a", Order: 2, Kind: "assistant", Text: "One message", Final: true}, {ID: "b", Order: 3, Kind: "assistant", Text: "Another message", Final: true}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	for _, label := range []string{"加载更早消息", "加载后续消息", "正在加载历史…"} {
		if ui.HasText(label) {
			t.Fatal("normal paging adds a loading row", label)
		}
	}
	one, _ := ui.Find("One message")
	two, _ := ui.Find("Another message")
	d.History = nil
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	plainOne, _ := ui.Find("One message")
	plainTwo, _ := ui.Find("Another message")
	if math.Abs(float64(one.Y-plainOne.Y)) > 1 || math.Abs(float64(two.Y-plainTwo.Y)) > 1 {
		t.Fatal("normal paging reserves additional space", one, two, plainOne, plainTwo)
	}
}

func TestRunOptionsKeyboardModelChangeAndNarrowLayout(t *testing.T) {
	for _, width := range []int{1240, 700} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := layoutModel()
			m.Readiness.Plan = true
			m.Readiness.Models = []domain.Model{{ID: "fast-model", Name: "Fast model", Default: true, Efforts: []string{"high"}, DefaultEffort: "high", Fast: true}, {ID: "standard-model", Name: "Standard model"}}
			m.Drafts["session:s"] = "preserve draft"
			v := New(m, nil, "")
			ui := n.NewTester(v.View, width, 820)
			ui.Advance(time.Second)
			ui.Click("运行配置")
			ui.Advance(time.Second)
			plan, _ := ui.Find("Plan 开关")
			fast, _ := ui.Find("Fast 开关")
			if plan.X < 0 || fast.X+fast.W > float32(width) {
				t.Fatal("run menu extends beyond narrow pane", plan, fast)
			}
			ui.ClickAt(plan.X+5, plan.Y+plan.H/2)
			ui.Advance(200 * time.Millisecond)
			if !m.Config().Plan {
				t.Fatal("leading padding is not part of Plan hit area")
			}
			ui.Key(0, n.KeySpace)
			ui.Advance(200 * time.Millisecond)
			if m.Config().Plan {
				t.Fatal("Space did not toggle the focused native switch")
			}
			ui.Click("Fast 开关")
			ui.Advance(time.Second)
			ui.Click("模型")
			ui.Advance(configurationTabDuration)
			ui.Click("Standard model")
			ui.Advance(configurationTabDuration)
			if m.Config().Fast || ui.HasText("Fast") || m.Drafts["session:s"] != "preserve draft" {
				t.Fatal("model change kept unsupported Fast or lost the draft", m.Config())
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("run-options-standard-%d.png", width))
		})
	}
}

func TestRunOptionsNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_RUN_OPTIONS_RECORDING")
	if dir == "" {
		t.Skip("opt-in native recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	v, ui := runOptionsFixture(t, false)
	v.Model.Readiness.Models = append(v.Model.Readiness.Models, domain.Model{ID: "standard-model", Name: "Standard model"})
	for frame := 0; frame < 360; frame++ {
		switch frame {
		case 30:
			r, _ := ui.Find("Plan 开关")
			ui.Move(r.X+10, r.Y+r.H/2)
		case 60:
			if e := ui.Click("Plan 开关"); e != nil {
				t.Fatal(e)
			}
		case 90:
			if e := ui.Click("Fast 开关"); e != nil {
				t.Fatal(e)
			}
		case 120:
			ui.Click("运行配置")
		case 150:
			ui.Click("运行配置")
		case 165:
			ui.Click("模型")
		case 195:
			if e := ui.Click("Standard model"); e != nil {
				t.Fatal(e)
			}
		case 225:
			ui.Click("模型")
		case 255:
			if e := ui.Click("gpt-5.4"); e != nil {
				t.Fatal(e)
			}
		case 285:
			ui.Click("Fast 开关")
		case 315:
			ui.Click("运行配置")
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
}

func TestPlanAndFastSymbolsShareTheSameNativeInkHeight(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		icons := []*n.SVG{planModeIcon, fastModeIcon}
		heights := []float32{}
		for _, icon := range icons {
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Padding(16).Background(c.Theme().Background).Children(func() {
					inlineIconText(c, icon, "Plan", n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}, 14, 8, c.Theme().Text)
				})
			}, 130, 60)
			ui.SetScale(scale)
			ui.Advance(time.Second)
			im := ui.Image()
			bg := im.RGBAAt(0, 0)
			top, bottom := int(60*scale), -1
			for y := int(10 * scale); y < int(48*scale); y++ {
				for x := int(16 * scale); x < int(30*scale); x++ {
					p := im.RGBAAt(x, y)
					if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 50 {
						top = min(top, y)
						bottom = max(bottom, y)
					}
				}
			}
			heights = append(heights, float32(bottom-top+1)/scale)
		}
		if math.Abs(float64(heights[0]-heights[1])) > 1 {
			t.Fatal("Plan and Fast have different visible symbol heights", scale, heights)
		}
	}
}
