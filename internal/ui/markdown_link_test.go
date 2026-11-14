package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
)

// A neutral link color needs a persistent non-color cue, including while
// streaming and wrapping. Hover alone cannot make a link discoverable.
func TestMarkdownLinksAreRecognizableBeforeHover(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/stream=%v", dark, streaming), func(t *testing.T) {
				prefs := (app.ClientPreferences{}).Normalized()
				options := markdown.Options{Theme: replyTheme(prefs), Reveal: &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}}
				const source = "普通正文\n\n[查看原生对比与录屏](https://example.com/preview)\n\nRead the [English report](https://example.com/report).\n\n[长链接文字会自然换行，换行后也应当清楚识别并保持可以点击](https://example.com/wrapped)\n\n[`content.go`](https://example.com/source)"
				ui := n.NewTester(func(c *n.Context) {
					theme := applyAppearance(c, prefs)
					n.Column(c).Fill().Padding(20).Background(theme.Background).Children(func() {
						if streaming {
							markdown.Stream(c, source, &options).FillWidth()
						} else {
							markdown.View(c, source, &options).FillWidth()
						}
					})
				}, 360, 300)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("links-dark-%v-stream-%v.png", dark, streaming))
				var hit n.Rect
				for _, label := range []string{"普通正文", "查看原生对比与录屏", "English report", "长链接文字会自然换行，换行后也应当清楚识别并保持可以点击", "content.go"} {
					var boxes []n.Rect
					start := strings.Index(source, label)
					options.DecorateRange = &markdown.RangeDecoration{Start: start, End: start + len(label), Draw: func(_ *n.Painter, r n.Rect) { boxes = append(boxes, r) }}
					ui.Frame()
					if len(boxes) == 0 {
						t.Fatal("native glyph bounds missing", label)
					}
					for _, box := range boxes {
						if nativeUnderline(ui, box, dark) != (label != "普通正文") {
							t.Error("persistent underline does not distinguish links from prose", label, box)
						}
					}
					if label == "查看原生对比与录屏" {
						hit = boxes[0]
					}
				}
				options.DecorateRange = nil
				ui.Frame()
				link, _ := ui.Find("查看原生对比与录屏")
				ui.Move(hit.X+hit.W/2, hit.Y+hit.H/2)
				ui.Advance(200 * time.Millisecond)
				if hovered, _ := ui.Find("查看原生对比与录屏"); hovered != link {
					t.Error("hover changes link geometry", link, hovered)
				}
				ui.ClickAt(hit.X+hit.W/2, hit.Y+hit.H/2)
				if !slices.Equal(ui.OpenedURLs(), []string{"https://example.com/preview"}) {
					t.Error("styled link no longer opens its actual destination", ui.OpenedURLs())
				}
				streaming = false
				ui.Frame()
				if completed, _ := ui.Find("查看原生对比与录屏"); completed != link {
					t.Error("stream completion changes link geometry", link, completed)
				}
			})
		}
	}
}

// A solid stroke spanning most of the text width distinguishes the native
// underline from disconnected glyph strokes. Inspect actual pixels, not theme
// fields, so a renderer that drops the style fails this regression.
func nativeUnderline(ui *n.Tester, box n.Rect, dark bool) bool {
	im := ui.Image()
	for y := int(box.Y); y < int(box.Y+box.H); y++ {
		ink := 0
		for x := int(box.X); x < int(box.X+box.W); x++ {
			p := im.RGBAAt(x, y)
			if dark && p.R > 120 || !dark && p.R < 140 {
				ink++
			}
		}
		if box.W > 0 && float32(ink) > box.W*.8 {
			return true
		}
	}
	return false
}
