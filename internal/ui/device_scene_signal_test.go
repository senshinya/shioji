package ui

import (
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"strconv"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestDeviceSceneInputPortsTouchHardware(t *testing.T) {
	for kind, edge := range map[string]float64{"monitor": 350, "laptop": 350, "server": 350, "phone": 421, "tablet": 370} {
		source, err := deviceSceneAssets.ReadFile("assets/device-scene/target-" + kind + ".svg")
		if err != nil {
			t.Fatal(err)
		}
		decoder := xml.NewDecoder(bytes.NewReader(source))
		found := false
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			node, ok := token.(xml.StartElement)
			if !ok || node.Name.Local != "circle" {
				continue
			}
			attrs := map[string]string{}
			for _, a := range node.Attr {
				attrs[a.Name.Local] = a.Value
			}
			if attrs["class"] != "device-input-port" {
				continue
			}
			cx, _ := strconv.ParseFloat(attrs["cx"], 64)
			r, _ := strconv.ParseFloat(attrs["r"], 64)
			if math.Abs(cx-float64(deviceSceneInputX(app.DeviceIcon(kind)))) > .01 {
				t.Fatal("drawn route endpoint differs from hardware port", kind, cx)
			}
			if math.Abs(cx+r-edge) > .01 {
				t.Fatal("input port floats away from hardware", kind, cx, r, edge)
			}
			found = true
		}
		if !found {
			t.Fatal("input port missing", kind)
		}
	}
}

func deviceSignalBand(ui *n.Tester, lane float32) []byte {
	scene, _ := ui.Find("设备连接图")
	scale := scene.W / 600
	im := ui.Image()
	var pixels []byte
	for y := int(scene.Y + (lane-3)*scale); y < int(scene.Y+(lane+3)*scale); y++ {
		for x := int(scene.X + 123*scale); x < int(scene.X+319*scale); x++ {
			p := im.RGBAAt(x, y)
			pixels = append(pixels, p.R, p.G, p.B)
		}
	}
	return pixels
}

func TestDeviceSceneCurrentRouteKeepsTransmittingAfterTransition(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, active := range []int{0, 1} {
			v := readyDeviceSceneView()
			state := v.Model.DeviceStates["air"]
			for i := range state.Paths {
				state.Paths[i].Phase = "ready"
				if i == active {
					state.Paths[i].Phase = "active"
				}
			}
			ui := n.NewTester(v.View, 1240, 1000)
			ui.SetDark(dark)
			ui.Advance(3 * time.Second)
			lanes := []float32{66, 144}
			prior := deviceSignalBand(ui, lanes[active])
			backup := deviceSignalBand(ui, lanes[1-active])
			moved := false
			for i := 0; i < 24; i++ {
				ui.Advance(100 * time.Millisecond)
				next := deviceSignalBand(ui, lanes[active])
				moved = moved || !bytes.Equal(prior, next)
				prior = next
				if !bytes.Equal(backup, deviceSignalBand(ui, lanes[1-active])) {
					t.Fatal("standby route shows a transmission signal", active)
				}
			}
			if !moved {
				t.Fatal("active signal stopped after the transition", active)
			}
			ui.SetPreferences(n.Preferences{ReduceMotion: true})
			ui.Advance(time.Second)
			still := deviceSignalBand(ui, lanes[active])
			ui.Advance(500 * time.Millisecond)
			if !bytes.Equal(still, deviceSignalBand(ui, lanes[active])) {
				t.Fatal("reduced motion still sweeps the active route")
			}
			state.Connected = false
			ui.SetPreferences(n.Preferences{})
			ui.Frame()
			ui.Advance(time.Second)
			offline := deviceSignalBand(ui, lanes[active])
			ui.Advance(500 * time.Millisecond)
			if !bytes.Equal(offline, deviceSignalBand(ui, lanes[active])) {
				t.Fatal("disconnected route still transmits")
			}
			state.Connected = true
			ui.Frame()
			ui.Advance(time.Second)
			ui.Click("工作区")
			ui.Advance(time.Second)
			if ui.FramePending() {
				t.Fatal("hidden device scene keeps requesting animation frames")
			}
		}
	}
}

func TestDeviceSceneSignalReachesHandheldConnector(t *testing.T) {
	v := readyDeviceSceneView()
	v.Model.SetDeviceIcon("air", "phone")
	ui := n.NewTester(v.View, 850, 1000)
	ui.Advance(3 * time.Second)
	sample := func() []byte {
		scene, _ := ui.Find("设备连接图")
		s := scene.W / 600
		im := ui.Image()
		var out []byte
		for y := int(scene.Y + 103*s); y < int(scene.Y+109*s); y++ {
			for x := int(scene.X + 355*s); x < int(scene.X+410*s); x++ {
				p := im.RGBAAt(x, y)
				out = append(out, p.R, p.G, p.B)
			}
		}
		return out
	}
	previous := sample()
	moved := false
	for i := 0; i < 40; i++ {
		ui.Advance(100 * time.Millisecond)
		next := sample()
		moved = moved || !bytes.Equal(previous, next)
		previous = next
	}
	if !moved {
		t.Fatal("active signal stops before the phone's connector")
	}
}
