package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestConfigurationRowsShareRhythmAndSwitchAlignment(t *testing.T) {
	for _, dark := range []bool{false, true} {
		ui := n.NewTester(func(c *n.Context) {
			value := true
			n.Column(c).Width(560).Children(func() {
				settingsRow(c, c.Theme(), "系统通知", func() { configurationSwitch(c, &value).Label("设置开关") })
				deviceConfigRow(c, "登录后启动服务", func() {
					n.Row(c).FillWidth().Justify(n.End).Children(func() { configurationSwitch(c, &value).Label("设备开关") })
				})
				setupRow(c, "前台也通知", func() {
					n.Row(c).FillWidth().Justify(n.End).Children(func() { configurationSwitch(c, &value).Label("配置开关") })
				})
			})
		}, 560, 160)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		var prev n.Rect
		for i, label := range []string{"设置开关", "设备开关", "配置开关"} {
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("missing switch", label)
			}
			if i > 0 && (math.Abs(float64(r.Y-prev.Y-40)) > 1 || math.Abs(float64(r.X+r.W-prev.X-prev.W)) > 1) {
				t.Fatal("forms differ in rhythm or right edge", dark, label, prev, r)
			}
			prev = r
		}
	}
}

func TestNarrowDeviceHeaderAndBodyShareLeftEdge(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	v.devices = true
	v.deviceSelected = "local"
	v.deviceTab = "settings"
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)
	title, _ := ui.Find(v.Model.LocalName())
	icon, iconOK := ui.Find("设备图标")
	body, ok := ui.Find("执行服务")
	if !ok || !iconOK || math.Abs(float64(icon.X+8-body.X)) > 1 || title.X < icon.X+icon.W {
		t.Fatal("narrow header group and body are not aligned", icon, title, body)
	}
}

func TestNarrowDeviceBackActionClearsNativeWindowControls(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)
	back, ok := ui.Find("返回设备列表")
	if !ok || back.X < 80 && back.Y < 32 {
		t.Fatal("compact toolbar overlaps native traffic lights", back)
	}
}

func TestWideDevicePagesShareHeaderAndBodyEdges(t *testing.T) {
	for _, width := range []float32{1240, 1800} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%.0f-dark-%v", width, dark), func(t *testing.T) {
				m := deviceUIModel()
				m.DeviceStates["air"].ConnectionEpoch = ""
				m.DeviceStates["air"].WorkerMinor = 22
				m.DeviceStates["air"].Readiness.Harnesses = []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}}
				v := New(m, nil, "")
				v.devices = true
				v.deviceSelected = "air"
				ui := n.NewTester(v.View, int(width), 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				arrow, ok := ui.Find("打开 Custom Agent 配置")
				if !ok || math.Abs(float64(width-configurationInset-8-arrow.X-arrow.W)) > 1 {
					t.Fatal("agent list does not reach header right edge", width, arrow)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("device-width-agents-%.0f-dark-%v.png", width, dark))
				ui.Click("设备设置")
				ui.Advance(time.Second)
				heading, _ := ui.Find("执行服务")
				if math.Abs(float64(width-configurationInset-heading.X-heading.W)) > 1 {
					t.Fatal("device settings section does not share page edges", width, heading)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("device-width-settings-%.0f-dark-%v.png", width, dark))
				ui.Click("Agent")
				ui.Advance(time.Second)
				ui.Click("打开 Custom Agent 配置")
				ui.Advance(time.Second)
				field, ok := ui.Find("Custom Agent 可执行文件")
				if !ok || math.Abs(float64(width-configurationInset-field.X-field.W)) > 1 {
					t.Fatal("agent configuration does not share header right edge", width, field)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("device-width-config-%.0f-dark-%v.png", width, dark))
			})
		}
	}
}

func TestWideWorkspaceSettingsUsesAvailableWidth(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	v.editWorkspace(v.Model.Snapshot.Workspaces[0])
	ui := n.NewTester(v.View, 1800, 820)
	ui.Advance(time.Second)
	field, ok := ui.Find("工作区名称")
	if !ok || math.Abs(float64(1800-configurationInset-field.X-field.W)) > 1 {
		t.Fatal("workspace settings retains an asymmetric capped column", field)
	}
	saveNativeSkillImage(t, ui, "workspace-width-1800.png")
}
