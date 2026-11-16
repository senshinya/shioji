package ui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/tailnet"
)

func readyDeviceSceneView() *View {
	v := devicePathsView()
	s := v.Model.DeviceStates["air"]
	s.ConnectionEpoch = ""
	s.Readiness = domain.Readiness{State: domain.RuntimeReady, Harnesses: []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady}}}
	s.Paths[1].Phase = app.PathReady
	v.Model.Tailnet = tailnet.Snapshot{State: "Running", Peers: []tailnet.Peer{{ID: "peer", Online: true, Measurement: &tailnet.Measurement{Path: "derp", Region: "nrt", RegionName: "Tokyo", Milliseconds: 30, ObservedAt: time.Now()}}}}
	return v
}

// Removing route identity from the scene or using the selected route to build
// every detail panel would expose the wrong SSH credentials after handoff.
func TestDeviceStatusSceneShowsRealRoutesAndKeepsDetailsOnTheirRoute(t *testing.T) {
	v := readyDeviceSceneView()
	ui := n.NewTester(v.View, 1240, 1000)
	ui.Advance(time.Second)
	stage, ok := ui.Find("设备连接图")
	if !ok || stage.W < 750 {
		t.Fatal("missing full-width native scene", stage)
	}
	if !ui.HasText("设备已就绪") {
		t.Fatal("ready worker and Agent not reflected", ui.Texts())
	}
	connection := v.Model.Connections[0]
	for _, route := range connection.Routes {
		hit, ok := ui.Find("展开连接路径:" + route.ID)
		if !ok || hit.X < stage.X || hit.X+hit.W > stage.X+stage.W+1 || hit.Y < stage.Y || hit.Y+hit.H > stage.Y+stage.H+1 {
			t.Fatal("route hit target detached from diagram", hit, stage)
		}
		if _, ok := ui.Find("路径地址:" + route.ID); ok {
			t.Fatal("collapsed scene repeats the old address table")
		}
		if err := ui.Click("展开连接路径:" + route.ID); err != nil {
			t.Fatal(err)
		}
		ui.Advance(300 * time.Millisecond)
	}
	if !ui.HasText("shinya") || !ui.HasText("DERP 中继 · Tokyo (nrt)") {
		t.Fatal("owning route details missing", ui.Texts())
	}
	s := v.Model.DeviceStates["air"]
	s.Paths[0].Phase = app.PathReady
	s.Paths[1].Phase = app.PathActive
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	for _, route := range connection.Routes {
		if _, ok := ui.Find("路径地址:" + route.ID); !ok {
			t.Fatal("handoff discarded open route details")
		}
	}
}

func TestDeviceStatusSceneNeverTreatsCachedReadinessAsLive(t *testing.T) {
	for _, tc := range []struct {
		name       string
		connected  bool
		agent      domain.ReadinessState
		configured bool
		want       string
	}{
		{"ready", true, domain.RuntimeReady, true, "设备已就绪"},
		{"offline", false, domain.RuntimeReady, true, "连接已断开"},
		{"unavailable", true, domain.RuntimeUnavailable, true, "Agent 不可用"},
		{"refreshing", true, domain.RuntimeRefreshing, true, "正在确认 Agent"},
		{"disabled", true, domain.RuntimeReady, false, "配置 Agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := readyDeviceSceneView()
			s := v.Model.DeviceStates["air"]
			s.Connected = tc.connected
			s.Readiness.Harnesses[0].State = tc.agent
			s.Readiness.Harnesses[0].Configured = tc.configured
			ui := n.NewTester(v.View, 1240, 900)
			ui.Advance(time.Second)
			if !ui.HasText(tc.want) {
				t.Fatal("readiness boundary misrepresented", tc.want, ui.Texts())
			}
			if tc.name != "ready" && ui.HasText("设备已就绪") {
				t.Fatal("claimed readiness without all prerequisites")
			}
		})
	}
}

func TestDeviceStatusSceneEdgesAndTargetFollowSelectedIcon(t *testing.T) {
	for _, width := range []int{600, 850, 1240} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-%v", width, dark), func(t *testing.T) {
				v := readyDeviceSceneView()
				ui := n.NewTester(v.View, width, 1000)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				stage, ok := ui.Find("设备状态区域")
				if !ok {
					t.Fatal("status area missing")
				}
				heading, _ := ui.Find("工作区")
				if math.Abs(float64(stage.X-heading.X)) > 1 {
					t.Fatal("section reading edges differ", stage, heading)
				}
				for _, icon := range []app.DeviceIcon{app.DeviceIconMonitor, app.DeviceIconLaptop, app.DeviceIconServer, app.DeviceIconPhone, app.DeviceIconTablet} {
					if err := v.Model.SetDeviceIcon("air", icon); err != nil {
						t.Fatal(err)
					}
					ui.Frame()
					ui.Advance(300 * time.Millisecond)
					saveNativeSkillImage(t, ui, fmt.Sprintf("scene-%s-%d-%v.png", icon, width, dark))
					if _, ok := ui.Find("目标设备:" + string(icon)); !ok {
						t.Fatal("selected icon not reflected by native target", icon, ui.Texts())
					}
				}
			})
		}
	}
}

func TestDeviceSceneObservationBoundaries(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name   string
		sample *app.DeviceTiming
		online bool
		phase  app.DevicePathPhase
		want   string
	}{
		{"unknown", nil, true, app.PathActive, "—"},
		{"current", &app.DeviceTiming{Milliseconds: 0, ObservedAt: now}, true, app.PathActive, "0 ms"},
		{"stale", &app.DeviceTiming{Milliseconds: 18, ObservedAt: now.Add(-16 * time.Second)}, true, app.PathReady, "上次 18 ms"},
		{"offline", &app.DeviceTiming{Milliseconds: 18, ObservedAt: now}, false, app.PathActive, "上次 18 ms"},
		{"failed", &app.DeviceTiming{Milliseconds: 18, ObservedAt: now}, true, app.PathUnavailable, "上次 18 ms"},
		{"future", &app.DeviceTiming{Milliseconds: 18, ObservedAt: now.Add(time.Second)}, true, app.PathActive, "—"},
		{"invalid", &app.DeviceTiming{Milliseconds: math.NaN(), ObservedAt: now}, true, app.PathActive, "—"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deviceSceneTiming(app.DevicePathStatus{Timing: tc.sample, Phase: tc.phase}, tc.online, now); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	v := readyDeviceSceneView()
	s := v.Model.DeviceStates["air"]
	for _, tc := range []struct {
		path   string
		age    time.Duration
		online bool
		want   string
	}{
		{"direct", 0, true, "直连"}, {"derp", 0, true, "DERP · Tokyo (nrt)"}, {"peer-relay", 0, true, "Peer Relay"},
		{"", 0, true, "路径未知"}, {"derp", -31 * time.Second, true, "上次 · DERP · Tokyo (nrt)"}, {"direct", 0, false, "上次 · 直连"},
		{"derp", time.Second, true, "路径未知"},
	} {
		s.Connected = tc.online
		v.Model.Tailnet.Peers[0].Measurement.Path = tc.path
		v.Model.Tailnet.Peers[0].Measurement.ObservedAt = now.Add(tc.age)
		p := v.deviceSceneProjection("air", s, now)
		if p.Routes[1].Network != tc.want {
			t.Fatal(tc, p.Routes[1])
		}
	}
}

func TestDeviceSceneRecheckIsExplicitAndLateReplyCannotCrossEpoch(t *testing.T) {
	v := readyDeviceSceneView()
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "readiness" || job.Method == "$reconnect" {
			t.Fatal("render initiated a recheck")
		}
	}
	if err := ui.Click("重新检测设备"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "readiness")
	if job.ConnectionID != "air" {
		t.Fatal("recheck escaped device")
	}
	ui.Frame()
	if !ui.HasText("正在检测设备") {
		t.Fatal("pending state invisible")
	}
	v.Model.DeviceStates["air"].ConnectionEpoch = "replacement"
	ui.Frame()
	body := []byte(`{"state":"unavailable","harnesses":[]}`)
	job.Apply(v.Model, body, nil)
	ui.Frame()
	if v.Model.DeviceStates["air"].Readiness.State != domain.RuntimeReady {
		t.Fatal("late reply replaced current epoch")
	}
	if ui.HasText("正在检测设备") {
		t.Fatal("stale request remained pending")
	}
}

func TestDeviceSceneKeyboardOpensFocusedRouteAndReducedMotionSettles(t *testing.T) {
	v := readyDeviceSceneView()
	ui := n.NewTester(v.View, 1240, 1000)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Advance(time.Second)
	route := v.Model.Connections[0].Routes[0]
	if err := ui.Click("展开连接路径:" + route.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := ui.Find("路径地址:" + route.ID); !ok {
		t.Fatal("reduced motion did not immediately open details")
	}
	ui.Key(0, n.KeySpace)
	if _, ok := ui.Find("路径地址:" + route.ID); ok {
		t.Fatal("Space did not close focused route")
	}
	ui.Key(0, n.KeySpace)
	if _, ok := ui.Find("路径地址:" + route.ID); !ok {
		t.Fatal("Space did not open focused route")
	}
	ui.Move(0, 0)
	ui.Advance(time.Second)
	if ui.FramePending() {
		t.Fatal("static reduced-motion scene keeps requesting frames")
	}
}

// Captures use the production native View and renderer with explicit state
// fixtures. They demonstrate presentation and interactions, not real networking.
func TestDeviceSceneNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_DEVICE_SCENE_RECORDING")
	if dir == "" {
		t.Skip("opt-in native recording")
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v := readyDeviceSceneView()
			v.Model.SetDeviceIcon("air", app.DeviceIconServer)
			ui := n.NewTester(v.View, 1240, 1000)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprint(dark)))
			frames := 0
			record := func(count int) {
				for i := 0; i < count; i++ {
					ui.Advance(time.Second / 30)
					saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frames))
					frames++
				}
			}
			record(24)
			for _, icon := range []app.DeviceIcon{app.DeviceIconPhone, app.DeviceIconTablet, app.DeviceIconLaptop, app.DeviceIconMonitor, app.DeviceIconServer} {
				v.Model.SetDeviceIcon("air", icon)
				ui.Frame()
				record(24)
			}
			route := v.Model.Connections[0].Routes[1]
			ui.Click("展开连接路径:" + route.ID)
			record(24)
			ui.Click("收起连接路径:" + route.ID)
			record(20)
			v.Model.DeviceStates["air"].Paths[0].Phase = app.PathReady
			v.Model.DeviceStates["air"].Paths[1].Phase = app.PathActive
			ui.Frame()
			record(36)
			ui.Click("重新检测设备")
			record(24)
			job := welcomeJob(t, v.Controller, "readiness")
			v.Model.ApplyDeviceUpdate("air", func(m *app.Model) {
				job.Apply(m, []byte(`{"state":"ready","harnesses":[{"id":"custom","configured":true,"state":"ready"}]}`), nil)
			})
			ui.Frame()
			record(36)
		})
	}
}

func TestDeviceSceneSectionsShareEdgesAndVerticalRhythm(t *testing.T) {
	for _, width := range []int{600, 850, 1240} {
		v := readyDeviceSceneView()
		ui := n.NewTester(v.View, width, 1100)
		ui.Advance(time.Second)
		previous, ok := ui.Find("设备状态区域")
		if !ok {
			t.Fatal("scene missing")
		}
		for _, label := range []string{"设备工作区区域", "设备执行服务区域", "存储区域"} {
			rect, ok := ui.Find(label)
			if !ok {
				t.Fatal(label)
			}
			if math.Abs(float64(rect.X-previous.X)) > 1 || math.Abs(float64(rect.X+rect.W-previous.X-previous.W)) > 1 {
				t.Fatal("section edges drift", label, previous, rect)
			}
			if math.Abs(float64(rect.Y-previous.Y-previous.H-configurationSectionGap)) > 1 {
				t.Fatal("section vertical rhythm drifts", label, previous, rect)
			}
			previous = rect
		}
		add, _ := ui.Find("添加连接路径")
		workspace, _ := ui.Find("新建工作区")
		if math.Abs(float64(add.X+add.W/2-workspace.X-workspace.W/2)) > 1 {
			t.Fatal("section action glyph columns differ", add, workspace)
		}
	}
}

func TestDeviceSceneScreenStatusUsesNativeCapAlignment(t *testing.T) {
	for _, width := range []int{600, 1240} {
		for _, dark := range []bool{false, true} {
			for _, icon := range []app.DeviceIcon{app.DeviceIconMonitor, app.DeviceIconLaptop, app.DeviceIconServer, app.DeviceIconPhone, app.DeviceIconTablet} {
				v := readyDeviceSceneView()
				v.Model.SetDeviceIcon("air", icon)
				ui := n.NewTester(v.View, width, 900)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				for _, kind := range []string{"Worker", "Agent"} {
					text, ok := ui.Find("屏幕" + kind + "名称")
					if !ok {
						t.Fatal(kind)
					}
					glyph, ok := ui.Find("屏幕" + kind + "就绪")
					if !ok {
						t.Fatal(kind)
					}
					if text.X+text.W+3 > glyph.X || math.Abs(float64(text.Y-glyph.Y)) > 1 {
						t.Fatal("screen label and status collide", icon, text, glyph)
					}

				}
			}
		}
	}
}

func TestDeviceSceneScreenMarkMatchesHAndCJKReferenceInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, name := range []string{"H", "国"} {
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Padding(20).Children(func() { deviceSceneScreenRow(c, c.Theme(), "reference", name, "", true, false).Width(120) })
			}, 180, 80)
			ui.SetDark(dark)
			text, _ := ui.Find("屏幕reference名称")
			glyph, _ := ui.Find("屏幕reference就绪")
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
					t.Fatal("missing native ink")
				}
				return float64(lo+hi) / 2
			}
			if delta := center(text) - center(glyph); math.Abs(delta) > 1 {
				t.Fatal("screen mark cap alignment", dark, name, delta)
			}
		}
	}
}

func TestDeviceScenePeerRelayRemainsPeerRelayWhenStale(t *testing.T) {
	v := readyDeviceSceneView()
	m := v.Model.Tailnet.Peers[0].Measurement
	m.Path = "peer-relay"
	m.ObservedAt = time.Now().Add(-time.Minute)
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	if !ui.HasText("Relay") || ui.HasText("DERP") {
		t.Fatal("stale peer relay was mislabeled DERP", ui.Texts())
	}
}

func TestDeviceSceneExpandedMeasurementsRejectFutureObservations(t *testing.T) {
	now := time.Now()
	peer := tailnet.Peer{Online: true, Measurement: &tailnet.Measurement{Path: "derp", Region: "nrt", RegionName: "Tokyo", Milliseconds: 18, ObservedAt: now.Add(time.Second)}}
	if got := tailnetPathLabel(peer, now); got != "尚未确认" {
		t.Fatal("future path displayed as observed", got)
	}
	if got := tailnetLatency(peer, now); got != "尚未测量" {
		t.Fatal("future RTT displayed as observed", got)
	}
}

func TestDeviceSceneOneAndMultipleRoutesKeepDistinctEndpointsAndHitRegions(t *testing.T) {
	for _, count := range []int{1, 4} {
		v := readyDeviceSceneView()
		connection := v.Model.Connections[0]
		connection.Routes = connection.Routes[:1]
		for i := 1; i < count; i++ {
			route := connection.Routes[0]
			route.ID = fmt.Sprint("opaque-", i)
			route.SSH.Target = fmt.Sprintf("developer@192.168.7.%d", 120+i)
			connection.Routes = append(connection.Routes, route)
		}
		v.Model.Connections[0] = connection
		state := v.Model.DeviceStates["air"]
		state.Paths = state.Paths[:1]
		for _, route := range connection.Routes[1:] {
			state.Paths = append(state.Paths, app.DevicePathStatus{ID: route.ID, Phase: app.PathReady})
		}
		ui := n.NewTester(v.View, 600, 1300)
		ui.Advance(time.Second)
		diagram, _ := ui.Find("设备连接图")
		lastBottom := float32(0)
		for _, route := range connection.Routes {
			hit, ok := ui.Find("展开连接路径:" + route.ID)
			if !ok || hit.Y < lastBottom || hit.Y+hit.H > diagram.Y+diagram.H+1 {
				t.Fatal("route regions overlap or escape", count, hit, diagram)
			}
			lastBottom = hit.Y + hit.H
			if count > 1 {
				if _, ok := ui.Find("网络路径:" + route.ID); !ok {
					t.Fatal("same-kind routes lack endpoint distinction")
				}
			}
			if err := ui.Click("展开连接路径:" + route.ID); err != nil {
				t.Fatal(err)
			}
			ui.Advance(250 * time.Millisecond)
			if !ui.HasText(route.SSH.Target) {
				t.Fatal("opened a different route's endpoint", route.ID, route.SSH.Target)
			}
		}
	}
}

func TestDeviceSceneRecheckSurvivesOwningDeviceProjection(t *testing.T) {
	v := readyDeviceSceneView()
	ui := n.NewTester(v.View, 1240, 1000)
	ui.Advance(time.Second)
	localBefore := v.Model.DeviceStates["local"].Readiness.State
	if err := ui.Click("重新检测设备"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "readiness")
	v.Model.ApplyDeviceUpdate("air", func(m *app.Model) {
		job.Apply(m, []byte(`{"state":"unavailable","harnesses":[{"id":"custom","configured":true,"state":"unavailable"}]}`), nil)
	})
	ui.Frame()
	ui.Advance(time.Second)
	if v.Model.DeviceStates["air"].Readiness.State != domain.RuntimeUnavailable || !ui.HasText("Agent 不可用") {
		t.Fatal("recheck reply lost in production device update boundary", ui.Texts())
	}
	if v.Model.DeviceStates["local"].Readiness.State != localBefore {
		t.Fatal("recheck changed another device")
	}
}
