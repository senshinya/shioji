package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

// Equal icon slots do not imply equal visible ink. The file outline previously
// occupied only 12/20 of its slot, making Diff smaller than its neighbours.
func TestInspectorDiffIconVisibleSize(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2, 3} {
			t.Run(fmt.Sprintf("dark=%v/scale=%g", dark, scale), func(t *testing.T) {
				v := New(layoutModel(), nil, "")
				icons := []struct {
					label string
					icon  *n.SVG
				}{{"Diff", changesIcon}, {"Agent", agentActivityIcon}, {"刷新", refreshIcon}}
				ui := n.NewTester(func(c *n.Context) {
					theme := applyAppearance(c, v.Model.Preferences)
					n.Row(c).Fill().Padding(12).Gap(8).Background(theme.Background).Children(func() {
						for _, icon := range icons {
							iconButton(c, icon.icon, icon.label)
						}
					})
				}, 160, 60)
				ui.SetDark(dark)
				ui.SetScale(scale)
				ui.Advance(time.Second)
				var diffHeight, diffCenter float64
				for i, icon := range icons {
					button, ok := ui.Find(icon.label)
					if !ok || button.W != 28 || button.H != 28 {
						t.Fatal("compact icon hit area changed", icon.label, button)
					}
					im := ui.Image()
					bg := im.RGBAAt(int(button.X*scale), int(button.Y*scale))
					low, high := im.Bounds().Max.Y, -1
					for y := int(button.Y * scale); y < int((button.Y+button.H)*scale); y++ {
						for x := int(button.X * scale); x < int((button.X+button.W)*scale); x++ {
							p := im.RGBAAt(x, y)
							if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
								low, high = min(low, y), max(high, y)
							}
						}
					}
					if high < 0 {
						t.Fatal("icon has no visible ink", icon.label)
					}
					height := float64(high-low+1) / float64(scale)
					center := float64(low+high+1)/float64(2*scale) - float64(button.Y)
					t.Log(icon.label, "height", height, "center", center)
					if i == 0 {
						diffHeight, diffCenter = height, center
					} else if (icon.label == "刷新" && math.Abs(diffHeight-height) > 1) || math.Abs(diffCenter-center) > 1 {
						t.Errorf("Diff ink is smaller or off-centre: height %.2f vs %.2f, centre %.2f vs %.2f (%s)", diffHeight, height, diffCenter, center, icon.label)
					}
				}
			})
		}
	}
}
