package ui

import (
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func deviceScenePaintFixture(active, count int, dark bool) *n.Tester {
	v := readyDeviceSceneView()
	state := v.Model.DeviceStates["air"]
	connection := v.Model.Connections[0]
	connection.Routes = connection.Routes[:count]
	v.Model.Connections[0] = connection
	for i := range state.Paths {
		state.Paths[i].Phase = app.PathReady
		if i == active {
			state.Paths[i].Phase = app.PathActive
		}
	}
	return n.NewTester(func(c *n.Context) {
		// Render only the production Painter; shared trunks must be independent of
		// row order, text, focus and dynamic labels.
		p := v.deviceSceneProjection("air", state, c.Now())
		height, lanes := deviceSceneGeometry(count)
		theme := c.Theme()
		n.Column(c).Fill().Draw(func(painter *n.Painter, box n.Rect) {
			v.drawDeviceScene(painter, box, theme, app.DeviceIconServer, p, height, lanes, false, time.Time{}, true)
		})
	}, 600, 210)
}

func TestDeviceSceneSharedTrunksKeepActiveRouteAboveBackup(t *testing.T) {
	for _, dark := range []bool{false, true} {
		upper := deviceScenePaintFixture(0, 2, dark)
		lower := deviceScenePaintFixture(1, 2, dark)
		upper.SetDark(dark)
		lower.SetDark(dark)
		for _, x := range []int{90, 349} {
			for y := 104; y <= 107; y++ {
				a, b := upper.Image().RGBAAt(x, y), lower.Image().RGBAAt(x, y)
				if max(absChannel(a.R, b.R), absChannel(a.G, b.G), absChannel(a.B, b.B)) > 2 {
					t.Fatal("backup paint covers active shared trunk", dark, x, y, a, b)
				}
			}
		}
	}
}

func TestDeviceSceneSharedTrunksMeetHardwarePortCenter(t *testing.T) {
	ui := deviceScenePaintFixture(0, 1, false)
	im := ui.Image()
	for _, x := range []int{90, 349} {
		weight, position := float64(0), float64(0)
		for y := 100; y < 112; y++ {
			p := im.RGBAAt(x, y)
			w := float64(255 - p.R)
			weight += w
			position += (float64(y) + .5) * w
		}
		// Every hardware asset uses viewBox y=25 and connector y=131.
		if weight == 0 || math.Abs(position/weight-106) > .5 {
			t.Fatal("line is vertically offset from the hardware port", x, position/weight)
		}
	}
}
