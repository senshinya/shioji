package ui

import (
	"shioji/internal/app"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestDeviceIconItselfOpensMenuAndChangesExistingDevice(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("设备图标"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	for _, label := range []string{"显示器", "笔记本", "平板", "手机", "服务器"} {
		if !ui.HasText(label) {
			t.Fatal("missing icon choice", label)
		}
	}
	ui.Click("笔记本")
	ui.Advance(time.Second)
	if m.DeviceIconChoice("air") != app.DeviceIconLaptop {
		t.Fatal("icon was not applied")
	}
	if m.DeviceIconChoice("local") != app.DeviceIconAuto {
		t.Fatal("remote icon changed local choice")
	}
	if ui.HasText("服务器") {
		t.Fatal("selection did not close menu")
	}
	ui.Click("设备图标")
	ui.Advance(time.Second)
	ui.Click("服务器")
	ui.Advance(time.Second)
	if m.DeviceIconChoice("air") != app.DeviceIconServer {
		t.Fatal("cannot change an existing icon choice")
	}
}

func TestDeviceIconPaletteContainsOnlyFiveCompactIcons(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	v.devices = true
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("设备图标")
	ui.Advance(time.Second)
	if _, ok := ui.Find("自动"); ok {
		t.Fatal("automatic choice must not be exposed")
	}
	var first n.Rect
	for i, label := range []string{"显示器", "笔记本", "平板", "手机", "服务器"} {
		r, ok := ui.Find(label)
		if !ok {
			t.Fatal(label)
		}
		if r.W != 28 || r.H != 28 || i > 0 && r.Y != first.Y {
			t.Fatal("icon choices should share one compact row", label, r)
		}
		if i == 0 {
			first = r
		}
	}
}

func TestAddingDevicePreselectsConcreteIconFromPeerSystem(t *testing.T) {
	m := deviceUIModel()
	m.Tailnet = tailnet.Snapshot{State: "Running", Peers: []tailnet.Peer{{ID: "peer", OS: "macOS"}}}
	v := New(m, nil, "")
	v.beginTailnet()
	v.sshTailnetPeer = "peer"
	v.saveDeviceConnection(app.DeviceConnection{ID: "added", DeviceID: "new-device", Name: "新设备", SSH: transport.SSH{Target: "new-host", TailnetPeer: "peer"}}, false)
	if m.DeviceIconChoice("added") != app.DeviceIconMonitor {
		t.Fatal("default must be selected when adding, not an automatic menu item")
	}
}

func TestCollectionDeviceIconOpensMenuWithoutNavigating(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("设备图标：MacBook Air"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.deviceSelected != "local" {
		t.Fatal("icon click navigated instead of editing icon")
	}
	ui.Click("笔记本")
	ui.Advance(time.Second)
	if m.DeviceIconChoice("air") != app.DeviceIconLaptop || v.deviceSelected != "local" {
		t.Fatal("icon selection changed navigation or wrong device")
	}
}

func TestAddingDeviceIconStaysInDraftUntilAccepted(t *testing.T) {
	m := deviceUIModel()
	v := New(m, app.NewController(""), "")
	v.devices = true
	v.deviceSelected = "local"
	v.beginWelcomeSSH()
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("添加设备图标"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	ui.Click("平板")
	ui.Advance(time.Second)
	if v.setupFlow().icon != app.DeviceIconTablet || m.DeviceIconChoice("local") != app.DeviceIconAuto {
		t.Fatal("draft icon changed another device")
	}
	v.saveDeviceConnection(app.DeviceConnection{ID: "added", DeviceID: "added-device", Name: "新设备", SSH: transport.SSH{Target: "new-host"}}, false)
	if m.DeviceIconChoice("added") != app.DeviceIconTablet {
		t.Fatal("accepted device lost draft icon")
	}
}

func TestDeviceIconDraftsStayWithTheirConnectionRoute(t *testing.T) {
	v := New(welcomeModel(), nil, "")
	v.beginWelcomeSSH()
	v.setupFlow().icon = app.DeviceIconLaptop
	v.saveWelcomeSSHInput()
	v.beginTailnet()
	v.sshTailnetPeer = "peer"
	v.setupFlow().icon = app.DeviceIconTablet
	v.saveWelcomeSSHInput()
	v.beginWelcomeSSH()
	if v.setupFlow().icon != app.DeviceIconLaptop || v.setupFlow().tailnetSSH["peer"].icon != app.DeviceIconTablet {
		t.Fatal("connection route drafts shared an icon choice")
	}
}

func TestTailnetRegisteredNameRemainsSeparateFromDisplayAlias(t *testing.T) {
	m := deviceUIModel()
	m.LocalDeviceName = "控制机"
	m.Connections[0].Name = "我的 Air"
	m.Connections[0].SSH.TailnetPeer = "peer"
	m.Connections[0].Icon = app.DeviceIconLaptop
	m.Tailnet = tailnet.Snapshot{State: "Running", Hostname: "shioji-node", Network: "example", Address: "100.64.0.2", Peers: []tailnet.Peer{{ID: "peer", Name: "mba-registered", Address: "100.64.0.3", OS: "macOS", Online: true}}}
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "tailnet"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if !ui.HasText("控制机") || !ui.HasText("shioji-node") {
		t.Fatal("local alias and registered name not separated", ui.Texts())
	}
	if err := ui.Click("查看 Tailnet 设备：我的 Air"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("mba-registered") {
		t.Fatal("registered peer name hidden", ui.Texts())
	}
	if err := m.RenameDevice(m.Connections[0].ID, "家中 Air"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Advance(time.Second)
	if m.Tailnet.Peers[0].Name != "mba-registered" || !ui.HasText("家中 Air") || !ui.HasText("mba-registered") {
		t.Fatal("display rename changed registered name")
	}
}

func TestDeviceIconsNativeAppearance(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		m := deviceUIModel()
		m.Connections[0].Icon = app.DeviceIconLaptop
		v := New(m, nil, "")
		v.devices = true
		v.deviceSelected = "air"
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "device-icons-detail-"+theme+".png")
		ui.Click("设备图标")
		ui.Move(5, 100)
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "device-icons-menu-"+theme+".png")
		ui.Click("手机")
		ui.Advance(time.Second)
		ui.SetSize(850, 720)
		ui.Move(5, 100)
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "device-icons-narrow-"+theme+".png")
		m2 := deviceUIModel()
		v2 := New(m2, app.NewController(""), "")
		v2.devices = true
		v2.deviceSelected = "local"
		v2.beginWelcomeSSH()
		adding := n.NewTester(v2.View, 1240, 820)
		adding.SetDark(dark)
		adding.Advance(time.Second)
		adding.Click("添加设备图标")
		adding.Move(5, 100)
		adding.Advance(time.Second)
		saveNativeSkillImage(t, adding, "device-icons-add-"+theme+".png")
	}
}
