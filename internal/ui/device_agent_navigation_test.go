package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

// A nested disclosure or a replaced detail root would lose the full-page
// transition and its return destination; exercise the actual device browser.
func TestAgentConfigurationReplacesDevicePageAndSlidesBack(t *testing.T) {
	for _, width := range []int{1240, 850} {
		m := deviceUIModel()
		m.DeviceStates["local"].WorkerMinor = 24
		m.DeviceStates["local"].Readiness.Harnesses = []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}}
		v := New(m, nil, "")
		v.devices, v.deviceSelected = true, "local"
		ui := n.NewTester(v.View, width, 820)
		ui.Advance(time.Second)
		list, ok := ui.Find("配置 Custom Agent 路径")
		if !ok {
			t.Fatal("missing Agent entry", ui.Texts())
		}
		if err := ui.Click("配置 Custom Agent 路径"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(60 * time.Millisecond)
		mid, ok := ui.Find("Custom Agent 可执行文件")
		if !ok {
			t.Fatal("missing configuration", ui.Texts())
		}
		ui.Advance(400 * time.Millisecond)
		end, _ := ui.Find("Custom Agent 可执行文件")
		if mid.X <= end.X+1 {
			t.Fatal("configuration did not slide from right", mid, end)
		}
		if ui.HasText("设备设置") {
			t.Fatal("configuration is still nested in device page", ui.Texts())
		}
		label, _ := ui.Find("可执行文件")
		tab, _ := ui.Find("常规")
		if math.Abs(float64(label.X-tab.X-8)) > 1 || math.Abs(float64(label.X-list.X)) > 9 {
			t.Fatal("configuration retains disclosure indentation", list, tab, label)
		}
		if err := ui.Click("返回 Agent 列表"); err != nil {
			t.Fatal("no return destination", err, ui.Texts())
		}
		ui.Advance(60 * time.Millisecond)
		backMid, ok := ui.Find("配置 Custom Agent 路径")
		if !ok {
			t.Fatal("return list missing")
		}
		ui.Advance(400 * time.Millisecond)
		backEnd, _ := ui.Find("配置 Custom Agent 路径")
		if backMid.X >= backEnd.X-1 && backMid.W >= backEnd.W-1 {
			t.Fatal("return did not uncover list from left", backMid, backEnd)
		}
		if !ui.HasText("设备设置") {
			t.Fatal("device page not restored", ui.Texts())
		}
	}
}
