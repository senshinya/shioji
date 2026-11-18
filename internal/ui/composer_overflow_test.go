package ui

import (
	"fmt"
	"image"
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

func TestComposerKeepsFirstLineVisibleWhileResizing(t *testing.T) {
	for _, home := range []bool{true, false} {
		t.Run(fmt.Sprintf("home-%v", home), func(t *testing.T) {
			m := layoutModel()
			if home {
				m.SessionID = ""
			}
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			if err := ui.Click("消息输入"); err != nil {
				t.Fatal(err)
			}
			ui.Type("MMMMMMMMMMMMMMMM\n二\n三")
			ui.Advance(400 * time.Millisecond)
			before, _ := ui.Find("消息编辑器")
			input, _ := ui.Find("消息输入")
			inset := input.Y - before.Y
			firstLineInk := func(frame n.Rect) int {
				img := ui.Image()
				top := int(frame.Y + inset)
				area := image.Rect(int(input.X)+5, top, int(input.X)+155, top+20)
				ink := 0
				for y := area.Min.Y; y < area.Max.Y; y++ {
					for x := area.Min.X; x < area.Max.X; x++ {
						if img.RGBAAt(x, y).R < 125 {
							ink++
						}
					}
				}
				return ink
			}
			wantInk := firstLineInk(before)
			if wantInk < 100 {
				t.Fatal("missing first line before resize", wantInk)
			}
			capture := func(name string) n.Rect {
				t.Helper()
				frame, _ := ui.Find("消息编辑器")
				saveNativeSkillImage(t, ui, fmt.Sprintf("home-%v-%s.png", home, name))
				if ink := firstLineInk(frame); ink < wantInk*8/10 {
					t.Errorf("%s: first line moves out of view while resizing: ink=%d, want near %d, frame=%+v", name, ink, wantInk, frame)
				}
				return frame
			}

			ui.Type("\n四")
			growStart := capture("grow-start")
			ui.Advance(60 * time.Millisecond)
			growMid := capture("grow-mid")
			ui.Advance(340 * time.Millisecond)
			grown := capture("grown")
			if !(growStart.H < growMid.H && growMid.H < grown.H) {
				t.Error("composer does not grow smoothly", growStart, growMid, grown)
			}

			ui.Key(0, n.KeyBackspace)
			ui.Key(0, n.KeyBackspace)
			shrinkStart := capture("shrink-start")
			ui.Advance(60 * time.Millisecond)
			shrinkMid := capture("shrink-mid")
			ui.Advance(340 * time.Millisecond)
			shrunk := capture("shrunk")
			if !(shrinkStart.H > shrinkMid.H && shrinkMid.H > shrunk.H && shrunk.H == before.H) {
				t.Error("composer does not shrink smoothly", shrinkStart, shrinkMid, shrunk)
			}
		})
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
