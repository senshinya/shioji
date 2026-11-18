package ui

import (
	"fmt"
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
			v.drawDeviceScene(painter, box, theme, app.DeviceIconServer, p, height, lanes, false, true)
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

func TestDeviceSceneEveryHardwareScreenIncludesNativeRuntimeIcons(t *testing.T) {
	screens := map[app.DeviceIcon]n.Rect{
		app.DeviceIconMonitor: {X: 355, Y: 45, W: 222, H: 140}, app.DeviceIconLaptop: {X: 355, Y: 60, W: 222, H: 140},
		app.DeviceIconServer: {X: 413, Y: 33, W: 106, H: 63}, app.DeviceIconPhone: {X: 427, Y: 33, W: 78, H: 172}, app.DeviceIconTablet: {X: 377, Y: 56, W: 178, H: 128},
	}
	for _, width := range []int{600, 850, 1240} {
		for _, dark := range []bool{false, true} {
			for icon, screen := range screens {
				v := readyDeviceSceneView()
				v.Model.SetDeviceIcon("air", icon)
				ui := n.NewTester(v.View, width, 1000)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				diagram, _ := ui.Find("设备连接图")
				scale := diagram.W / 600
				physical := n.Rect{X: diagram.X + screen.X*scale, Y: diagram.Y + (screen.Y-25)*scale, W: screen.W * scale, H: screen.H * scale}
				for _, kind := range []string{"Worker", "Agent"} {
					glyph, ok := ui.Find("屏幕" + kind + "图标")
					if !ok {
						t.Fatal("runtime icon missing", width, dark, icon, kind)
					}
					text, _ := ui.Find("屏幕" + kind + "名称")
					if glyph.X < physical.X || glyph.Y < physical.Y || glyph.X+glyph.W > physical.X+physical.W || glyph.Y+glyph.H > physical.Y+physical.H {
						t.Fatal("runtime icon escapes physical screen", width, dark, icon, kind, glyph, physical)
					}
					if glyph.X+glyph.W+1.5 > text.X || math.Abs(float64(glyph.Y-text.Y)) > 1 {
						t.Fatal("icon crowds label or changes baseline", width, dark, icon, kind, glyph, text)
					}
				}
			}
		}
	}
}

func TestDeviceSceneRuntimeIconsShareNativeCapBaseline(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, kind := range []string{"Worker", "Agent"} {
			for _, caption := range []string{"H", "国"} {
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Padding(20).Children(func() { deviceSceneScreenRow(c, c.Theme(), kind, caption, "", true, false).Width(140) })
				}, 200, 80)
				ui.SetDark(dark)
				text, _ := ui.Find("屏幕" + kind + "名称")
				glyph, ok := ui.Find("屏幕" + kind + "图标")
				if !ok {
					t.Fatal("runtime icon missing", kind)
				}
				im := ui.Image()
				bg := im.RGBAAt(10, 10)
				center := func(r n.Rect) float64 {
					lo, hi := im.Bounds().Max.Y, -1
					for y := int(r.Y); y < int(r.Y+r.H); y++ {
						for x := int(r.X); x < int(r.X+r.W); x++ {
							p := im.RGBAAt(x, y)
							if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
								lo = min(lo, y)
								hi = max(hi, y)
							}
						}
					}
					if hi < lo {
						t.Fatal("runtime icon has no visible ink", kind)
					}
					return float64(lo+hi) / 2
				}
				if delta := center(glyph) - center(text); math.Abs(delta) > 1 {
					t.Fatal("runtime icon ink differs from native cap/ideographic center", dark, kind, caption, delta)
				}
			}
		}
	}
}

func TestDeviceSceneTargetPortStaysAboveIncomingWire(t *testing.T) {
	for _, dark := range []bool{false, true} {
		actual := deviceScenePaintFixture(0, 1, dark)
		actual.SetDark(dark)
		hardware := n.NewTester(func(c *n.Context) {
			t := c.Theme()
			n.Column(c).Fill().Draw(func(p *n.Painter, box n.Rect) {
				p.Image(deviceSceneTarget(t, app.DeviceIconServer, true), box, n.Contain)
			})
		}, 600, 210)
		hardware.SetDark(dark)
		// The input port's filled center belongs to the hardware, and must not be
		// crossed by an overpainted wire even when that route is currently active.
		for _, y := range []int{105, 106} {
			a, b := actual.Image().RGBAAt(347, y), hardware.Image().RGBAAt(347, y)
			if a != b {
				t.Fatal("incoming route paints over target port", dark, y, a, b)
			}
		}
	}
}

func TestDeviceWorkspacesHaveIndependentTabAfterAgent(t *testing.T) {
	for _, width := range []int{600, 850, 1240} {
		for _, dark := range []bool{false, true} {
			v := readyDeviceSceneView()
			v.Model.Drafts["home"] = "保留未发送的首页草稿"
			ui := n.NewTester(v.View, width, 1000)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			agent, _ := ui.Find("Agent")
			workspace, ok := ui.Find("工作区")
			if !ok || workspace.X <= agent.X+agent.W || math.Abs(float64(workspace.Y-agent.Y)) > 1 {
				t.Fatal("workspace is not a tab after Agent", width, agent, workspace)
			}
			if _, ok := ui.Find("设备工作区区域"); ok {
				t.Fatal("device settings still contain the workspace list")
			}
			ui.Click("工作区")
			ui.Advance(configurationTabDuration)
			if _, ok := ui.Find("设备工作区区域"); !ok {
				t.Fatal("workspace tab has no list", ui.Texts())
			}
			if _, ok := ui.Find("设备状态区域"); ok {
				t.Fatal("workspace tab repeats device settings")
			}
			if _, ok := ui.Find("打开工作区：Remote"); !ok {
				t.Fatal("owning device workspace missing")
			}
			if _, ok := ui.Find("打开工作区：Local"); ok {
				t.Fatal("workspace tab lists another device's workspace")
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("workspace-tab-%d-%v.png", width, dark))
			if err := ui.Click("新建工作区"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if v.remoteWorkspaceConnection != "air" {
				t.Fatal("new workspace escaped selected device", v.remoteWorkspaceConnection)
			}
			if v.Model.Drafts["home"] != "保留未发送的首页草稿" {
				t.Fatal("workspace tab consumed home draft")
			}
		}
	}
}
