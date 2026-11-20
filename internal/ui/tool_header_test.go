package ui

import (
	"fmt"
	"image"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestToolCountUsesTheCommandGlyph(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, live := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/live=%v", dark, live), func(t *testing.T) {
				v := toolTailFixture(2)
				if !live {
					d := v.Model.Details["s"]
					d.Items[2].Status, d.Items[2].Final = "completed", true
					d.Turns[0].Status = "completed"
					v.Model.Details["s"] = d
				}
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if !live {
					if err := ui.Click("2 次工具调用"); err != nil {
						t.Fatal(err)
					}
					ui.Move(1200, 790)
					ui.Advance(time.Second)
				}
				ink := func(x, y int) image.Rectangle {
					im := ui.Image()
					bg := im.RGBAAt(x+14, y)
					var bounds image.Rectangle
					for dy := 0; dy < 20; dy++ {
						for dx := 0; dx < 14; dx++ {
							p := im.RGBAAt(x+dx, y+dy)
							if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 35 {
								bounds = bounds.Union(image.Rect(dx, dy, dx+1, dy+1))
							}
						}
					}
					return bounds
				}
				cmd, ok := ui.Find("go test ./internal/ui -run Check0")
				if !ok {
					t.Fatal("command reference must be visible")
				}
				want := ink(int(cmd.X)-24, int(cmd.Y))
				disclosure := "2 次工具调用"
				if live {
					disclosure = "执行工具"
				}
				for _, folded := range []bool{false, true} {
					saveNativeSkillImage(t, ui, fmt.Sprintf("tool-count-dark-%v-live-%v-folded-%v.png", dark, live, folded))
					count, ok := ui.Find("2 次工具调用")
					if !ok {
						t.Fatal("tool count must be visible")
					}
					x := int(count.X)
					if live {
						x += 12 // the green activity dot precedes the icon
					}
					got := ink(x, int(count.Y))
					if want.Empty() || got.Empty() || got.Min.X != want.Min.X || got.Max.X != want.Max.X || abs(got.Dy()-want.Dy()) > 1 {
						t.Errorf("tool count glyph differs from the command glyph: count=%v command=%v", got, want)
					}
					if !folded {
						if err := ui.Click(disclosure); err != nil {
							t.Fatal(err)
						}
						ui.Move(1200, 790)
						ui.Advance(time.Second)
					}
				}
			})
		}
	}
}

func TestToolGroupOnlyTitleControlsDisclosure(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v := toolTailFixture(2)
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			key := "tool-group:s/call-0"
			for _, label := range []string{"2 次工具调用", "正在执行命令", "运行时长"} {
				r, ok := ui.Find(label)
				if !ok {
					t.Fatalf("lost status %s", label)
				}
				ui.ClickAt(r.X+r.W/2, r.Y+r.H/2)
				ui.Advance(200 * time.Millisecond)
				if v.Model.ToolDisclosure(key).Closed() {
					t.Fatalf("expanded bottom status %s folded the tools", label)
				}
			}
			if err := ui.Click("执行工具"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(200 * time.Millisecond)
			if !v.Model.ToolDisclosure(key).Closed() || ui.HasText("执行工具") || ui.HasText("go test ./internal/ui -run Check0") {
				t.Fatal("folded tools must leave only the one-line summary", ui.Texts())
			}
			summary, ok := ui.Find("2 次工具调用")
			phase, phaseOK := ui.Find("正在执行命令")
			clock, clockOK := ui.Find("运行时长")
			if !ok || !phaseOK || !clockOK || summary.Y != phase.Y || summary.Y != clock.Y {
				t.Fatal("folded header lost its single-line state and clock", summary, phase, clock)
			}
			if err := ui.Click("2 次工具调用"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(200 * time.Millisecond)
			if v.Model.ToolDisclosure(key).Closed() || !ui.HasText("执行工具") || !ui.HasText("go test ./internal/ui -run Check0") {
				t.Fatal("folded header did not reopen tools")
			}
		})
	}
}

// Completed tools keep a visible cue on their single count heading.
func TestCompletedToolGroupKeepsVisibleDisclosureCue(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v, ui := timelineFixture(t, dark)
			for _, closed := range []bool{false, true} {
				ui.Move(1200, 790)
				ui.Advance(time.Second)
				summary, ok := ui.Find("2 次工具调用")
				if !ok {
					t.Fatal("finished heading missing")
				}
				font := n.Font{Family: v.Model.Preferences.InterfaceFont, Size: 11, Weight: 400}
				width, _ := n.ShapeText("2 次工具调用", font, 0).Size()
				width += toolGlyphSlot + toolSummaryGap
				if right := activityLastInkX(ui, summary, dark); right <= summary.X+width+6 {
					t.Fatal("finished heading has no visible disclosure cue", closed, right, summary)
				}
				if v.Model.ToolDisclosure("tool-group:s/a").Closed() != closed {
					t.Fatal("wrong disclosure state", closed)
				}
				if err := ui.Click("2 次工具调用"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCompletedToolCountIsTheOnlyDisclosureHeading(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v, ui := timelineFixture(t, dark)
			heading, ok := ui.Find("2 次工具调用")
			first, firstOK := ui.Find("first command")
			if !ok || !firstOK || heading.Y >= first.Y || ui.HasText("执行工具") {
				t.Fatal("finished count must be the top disclosure title", heading, first, ui.Texts())
			}
			titleY := heading.Y
			if err := ui.Click("2 次工具调用"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			heading, ok = ui.Find("2 次工具调用")
			if !ok || ui.HasText("first command") || heading.Y != titleY {
				t.Fatal("finished title does not fold in place", heading, titleY)
			}
			if err := ui.Click("2 次工具调用"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !ui.HasText("first command") || !ui.HasText("回复信息") {
				t.Fatal("finished heading cannot reopen content and receipt")
			}
			// A single title represents this group; there is no redundant tail row.
			groups := 0
			for _, row := range v.transcriptRows(v.Model.Details["s"].Items) {
				if row.group != "" {
					groups++
					if !row.toolTitle {
						t.Fatal("finished group retains a bottom count row")
					}
				}
			}
			if groups != 1 {
				t.Fatal("finished group lost its single disclosure heading", groups)
			}
		})
	}
}
