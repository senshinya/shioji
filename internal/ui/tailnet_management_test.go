package ui

import (
	"math"
	"shioji/internal/app"
	"shioji/internal/tailnet"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func tailnetManagementView() (*View, *n.Tester) {
	m := deviceUIModel()
	m.Tailnet = tailnet.Snapshot{State: "Running", Hostname: "shioji-mac", Network: "example.com", Address: "100.75.136.76", InboundEnabled: true,
		Peers: []tailnet.Peer{{ID: "air", Name: "MacBook Air", Address: "100.88.0.105", OS: "macOS", Online: true, Measurement: &tailnet.Measurement{Milliseconds: 12, ObservedAt: time.Now(), Path: "derp", Region: "nrt", RegionName: "Tokyo"}}}}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected = true, "tailnet"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	return v, ui
}

func TestTailnetManagementCopiesAddressesAndDisclosesReadonlyPeers(t *testing.T) {
	v, ui := tailnetManagementView()
	if err := ui.Click("复制本机 Tailnet 地址"); err != nil {
		t.Fatal(err)
	}
	if ui.Clipboard() != "100.75.136.76" || !ui.HasText("已复制") {
		t.Fatal("missing real copy/feedback", ui.Clipboard(), ui.Texts())
	}
	if ui.HasText("Tokyo") {
		t.Fatal("details initially expanded")
	}
	if err := ui.Click("查看 Tailnet 设备：MacBook Air"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("DERP 中继 · Tokyo (nrt)") {
		t.Fatal("actual path missing", ui.Texts())
	}
	ui.Click("复制 Tailnet 设备地址：MacBook Air")
	if ui.Clipboard() != "100.88.0.105" {
		t.Fatal("copied wrong peer", ui.Clipboard())
	}
	ui.Click("查看 Tailnet 设备：MacBook Air")
	ui.Advance(time.Second)
	if ui.HasText("DERP 中继 · Tokyo (nrt)") || v.welcome != nil || v.sshCheck.open() {
		t.Fatal("readonly peer changed setup or failed to collapse")
	}
}

func TestTailnetManagementLogoutRequiresConfirmation(t *testing.T) {
	v, ui := tailnetManagementView()
	ui.Click("Tailnet 网络操作")
	ui.Advance(time.Second)
	if err := ui.Click("退出网络…"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("退出 Tailnet？") {
		t.Fatal("no logout confirmation", ui.Texts())
	}
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; job.Method == "$tailnet" {
			t.Fatal("logout before confirmation", job.Method)
		}
	}
	ui.Click("取消退出网络")
	ui.Advance(time.Second)
	ui.Click("Tailnet 网络操作")
	ui.Advance(time.Second)
	ui.Click("退出网络…")
	ui.Advance(time.Second)
	ui.Click("确认退出网络")
	if a := welcomeJob(t, v.Controller, "$tailnet").Input.(tailnet.Action); a.Kind != "logout" {
		t.Fatal(a)
	}
}

func TestTailnetManagementRefreshTargetsExistingOwner(t *testing.T) {
	v, ui := tailnetManagementView()
	if err := ui.Click("刷新网络设备"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "$tailnet")
	a := job.Input.(tailnet.Action)
	if a.Kind != "refresh" || a.ID == "" {
		t.Fatal(a)
	}
	// A repeat press before the owner's acknowledgement must not enqueue again.
	ui.Frame()
	ui.Click("刷新网络设备")
	select {
	case job := <-v.Controller.Jobs:
		t.Fatal("duplicate refresh", job.Method)
	default:
	}
	v.Model.Tailnet.OperationID = a.ID
	ui.Frame()
	ui.Click("刷新网络设备")
	if next := welcomeJob(t, v.Controller, "$tailnet").Input.(tailnet.Action); next.Kind != "refresh" || next.ID == a.ID {
		t.Fatal("refresh did not unlock after acknowledgement", next)
	}
}

func TestTailnetManagementUsesSymmetricInsetsAndSharedControlEdge(t *testing.T) {
	for _, width := range []float32{1240, 1600, 850} {
		v, _ := tailnetManagementView()
		ui := n.NewTester(v.View, int(width), 820)
		ui.Advance(time.Second)
		pane, _ := ui.Find("Tailnet 管理内容")
		left := float32(0)
		if width >= 900 {
			left = 232
		}
		if math.Abs(float64(pane.X-left-32)) > 1 || math.Abs(float64(width-pane.X-pane.W-32)) > 1 {
			t.Fatal("asymmetric main content", width, pane)
		}
		copy, _ := ui.Find("复制本机 Tailnet 地址")
		network, _ := ui.Find("Tailnet 连接开关")
		inbound, _ := ui.Find("入站转发开关")
		if math.Abs(float64(copy.X+copy.W-network.X-network.W)) > 1 || math.Abs(float64(copy.X+copy.W-inbound.X-inbound.W)) > 1 {
			t.Fatal("right edges differ", width, copy, network, inbound)
		}
		v.Model.Tailnet = tailnet.Snapshot{State: "NotStarted"}
		ui.Frame()
		ui.Advance(time.Second)
		form, ok := ui.Find("Tailnet 连接表单")
		if !ok || math.Abs(float64(form.W-560)) > 1 || math.Abs(float64(form.X+form.W/2-(left+width)/2)) > 1 {
			t.Fatal("join form off center", width, form)
		}
	}
}

func TestTailnetManagementPausedMeasurementsAreNotCurrent(t *testing.T) {
	v, ui := tailnetManagementView()
	ui.Click("查看 Tailnet 设备：MacBook Air")
	ui.Advance(time.Second)
	v.Model.Tailnet.State = "Offline"
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("上次 12.0 ms") || !ui.HasText("上次 · DERP 中继 · Tokyo (nrt)") || !ui.HasText("状态未知") {
		t.Fatal("offline observations look current", ui.Texts())
	}
}

func TestTailnetManagementNativeAppearance(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		v, _ := tailnetManagementView()
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "network-management-"+theme+".png")
		ui.Click("查看 Tailnet 设备：MacBook Air")
		ui.Move(700, 30)
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "network-peer-detail-"+theme+".png")
		v.Model.Tailnet = tailnet.Snapshot{State: "NotStarted"}
		ui.Frame()
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "network-join-"+theme+".png")
		v.networkUI().joining, v.networkUI().joinID = true, "preview-auth"
		v.Model.Tailnet = tailnet.Snapshot{State: "NeedsLogin", Authorizing: true, OperationID: "preview-auth", AuthURL: "https://login.tailscale.com/a/invalid-preview"}
		ui.Frame()
		ui.Advance(time.Second)
		saveNativeSkillImage(t, ui, "network-authorization-"+theme+".png")
	}
}

func TestTailnetManagementControlCentersFollowTextInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		_, ui := tailnetManagementView()
		ui.SetDark(dark)
		ui.Advance(time.Second)
		for _, pair := range [][2]string{{"已连接", "Tailnet 连接开关"}, {"100.75.136.76", "复制本机 Tailnet 地址"}, {"网络设备", "刷新网络设备"}} {
			text, ok := ui.Find(pair[0])
			if !ok {
				t.Fatal(pair[0])
			}
			control, ok := ui.Find(pair[1])
			if !ok {
				t.Fatal(pair[1])
			}
			lo, hi := 10000, -1
			im := ui.Image()
			for y := int(text.Y) - 3; y < int(text.Y+text.H)+3; y++ {
				for x := int(text.X); x < int(text.X+text.W); x++ {
					pixel := im.RGBAAt(x, y)
					ink := pixel.R < 190
					if dark {
						ink = pixel.R > 100
					}
					if ink {
						lo = min(lo, y)
						hi = max(hi, y)
					}
				}
			}
			if hi < 0 || math.Abs(float64(lo+hi)/2-float64(control.Y+control.H/2)) > 1 {
				t.Errorf("dark=%v %s ink center=%v control=%v", dark, pair[0], float64(lo+hi)/2, control)
			}
		}
	}
}

func TestTailnetRefreshAcknowledgementStaysSettledAfterOtherActions(t *testing.T) {
	v, ui := tailnetManagementView()
	ui.Click("刷新网络设备")
	a := welcomeJob(t, v.Controller, "$tailnet").Input.(tailnet.Action)
	v.Model.Tailnet.OperationID = a.ID
	ui.Frame()
	ui.Click("入站转发开关")
	other := welcomeJob(t, v.Controller, "$tailnet").Input.(tailnet.Action)
	v.Model.Tailnet.OperationID = other.ID
	ui.Frame()
	ui.Click("刷新网络设备")
	if len(v.Controller.Jobs) == 0 {
		t.Fatal("completed refresh became pending after unrelated operation")
	}
	if next := welcomeJob(t, v.Controller, "$tailnet").Input.(tailnet.Action); next.Kind != "refresh" {
		t.Fatal(next)
	}
}

func TestTailnetNarrowHeaderClearsNativeWindowControls(t *testing.T) {
	v, _ := tailnetManagementView()
	ui := n.NewTester(v.View, 850, 720)
	ui.Advance(time.Second)
	back, _ := ui.Find("返回设备列表")
	title, _ := ui.Find("Tailnet")
	if back.X < 96 || title.Y < 36 {
		t.Fatal("header overlaps native traffic lights", back, title)
	}
	saveNativeSkillImage(t, ui, "network-narrow-native-light.png")
}

func TestTailnetPeerDetailsShareEvenInkSpacingAndCopyCenter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, _ := tailnetManagementView()
		ui := n.NewTester(func(c *n.Context) {
			n.Column(c).Fill().Padding(32).Children(func() { v.tailnetNetworkPeers(c, c.Theme(), v.Model.Tailnet) })
		}, 1240, 820)
		ui.SetDark(dark)
		ui.Click("查看 Tailnet 设备：MacBook Air")
		ui.Move(700, 30)
		ui.Advance(time.Second)
		inkCenter := func(label string) float64 {
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal(label)
			}
			lo, hi := 10000, -1
			im := ui.Image()
			for y := int(r.Y) - 3; y < int(r.Y+r.H)+3; y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					p := im.RGBAAt(x, y)
					hit := p.R < 190
					if dark {
						hit = p.R > 100
					}
					if hit {
						lo = min(lo, y)
						hi = max(hi, y)
					}
				}
			}
			if hi < 0 {
				t.Fatal("no text ink", label)
			}
			return float64(lo+hi) / 2
		}
		path, latency, system := inkCenter("连接路径"), inkCenter("往返延迟"), inkCenter("系统")
		copy, ok := ui.Find("复制 Tailnet 设备地址：MacBook Air")
		if !ok {
			t.Fatal("copy missing")
		}
		address := inkCenter("Tailnet 地址")
		first := float64(copy.Y + copy.H/2)
		if math.Abs(address-first) > 1 || math.Abs(path-address-28) > 1 || math.Abs(latency-path-28) > 1 || math.Abs(system-latency-28) > 1 {
			t.Errorf("dark=%v uneven detail centers: copy=%v address=%v path=%v latency=%v system=%v", dark, first, address, path, latency, system)
		}
	}
}
