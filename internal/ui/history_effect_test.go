package ui

import (
	"fmt"
	"image/color"
	"testing"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func TestHistoryShatterPreservesStationaryCodeBlockColors(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			active, progress := false, float32(0)
			ui := n.NewTester(func(c *n.Context) {
				root := n.Column(c).Fill().Padding(30).Background(c.Theme().Background)
				root.Children(func() {
					row := n.Column(c).FillWidth()
					if active {
						row.Effect(historyShatter{progress: progress, seed: 1})
					}
					row.Children(func() {
						markdown.View(c, "说明文字\n\n```go\nfunc rotate() {\n    return\n}\n```", nil).FillWidth()
					})
				})
			}, 600, 350)
			ui.SetDark(dark)
			ui.Frame()
			block, ok := ui.Find("Code, go")
			if !ok {
				t.Fatal("missing code block")
			}
			// The right edge has not started dissolving at either sample time.
			x, y := int(block.X+block.W-30), int(block.Y)
			before := []color.RGBA{ui.Image().RGBAAt(x, y), ui.Image().RGBAAt(x, y+20)}
			for _, step := range []float32{0, .2} {
				active, progress = true, step
				ui.Frame()
				for i, py := range []int{y, y + 20} {
					a, b := ui.Image().RGBAAt(x, py), before[i]
					for _, delta := range []int{int(a.R) - int(b.R), int(a.G) - int(b.G), int(a.B) - int(b.B)} {
						if delta > 8 || delta < -8 {
							t.Fatalf("shatter changes intact code block color at progress %.2f, sample %d: %v -> %v", step, i, b, a)
						}
					}
				}
			}
		})
	}
}
