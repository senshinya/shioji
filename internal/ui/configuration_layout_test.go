package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
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
	body, ok := ui.Find("执行服务")
	if !ok || math.Abs(float64(title.X-body.X)) > 1 {
		t.Fatal("narrow header and body are not aligned", title, body)
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
