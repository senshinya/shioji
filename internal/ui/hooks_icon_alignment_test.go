package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"testing"
	"time"
)

func TestHookDisclosureArrowSharesChineseInkCenter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark=%v/scale=%g", dark, scale), func(t *testing.T) {
				v, ui := hookUIFixture(t, true)
				ui.SetDark(dark)
				ui.SetScale(scale)
				applyHookCatalog(t, v, ui, true)
				if err := ui.Click("查看 Hook：PreToolUse"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				for _, expanded := range []bool{false, true} {
					if expanded {
						if err := ui.Click("详细信息"); err != nil {
							t.Fatal(err)
						}
						ui.Advance(time.Second)
					}
					box, ok := ui.Find("详细信息")
					if !ok {
						t.Fatal("disclosure absent")
					}
					im := ui.Image()
					bg := im.RGBAAt(int((box.X+box.W/2)*scale), int((box.Y+2)*scale))
					inkBounds := func(r n.Rect) n.Rect {
						top, bottom := im.Bounds().Max.Y, -1
						left, right := im.Bounds().Max.X, -1
						for y := int(r.Y * scale); y < int((r.Y+r.H)*scale); y++ {
							for x := int(r.X * scale); x < int((r.X+r.W)*scale); x++ {
								pixel := im.RGBAAt(x, y)
								delta := int(pixel.R) - int(bg.R)
								if delta > 50 || delta < -50 {
									top = min(top, y)
									bottom = max(bottom, y)
									left = min(left, x)
									right = max(right, x)
								}
							}
						}
						if bottom < 0 {
							t.Fatal("missing visible ink", r)
						}
						return n.Rect{X: float32(left) / scale, Y: float32(top) / scale, W: float32(right-left+1) / scale, H: float32(bottom-top+1) / scale}
					}
					arrow := n.Rect{X: box.X + box.W - 12, Y: box.Y + 2, W: 12, H: box.H - 4}
					text := n.Rect{X: box.X, Y: box.Y + 2, W: 32, H: box.H - 4}
					a, word := inkBounds(arrow), inkBounds(text)
					if a.H > 8 || a.W > 9 {
						t.Fatal("trailing slot contains title text instead of a compact disclosure arrow", a, box)
					}
					delta := float64(a.Y + a.H/2 - word.Y - word.H/2)
					t.Logf("expanded=%v arrow/text ink centre delta=%.2f DIP", expanded, delta)
					if math.Abs(delta) > 1 {
						t.Fatalf("disclosure arrow misses Chinese ink centre: expanded=%v delta=%.2f DIP", expanded, delta)
					}
				}
			})
		}
	}
}
