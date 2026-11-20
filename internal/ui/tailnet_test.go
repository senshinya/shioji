package ui

import (
	n "github.com/egoist/mygo/ui"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"testing"
	"time"
)

func TestTailnetWelcomeSelectionHandsOffToExistingSSH(t *testing.T) {
	m := welcomeModel()
	c := app.NewController("")
	node := tailnet.New(filepath.Join(t.TempDir(), "node"), nil)
	defer node.Close()
	c.Tailnet = node
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	if err := ui.Click("从 Tailnet 添加"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("连接 Tailnet") {
		t.Fatal(ui.Texts())
	}
	ui.Click("加入网络")
	join := welcomeJob(t, c, "$tailnet").Input.(tailnet.Action)
	m.Tailnet = tailnet.Snapshot{State: "Running", OperationID: join.ID, Network: "example", Peers: []tailnet.Peer{{ID: "stable-peer", Name: "MacBook Air", Address: "100.64.0.1", OS: "macOS", Online: true}}}
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("选择 Tailnet 设备：MacBook Air"); err != nil {
		t.Fatal(err)
	}
	ui.Click("继续")
	ui.Advance(time.Second)
	if v.welcome.step != welcomeSSH {
		t.Fatal("independent Tailnet setup flow", v.welcome.step)
	}
	if _, ok := ui.Find("SSH 地址"); !ok {
		t.Fatal("existing SSH form missing")
	}
	v.checkSSHDevice()
	job := welcomeJob(t, c, "$device.check")
	in := job.Input.(app.CheckSSHDevice)
	if in.SSH.TailnetPeer != "stable-peer" || in.SSH.Target != "100.64.0.1" {
		t.Fatal(in.SSH)
	}
}
func TestTailnetNetworkManagementHasNoAddEntry(t *testing.T) {
	v := New(deviceUIModel(), app.NewController(""), "")
	v.devices = true
	v.deviceSelected = "tailnet"
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	if !ui.HasText("连接 Tailnet") {
		t.Fatal(ui.Texts())
	}
	if _, ok := ui.Find("连接设备"); ok {
		t.Fatal("network management adds devices")
	}
}

func TestTailnetNetworkSwitchPausesOnlyEmbeddedNode(t *testing.T) {
	m := deviceUIModel()
	m.Tailnet = tailnet.Snapshot{State: "Running", Hostname: "shioji-mac", Network: "example", Address: "100.64.0.2"}
	c := app.NewController("")
	v := New(m, c, "")
	v.devices = true
	v.deviceSelected = "tailnet"
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	r, ok := ui.Find("Tailnet 连接开关")
	if !ok || r.W != 40 {
		t.Fatal("network switch missing", r, ok)
	}
	if err := ui.Click("Tailnet 连接开关"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, c, "$tailnet")
	if a := job.Input.(tailnet.Action); a.Kind != "pause" {
		t.Fatal("wrong network action", a)
	}
}

func TestTailnetStepKeepsFooterOutsideHorizontalMotion(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	v.beginTailnet()
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	v.networkUI().joining = true
	m.Tailnet = tailnet.Snapshot{State: "Running", Network: "example"}
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	r, ok := ui.Find("继续")
	if !ok || r.X+r.W > 921 || r.X+r.W < 919 {
		t.Fatal("shared actions slide with content", r)
	}
	ui.Advance(time.Second)
	end, _ := ui.Find("继续")
	if end.X+end.W != 920 {
		t.Fatal(end)
	}
}

func TestChangingNetworkCannotAcceptTheOldRunningSnapshot(t *testing.T) {
	m := welcomeModel()
	m.Tailnet = tailnet.Snapshot{State: "Running", Network: "old"}
	c := app.NewController("")
	v := New(m, c, "")
	v.beginTailnet()
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.Click("更换网络")
	ui.Advance(time.Second)
	ui.Click("加入网络")
	ui.Frame()
	if v.welcome.step != welcomeTailnetJoin || !v.networkUI().joining {
		t.Fatal("old network completed new enrollment")
	}
	job := welcomeJob(t, c, "$tailnet")
	a := job.Input.(tailnet.Action)
	if a.ID == "" {
		t.Fatal("enrollment has no identity")
	}
	m.Tailnet = tailnet.Snapshot{State: "Running", OperationID: a.ID, Network: "new"}
	ui.Frame()
	if v.welcome.step != welcomeTailnetPeers {
		t.Fatal("matching enrollment did not complete")
	}
}

func TestReopeningTailnetSetupKeepsPendingEnrollment(t *testing.T) {
	m := welcomeModel()
	m.Tailnet = tailnet.Snapshot{State: "NeedsLogin", Authorizing: true, OperationID: "current-join", AuthURL: "https://login.tailscale.com/a/invalid-fixture"}
	v := New(m, app.NewController(""), "")
	v.beginTailnet()
	if !v.networkUI().joining || v.networkUI().joinID != "current-join" || v.welcome.step != welcomeTailnetJoin {
		t.Fatal("pending authorization lost")
	}
}

func TestTailnetNeverOpensAuthorizationFromPreviousAttempt(t *testing.T) {
	m := welcomeModel()
	m.Tailnet = tailnet.Snapshot{State: "NeedsLogin", Authorizing: true, OperationID: "old", AuthURL: "https://login.tailscale.com/a/invalid-old"}
	v := New(m, app.NewController(""), "")
	u := v.networkUI()
	u.joining = true
	u.autoOpen = true
	u.joinID = "new"
	opened := false
	before := openAuthorizationURL
	openAuthorizationURL = func(string) error { opened = true; return nil }
	defer func() { openAuthorizationURL = before }()
	v.syncTailnet()
	if opened {
		t.Fatal("opened canceled/stale authorization URL")
	}
}

func TestAddingRouteRetainsPendingOperationQueries(t *testing.T) {
	m := deviceUIModel()
	m.PendingDevices["operation"] = "air"
	c := app.NewController("")
	v := New(m, c, "")
	v.sshTarget = "user@other"
	v.sshCheck = sshEditing
	v.welcome = &welcomeFlow{key: "", step: welcomeSSH}
	v.finishSSHSetup(app.DeviceSetupCheck{Info: transport.WorkerInfo{DeviceID: "B", Worker: "/worker"}})
	job := welcomeJob(t, c, "$device.configure")
	in := job.Input.(app.ConfigureDevice)
	if len(in.Pending) != 1 || in.Pending[0].ID != "operation" {
		t.Fatal("adding a route lost pending-operation queries", in.Pending)
	}
}
