package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"shioji/internal/app"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"strings"
	"testing"
	"time"
)

func TestDevicePathDetailsBelongToEachRoute(t *testing.T) {
	v := devicePathsView()
	c := v.Model.Connections[0]
	c.Routes[0].SSH.Target, c.Routes[0].SSH.Port = "alice@192.168.7.119", 2222
	c.Routes[1].SSH.Target, c.Routes[1].SSH.Port = "bob@100.88.0.105", 2200
	v.Model.Connections[0] = c
	v.Model.Tailnet = tailnet.Snapshot{State: "Running", Peers: []tailnet.Peer{{ID: "peer", Online: true, Measurement: &tailnet.Measurement{Path: "direct", ObservedAt: time.Now()}}}}
	ui := n.NewTester(v.View, 1240, 1100)
	ui.Advance(time.Second)
	for _, route := range c.Routes {
		if err := ui.Click("展开连接路径:" + route.ID); err != nil {
			t.Fatal("each route must expose its own details", err)
		}
		ui.Advance(300 * time.Millisecond)
	}
	for _, text := range []string{"alice", "2222", "bob", "2200", "直接连接"} {
		if !ui.HasText(text) {
			t.Fatal("route's own details missing", text, ui.Texts())
		}
	}
	if ui.HasText("连接详情") {
		t.Fatal("ambiguous device-wide connection details retained")
	}
	if err := ui.Click("设备身份"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	count := 0
	for _, text := range ui.Texts() {
		if text == "B" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("shared device identity must appear once", count)
	}
	// A new active route cannot replace either row's expanded details.
	c.ActiveRouteID = c.Routes[1].ID
	v.Model.Connections[0] = c
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("alice") || !ui.HasText("bob") {
		t.Fatal("handoff replaced per-route details")
	}
	ui.Move(10, 10)
	ui.Advance(250 * time.Millisecond)
	saveNativeSkillImage(t, ui, "device-path-details-light.png")
	ui.SetDark(true)
	ui.Advance(300 * time.Millisecond)
	saveNativeSkillImage(t, ui, "device-path-details-dark.png")
}

func TestDevicePathUsesResolvedSSHLoginWithoutChangingSavedTarget(t *testing.T) {
	v := devicePathsView()
	c := v.Model.Connections[0]
	c.Routes[1].SSH.Target = "100.88.0.105"
	v.Model.Connections[0] = c
	v.Model.DeviceStates["air"].Paths[1].Endpoint = &transport.SSHEndpoint{User: "tailnet-user", Host: "100.88.0.105", Port: 2223}
	ui := n.NewTester(v.View, 850, 900)
	ui.Advance(time.Second)
	if got := devicePathAddress(c.Routes[1].SSH, v.Model.DeviceStates["air"].Paths[1].Endpoint); got != "tailnet-user@100.88.0.105 · 2223" {
		t.Fatal("effective SSH login missing from Tailnet row", got)
	}
	if err := ui.Click("展开连接路径:" + c.Routes[1].ID); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("tailnet-user") || !ui.HasText("2223") {
		t.Fatal("effective login/port missing from that path's details")
	}
	if v.Model.Connections[0].Routes[1].SSH.Target != "100.88.0.105" {
		t.Fatal("display metadata rewrote the connection target")
	}
	ui.Move(10, 10)
	ui.Advance(250 * time.Millisecond)
	saveNativeSkillImage(t, ui, "device-path-details-narrow.png")
}

func devicePathsView() *View {
	m := deviceUIModel()
	m.FirstRun = false
	c := app.DeviceConnection{ID: "air", DeviceID: "B", Name: "MacBook Air", SSH: transport.SSH{Target: "shinya@192.168.7.119", Worker: "/worker"}, OtherSSH: []transport.SSH{{Target: "shinya@100.88.0.105", Worker: "/worker", TailnetPeer: "peer"}}}.Normalized()
	m.Connections = []app.DeviceConnection{c}
	m.DeviceStates["air"].Paths = []app.DevicePathStatus{{ID: c.Routes[0].ID, Phase: app.PathActive, Timing: &app.DeviceTiming{Milliseconds: 4, ObservedAt: time.Now()}}, {ID: c.Routes[1].ID, Phase: app.PathSwitching, Timing: &app.DeviceTiming{Milliseconds: 18, ObservedAt: time.Now()}}}
	m.DeviceStates["air"].Timing = m.DeviceStates["air"].Paths[0].Timing
	v := New(m, app.NewController(""), "")
	v.devices = true
	v.deviceSelected = "air"
	v.deviceTab = "settings"
	return v
}

func TestDevicePathBackgroundSaveCannotOverwriteNewerDraft(t *testing.T) {
	before := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(before)
	v := devicePathsView()
	path := filepath.Join(t.TempDir(), "state.json")
	v.Saver = app.NewStateWriter(path)
	defer v.Saver.Close()
	v.sshRequest = "add"
	v.sshCheck = sshEditing
	v.sshTarget = "shinya@mba.local"
	v.welcome = &welcomeFlow{key: "", step: welcomeSSH}
	v.finishSSHSetup(app.DeviceSetupCheck{Info: transport.WorkerInfo{DeviceID: "B", Worker: "/worker"}})
	v.Model.Drafts["home"] = "newer draft after connection capture"
	v.queueState()
	deadline := time.Now().Add(2 * time.Second)
	for v.deviceConnectionSave != nil {
		v.syncDeviceConnectionSave()
		if time.Now().After(deadline) {
			t.Fatal("save did not complete")
		}
		runtime.Gosched()
	}
	if err := v.Saver.FlushLatest(); err != nil {
		t.Fatal(err)
	}
	m := app.NewModel()
	if err := m.Load(path); err != nil {
		t.Fatal(err)
	}
	if m.Drafts["home"] != "newer draft after connection capture" {
		t.Fatal("background connection save overwrote a newer queued draft", m.Drafts["home"])
	}
}

func TestDevicePathSaveFailureRetainsOriginalAndRetrySucceeds(t *testing.T) {
	v := devicePathsView()
	m := v.Model
	block := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(block, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	v.Saver = app.NewStateWriter(filepath.Join(block, "state.json"))
	v.sshCheck = sshEditing
	v.sshRequest = "add"
	v.sshTarget = "shinya@mba.local"
	v.welcome = &welcomeFlow{key: "", step: welcomeSSH}
	result := app.DeviceSetupCheck{Info: transport.WorkerInfo{DeviceID: "B", Worker: "/worker"}, Catalog: setupCatalog()}
	v.finishSSHSetup(result)
	if v.pathNotice.text != "" || v.sshCheck != sshSaving {
		t.Fatal("success shown before durable save")
	}
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for v.deviceConnectionSave != nil {
			v.syncDeviceConnectionSave()
			if time.Now().After(deadline) {
				t.Fatal("save never finished")
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait()
	v.Saver.Close()
	if len(m.Connections[0].Routes) != 2 || v.sshError == "" || v.pathNotice.text != "" || len(v.Controller.Jobs) > 0 {
		t.Fatal("failed save lost original routes or started a connection")
	}
	path := filepath.Join(t.TempDir(), "client.json")
	v.Saver = app.NewStateWriter(path)
	defer v.Saver.Close()
	v.finishSSHSetup(result)
	wait()
	if len(m.Connections[0].Routes) != 3 || !strings.Contains(v.pathNotice.text, "连接已添加") || v.sshCheck.open() {
		t.Fatal("retry did not merge and confirm saved connection", v.sshError)
	}
	loaded := app.NewModel()
	if err := loaded.Load(path); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Connections) != 1 || len(loaded.Connections[0].Routes) != 3 {
		t.Fatal("merged paths not persisted")
	}
}

func TestDevicePathRepeatedAddShowsExistingAndWrongIdentityIsRejected(t *testing.T) {
	v := devicePathsView()
	v.sshCheck = sshEditing
	v.sshTarget = "shinya@192.168.7.119"
	v.welcome = &welcomeFlow{key: "", step: welcomeSSH}
	v.finishSSHSetup(app.DeviceSetupCheck{Info: transport.WorkerInfo{DeviceID: "B", Worker: "/worker"}})
	if len(v.Model.Connections[0].Routes) != 2 || !strings.Contains(v.pathNotice.text, "已存在") {
		t.Fatal("same endpoint duplicated or reported as new")
	}
	v.pathNotice = devicePathNotice{}
	v.sshCheck = sshEditing
	v.sshExpectedDevice = "B"
	v.finishSSHSetup(app.DeviceSetupCheck{Info: transport.WorkerInfo{DeviceID: "other-worker", Worker: "/worker"}})
	if len(v.Model.Connections) != 1 || v.sshError == "" || v.pathNotice.text != "" {
		t.Fatal("path for a different worker merged")
	}
}

func TestDevicePathRowsAlign(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []float32{1240, 850} {
			v := devicePathsView()
			ui := n.NewTester(v.View, int(width), 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			scene, ok := ui.Find("设备连接图")
			if !ok {
				t.Fatal("native scene missing")
			}
			var left, right float32
			for index, route := range v.Model.Connections[0].Routes {
				hit, ok := ui.Find("展开连接路径:" + route.ID)
				if !ok || hit.X < scene.X || hit.X+hit.W > scene.X+scene.W+1 {
					t.Fatal("route hit bounds detached", hit, scene)
				}
				if index == 0 {
					left, right = hit.X, hit.X+hit.W
				} else if math.Abs(float64(hit.X-left)) > 1 || math.Abs(float64(hit.X+hit.W-right)) > 1 {
					t.Fatal("route columns drift", hit, left, right)
				}
				for _, label := range []string{"路径名称:", "路径延迟:", "路径状态:"} {
					text, ok := ui.Find(label + route.ID)
					if !ok || text.X < hit.X-1 || text.X+text.W > hit.X+hit.W+1 || text.Y < hit.Y || text.Y+text.H > hit.Y+hit.H+1 {
						t.Fatal("route text escapes its own hit region", label, text, hit)
					}
				}
				if _, ok := ui.Find("路径地址:" + route.ID); ok {
					t.Fatal("collapsed scene retains old table")
				}
			}
			theme := "light"
			if dark {
				theme = "dark"
			}
			saveNativeSkillImage(t, ui, "device-paths-"+theme+"-"+fmtWidth(width)+".png")
		}
	}
}

func TestDevicePathNormalAddAutomaticallyMergesBeforeAgentSetup(t *testing.T) {
	for _, peer := range []string{"", "peer"} {
		v := devicePathsView()
		m := v.Model
		v.sshTailnetPeer = peer
		v.sshTarget = "shinya@mba.local"
		if peer != "" {
			v.sshTarget = "shinya@100.88.0.106"
		}
		v.sshName = "Should not rename"
		v.sshCheck = sshEditing
		v.welcome = &welcomeFlow{key: "", step: welcomeSSH}
		v.finishSSHSetup(app.DeviceSetupCheck{Info: transport.WorkerInfo{DeviceID: "B", Worker: "/worker", Directory: "/state"}, Catalog: setupCatalog()})
		if len(m.Connections) != 1 || len(m.Connections[0].Routes) != 3 || m.Connections[0].Name != "MacBook Air" {
			t.Fatal("normal add did not merge into existing device", m.Connections)
		}
		if v.sshCheck.open() || v.welcome != nil {
			t.Fatal("same device re-entered Agent configuration")
		}
	}
}

func fmtWidth(width float32) string {
	if width == 850 {
		return "850"
	}
	return "1240"
}

func TestDevicePathHeadingActionSharesVisibleCenter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := devicePathsView()
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		ink := func(label string) float64 {
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal(label)
			}
			im := ui.Image()
			lo, hi := im.Bounds().Max.Y, -1
			for y := int(r.Y); y < int(r.Y+r.H); y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					px := im.RGBAAt(x, y)
					hit := px.R < 230
					if dark {
						hit = px.R > 56
					}
					if hit {
						lo = min(lo, y)
						hi = max(hi, y)
					}
				}
			}
			if hi < lo {
				t.Fatal("missing heading ink", label)
			}
			return float64(lo+hi) / 2
		}
		a, b := ink("重新检测"), ink("添加连接路径")
		if math.Abs(a-b) > 1 {
			t.Fatal("heading caption and add icon do not share glyph center", a, b)
		}
	}
}
