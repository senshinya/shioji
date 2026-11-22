package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/tailnet"
	"testing"
	"time"
)

func deviceHealthModel() *app.Model {
	m := layoutModel()
	m.WorkerMinor = 38
	m.ConnectionEpoch = "epoch"
	cpu := 24.
	memory := domain.DeviceMemory{UsedBytes: 12, TotalBytes: 100}
	m.DeviceHealth = &domain.DeviceHealth{ObservedAt: time.Now(), KeepAwake: domain.KeepAwakeState{Available: true, Revision: 4}, Samples: []domain.DeviceLoad{{At: time.Now().Add(-2 * time.Second), CPU: &cpu, Memory: &memory}, {At: time.Now(), CPU: &cpu, Memory: &memory}}}
	return m
}
func TestDevicePopoverShowsLoadWithoutGlobalTailnetAndAlignsValues(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := deviceHealthModel()
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1000, 700)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		if err := ui.Click("当前设备"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
		for _, label := range []string{"CPU 负载折线图", "内存 负载折线图", "保持唤醒开关", "设备详情"} {
			if _, ok := ui.Find(label); !ok {
				t.Fatal("missing device content", label, ui.Texts())
			}
		}
		if ui.HasText("本机 Tailnet") || ui.HasText("内嵌节点") {
			t.Fatal("global network leaked into current device")
		}
		if _, ok := ui.Find("设备连接值"); ok {
			t.Fatal("local device repeats its connection type")
		}
		if ui.HasText("已连接") || ui.HasText("未连接") {
			t.Fatal("popover duplicates toolbar connection state")
		}
		a, _ := ui.Find("CPU 当前负载")
		b, _ := ui.Find("内存 当前负载")
		if math.Abs(float64(a.Y-b.Y)) > 1 || math.Abs(float64(a.H-b.H)) > 1 {
			t.Fatal("load values have different baselines", a, b)
		}
		panel, _ := ui.Find("当前设备状态")
		sw, _ := ui.Find("保持唤醒开关")
		details, _ := ui.Find("设备详情")
		if gap := details.Y - sw.Y - sw.H; gap > 1 {
			t.Fatal("power and details have an unnecessary group gap", gap)
		}
		if math.Abs(float64(details.X+details.W-6-(sw.X+sw.W))) > 1 {
			t.Fatal("details arrow and power switch have different trailing edges", details, sw)
		}
		if panel.Y+panel.H > 700 || math.Abs(float64(sw.X+sw.W-(panel.X+panel.W-10))) > 1 || details.Y <= sw.Y {
			t.Fatal("panel overflow or trailing controls misaligned", panel, sw, details)
		}
	}
}
func TestKeepAwakeWaitsForReceiptAndDoesNotReplayOnFailure(t *testing.T) {
	m := deviceHealthModel()
	controller := app.NewController("")
	v := New(m, controller, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("保持设备唤醒"); err != nil {
		t.Fatal(err)
	}
	var change *app.Job
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "device.keepAwake" {
			change = &job
		}
	}
	if change == nil {
		t.Fatal("no device power operation")
	}
	in, ok := change.Input.(domain.KeepAwakeInput)
	if !ok || !in.Enabled || in.Revision != 4 || change.ConnectionID != "local" || change.ConnectionEpoch != "epoch" {
		t.Fatal("wrong device mutation", change)
	}
	if m.DeviceHealth.KeepAwake.Enabled {
		t.Fatal("optimistically claimed assertion enabled")
	}
	// The receipt is authoritative and updates both quick control and panel.
	result := *m.DeviceHealth
	result.KeepAwake.Enabled = true
	result.KeepAwake.Revision = 5
	b, _ := json.Marshal(result)
	change.Apply(m, b, nil)
	ui.Frame()
	if !m.DeviceHealth.KeepAwake.Enabled {
		t.Fatal("receipt not applied")
	}
	if err := ui.Click("保持设备唤醒"); err != nil {
		t.Fatal(err)
	}
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "device.keepAwake" {
			change = &job
		}
	}
	change.Apply(m, nil, contextDeadlineError{})
	ui.Frame()
	if len(controller.Jobs) != 0 {
		t.Fatal("unknown command was replayed")
	}
}

type contextDeadlineError struct{}

func (contextDeadlineError) Error() string { return "deadline exceeded" }
func TestLoadGraphBreaksAtUnknownAndDoesNotStretchHistory(t *testing.T) {
	now := time.Now()
	zero, full := 0., 100.
	samples := []domain.DeviceLoad{{At: now.Add(-60 * time.Second), CPU: &zero}, {At: now.Add(-58 * time.Second), CPU: nil}, {At: now.Add(-2 * time.Second), CPU: &full}, {At: now, CPU: &zero}}
	lines := app.DeviceLoadSegments(samples, now, false)
	if len(lines) != 2 || len(lines[0]) != 1 || len(lines[1]) != 2 {
		t.Fatal("missing sample connected across a gap", lines)
	}
	if lines[0][0].X != 0 || lines[0][0].Y != 1 || math.Abs(float64(lines[1][0].X-58./60)) > .001 || lines[1][0].Y != 0 || lines[1][1].X != 1 {
		t.Fatal("graph changed time or percentage scale", lines)
	}
}

func TestKeepAwakeTargetsRemoteWorkspaceAndDropsLateReceipt(t *testing.T) {
	m := deviceUIModel()
	m.SessionID = ""
	m.WorkspaceID = "wb"
	m.ApplyDeviceUpdate("air", func(p *app.Model) {
		p.WorkerMinor = 38
		p.DeviceHealth = &domain.DeviceHealth{DeviceID: "B", ObservedAt: time.Now(), KeepAwake: domain.KeepAwakeState{Available: true, Revision: 7}}
	})
	controller := app.NewController("")
	v := New(m, controller, "")
	ui := n.NewTester(v.View, 1000, 700)
	if err := ui.Click("保持设备唤醒"); err != nil {
		t.Fatal(err)
	}
	var change app.Job
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "device.keepAwake" {
			change = job
		}
	}
	if change.ConnectionID != "air" || change.ConnectionEpoch != "b" {
		t.Fatal("remote switch targeted controlling Mac", change)
	}
	m.DeviceStates["air"].ConnectionEpoch = "replacement"
	report := domain.DeviceHealth{DeviceID: "B", ObservedAt: time.Now(), KeepAwake: domain.KeepAwakeState{Available: true, Enabled: true, Revision: 8}}
	body, _ := json.Marshal(report)
	change.Apply(m, body, nil)
	if m.DeviceStates["air"].Health.KeepAwake.Enabled {
		t.Fatal("old connection receipt changed replacement state")
	}
}

func TestStaleOrDisconnectedHealthNeverLetsQuickControlMutate(t *testing.T) {
	for _, mode := range []string{"offline", "stale", "older-worker"} {
		m := deviceHealthModel()
		switch mode {
		case "offline":
			m.Connected = false
		case "stale":
			m.DeviceHealth.ObservedAt = time.Now().Add(-20 * time.Second)
		case "older-worker":
			m.WorkerMinor = 37
		}
		controller := app.NewController("")
		v := New(m, controller, "")
		ui := n.NewTester(v.View, 1000, 700)
		ui.Click("保持设备唤醒")
		for len(controller.Jobs) > 0 {
			if job := <-controller.Jobs; job.Method == "device.keepAwake" {
				t.Fatal("unknown state accepted a power mutation", mode)
			}
		}
		ui.Click("当前设备")
		ui.Advance(time.Second)
		if ui.HasText("24%") || ui.HasText("12%") {
			t.Fatal("old load shown as current", mode)
		}
	}
}

func TestRemoteDeviceRowsShareOneVerticalRhythm(t *testing.T) {
	m := deviceUIModel()
	m.SessionID = ""
	m.WorkspaceID = "wb"
	m.RefreshDevices()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1000, 700)
	ui.Advance(time.Second)
	ui.Click("当前设备")
	ui.Advance(time.Second)
	connection, _ := ui.Find("设备连接值")
	latency, _ := ui.Find("设备往返延迟值")
	power, _ := ui.Find("保持唤醒开关")
	details, _ := ui.Find("设备详情")
	graph, _ := ui.Find("CPU 负载折线图")
	center := func(r n.Rect) float32 { return r.Y + r.H/2 }
	if connection.Y < graph.Y+graph.H {
		t.Fatal("connection details precede the charts")
	}
	if a, b := center(latency)-center(connection), center(details)-center(power); math.Abs(float64(a-b)) > 1 {
		t.Fatal("metadata and action rows use different vertical rhythm", a, b)
	}
	if a, b := center(power)-center(latency), center(details)-center(power); math.Abs(float64(a-b)) > 1 {
		t.Fatal("separate groups insert an extra metadata/action gap", a, b)
	}
}

func TestDeviceOverviewShowsTailnetLatencyApartFromItsPath(t *testing.T) {
	m := deviceUIModel()
	m.SessionID = ""
	m.WorkspaceID = "wb"
	m.Connections[0].SSH.TailnetPeer = "peer"
	m.Connections[0].Routes = nil
	m.Connections[0].ActiveRouteID = ""
	m.Tailnet = tailnet.Snapshot{State: "Running", Peers: []tailnet.Peer{{
		ID: "peer", Online: true, Measurement: &tailnet.Measurement{
			Path: "derp", Region: "tpe", RegionName: "Taipei", Milliseconds: 32, ObservedAt: time.Now(),
		},
	}}}
	m.RefreshDevices()
	ui := n.NewTester(New(m, nil, "").View, 1000, 700)
	ui.Advance(time.Second)
	if err := ui.Click("当前设备"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if _, ok := ui.Find("设备连接方式值"); !ok {
		t.Fatal("missing separate Tailnet connection mode row")
	}
	if _, ok := ui.Find("设备往返延迟值"); ok {
		t.Fatal("Tailnet latency appears in a second row")
	}
}
