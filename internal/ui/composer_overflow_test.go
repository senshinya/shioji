package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
)

func TestComposerEditorPaintStaysInsideScrollViewport(t *testing.T) {
	// One soft-wrapped paragraph is painted as one text layout, including the
	// lines below the viewport. It must not paint into neighbouring controls.
	doc := richtext.NewDocument(richtext.NewText(strings.Repeat("长文本 viewport boundary https://example.com/path ", 80)))
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Padding(24).Background(n.RGB(255, 255, 255)).Children(func() {
			richtext.EditorBase(c, doc, richtext.EditorOptions{Label: "editor", PlainText: true, MaxLines: 7, LineHeight: 26, Font: n.Font{Size: 15}, Theme: &richtext.Theme{Text: n.RGB(255, 0, 0)}}).Width(420)
		})
	}, 480, 340)
	box, ok := ui.Find("editor")
	if !ok {
		t.Fatal("missing editor")
	}
	for _, scroll := range []float32{0, 95, -47} {
		ui.Scroll(box.X+100, box.Y+50, 0, scroll)
		img := ui.Image()
		ink := 0
		for y := 0; y < img.Bounds().Max.Y; y++ {
			for x := 0; x < img.Bounds().Max.X; x++ {
				p := img.RGBAAt(x, y)
				if int(p.R) > int(p.G)+20 && int(p.R) > int(p.B)+20 {
					ink++
					if float32(x) < box.X || float32(x) >= box.X+box.W || float32(y) < box.Y || float32(y) >= box.Y+box.H {
						t.Fatalf("editor text paints outside viewport at (%d,%d), scroll=%g, viewport=%+v", x, y, scroll, box)
					}
				}
			}
		}
		if ink == 0 {
			t.Fatal("viewport lost its visible text")
		}
	}
}

func TestLongComposerNativeCapture(t *testing.T) {
	for _, home := range []bool{true, false} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("home-%v/dark-%v", home, dark), func(t *testing.T) {
				m := layoutModel()
				if home {
					m.SessionID = ""
				}
				m.Drafts[m.DraftKey()] = strings.Repeat("长内容输入测试：保留完整文本，底部操作栏保持可用。 https://example.com/very/long/path?workspace=example&navigation=configuration\n", 14)
				v := New(m, nil, "")
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				box, ok := ui.Find("消息输入")
				if !ok {
					t.Fatal("missing composer")
				}
				ui.Scroll(box.X+100, box.Y+50, 0, 95)
				footer, ok := ui.Find("Permission")
				if !ok || box.Y+box.H > footer.Y {
					t.Fatal("input viewport overlaps footer", box, footer)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("home-%v-dark-%v.png", home, dark))
				if dir := os.Getenv("SHIOJI_COMPOSER_RECORDING"); home && dir != "" {
					t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprintf("dark-%v", dark)))
					long := m.Drafts[m.DraftKey()]
					for frame := 0; frame < 120; frame++ {
						if frame < 30 {
							ui.Scroll(box.X+100, box.Y+50, 0, 7)
						} else if frame < 60 {
							ui.Scroll(box.X+100, box.Y+50, 0, -7)
						} else if frame == 60 {
							m.Drafts[m.DraftKey()] = "短内容输入检查"
						} else if frame == 85 {
							m.Drafts[m.DraftKey()] = long
						}
						ui.Advance(time.Second / 30)
						ui.Frame()
						saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
					}
				}
			})
		}
	}
}
