package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func selectionAppearanceFixture(t *testing.T, kind string, dark bool) (*View, *n.Tester, string) {
	t.Helper()
	var v *View
	var ui *n.Tester
	label := "消息输入"
	switch kind {
	case "field", "multiline":
		v = New(layoutModel(), nil, "")
		value := "选中文字应该清晰可见 Select text"
		ui = n.NewTester(func(c *n.Context) {
			theme := applyAppearance(c, v.Model.Preferences)
			n.Column(c).Fill().Padding(24).Background(theme.Background).Children(func() {
				if kind == "field" {
					configurationTextField(c, &value).Label(label)
				} else {
					configurationTextArea(c, &value).Label(label)
				}
			})
		}, 600, 220)
	case "history":
		view, tester, item := historyFixture()
		v, ui = view, tester
		v.beginHistoryEdit(item)
		label = "编辑历史消息"
	case "queue":
		v, ui = nativeQueueFixture()
		ui.Click("待发送 2")
		ui.Advance(time.Second)
		if err := ui.Click("编辑待发消息 1"); err != nil {
			t.Fatal(err)
		}
	default:
		m := layoutModel()
		m.Drafts[m.DraftKey()] = "选中文字应该清晰可见 Select text\n保留原生编辑和撤销行为"
		v = New(m, nil, "")
		ui = n.NewTester(v.View, 1200, 820)
	}
	v.Model.Preferences.Appearance = "light"
	if dark {
		v.Model.Preferences.Appearance = "dark"
	}
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click(label); err != nil {
		t.Fatal(err)
	}
	return v, ui, label
}

func selectedBackgroundPair(before, after *image.RGBA, r n.Rect) (color.RGBA, color.RGBA, int) {
	type pair struct{ old, new color.RGBA }
	counts := map[pair]int{}
	var best pair
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			p := pair{before.RGBAAt(x, y), after.RGBAAt(x, y)}
			if p.old == p.new {
				continue
			}
			counts[p]++
			if counts[p] > counts[best] {
				best = p
			}
		}
	}
	return best.old, best.new, counts[best]
}

func selectionContrast(a, b color.RGBA) float64 {
	luminance := func(c color.RGBA) float64 {
		linear := func(v uint8) float64 {
			s := float64(v) / 255
			if s <= .04045 {
				return s / 12.92
			}
			return math.Pow((s+.055)/1.055, 2.4)
		}
		return .2126*linear(c.R) + .7152*linear(c.G) + .0722*linear(c.B)
	}
	x, y := luminance(a), luminance(b)
	return (max(x, y) + .05) / (min(x, y) + .05)
}

func TestEditorSelectionStandsOutInDarkAppearance(t *testing.T) {
	for _, kind := range []string{"composer", "queue", "history", "field", "multiline"} {
		for _, dark := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s-dark-%v", kind, dark), func(t *testing.T) {
				_, ui, label := selectionAppearanceFixture(t, kind, dark)
				bounds, _ := ui.Find(label)
				before := ui.Image()
				ui.Key(n.Cmd, n.KeyA)
				ui.Frame()
				saveNativeSkillImage(t, ui, fmt.Sprintf("selection-%s-dark-%v.png", kind, dark))
				base, selected, pixels := selectedBackgroundPair(before, ui.Image(), bounds)
				if pixels < 100 || selected.R != selected.G || selected.G != selected.B {
					t.Fatal("native selection is missing or no longer neutral", base, selected, pixels)
				}
				if dark && selectionContrast(base, selected) < 2 {
					t.Fatal("dark text selection merges into the editor surface", base, selected, selectionContrast(base, selected))
				}
				foreground := color.RGBA{28, 28, 28, 255}
				if dark {
					foreground = color.RGBA{233, 233, 233, 255}
				}
				if selectionContrast(foreground, selected) < 4.5 {
					t.Fatal("selected text loses readability", foreground, selected)
				}
				if !dark && selected.R != 231 {
					t.Fatal("light selection color changed", selected)
				}
				ui.Key(n.Cmd, n.KeyC)
				original := ui.Clipboard()
				ui.Type("替换选区")
				ui.Key(n.Cmd, n.KeyA)
				ui.Key(n.Cmd, n.KeyC)
				if ui.Clipboard() != "替换选区" {
					t.Fatal("native selection replacement or copy changed", ui.Clipboard())
				}
				ui.Key(n.Cmd, n.KeyZ)
				ui.Key(n.Cmd, n.KeyA)
				ui.Key(n.Cmd, n.KeyC)
				if original == "" || ui.Clipboard() != original {
					t.Fatal("native undo failed to restore selected text", original, ui.Clipboard())
				}
			})
		}
	}
}

func TestEditorSelectionNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_SELECTION_RECORDING")
	if dir == "" {
		t.Skip("opt-in native dark text selection recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	for scene, kind := range []string{"composer", "queue", "history"} {
		_, ui, _ := selectionAppearanceFixture(t, kind, true)
		for frame := 0; frame < 120; frame++ {
			switch frame {
			case 30, 105:
				ui.Key(n.Cmd, n.KeyA)
			case 65:
				ui.Key(0, n.KeyRight)
			case 80:
				ui.Key(n.Shift, n.KeyLeft)
			}
			ui.Advance(time.Second / 30)
			saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", scene*120+frame))
		}
	}
}
