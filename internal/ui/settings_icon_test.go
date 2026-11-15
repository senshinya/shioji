package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestSettingsIconHasSquareNativeInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark=%v/scale=%g", dark, scale), func(t *testing.T) {
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Padding(16).Background(c.Theme().Background).Children(func() { iconButton(c, settingsIcon, "设置") })
				}, 60, 60)
				ui.SetDark(dark)
				ui.SetScale(scale)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("settings-dark-%v-scale-%g.png", dark, scale))
				im := ui.Image()
				bg := im.RGBAAt(0, 0)
				left, top, right, bottom := im.Bounds().Max.X, im.Bounds().Max.Y, -1, -1
				for y := 0; y < im.Bounds().Max.Y; y++ {
					for x := 0; x < im.Bounds().Max.X; x++ {
						p := im.RGBAAt(x, y)
						if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 50 {
							left, top, right, bottom = min(left, x), min(top, y), max(right, x), max(bottom, y)
						}
					}
				}
				w, h := float64(right-left+1)/float64(scale), float64(bottom-top+1)/float64(scale)
				if w < 12 || h < 12 || math.Abs(w-h) > .5 {
					t.Fatalf("settings gear is flattened: native ink=%gx%g", w, h)
				}
			})
		}
	}
}
