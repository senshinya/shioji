package ui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func checkWindowDrag(t *testing.T, ui *n.Tester, x, y float32, want int) {
	t.Helper()
	before := ui.WindowDragCount()
	ui.Press(x, y)
	ui.Release(x, y)
	if got := ui.WindowDragCount() - before; got != want {
		t.Errorf("window drag requests at (%g, %g) = %d, want %d", x, y, got, want)
	}
}

func TestMainWindowDragFromRightHeader(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	checkWindowDrag(t, ui, 750, 30, 1)
	if err := ui.Click("新建会话"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	checkWindowDrag(t, ui, 750, 30, 1)

	m = deviceUIModel()
	m.DeviceStates["local"].WorkerMinor = 22
	m.DeviceStates["local"].Readiness.Harnesses = setupCatalog()
	v = New(m, nil, "")
	v.devices, v.deviceSelected = true, "local"
	ui = n.NewTester(v.View, 1240, 820)
	saveNativeSkillImage(t, ui, "device-drag.png")
	checkWindowDrag(t, ui, 750, 30, 1)
	for _, label := range []string{"修改设备名称", "取消修改设备名称", "Agent", "配置 Custom Agent 路径"} {
		before := ui.WindowDragCount()
		if err := ui.Click(label); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
		if ui.WindowDragCount() != before {
			t.Errorf("clicking %q started a window drag", label)
		}
	}
	if !ui.HasText("返回 Agent 列表") {
		t.Fatal("Agent configuration did not open")
	}
	checkWindowDrag(t, ui, 750, 30, 1)
	if err := ui.Click("Tailnet"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.deviceSelected != "tailnet" {
		t.Fatal("Tailnet did not open")
	}
	checkWindowDrag(t, ui, 750, 30, 1)

	for _, device := range []string{"local", "tailnet"} {
		t.Run("narrow/"+device, func(t *testing.T) {
			v := New(deviceUIModel(), nil, "")
			v.devices, v.deviceSelected = true, device
			ui := n.NewTester(v.View, 860, 650)
			checkWindowDrag(t, ui, 750, 24, 1)
			if err := ui.Click("返回设备列表"); err != nil {
				t.Fatal(err)
			}
			if !v.deviceList {
				t.Fatal("narrow header back button did not open the device list")
			}
		})
	}
}

func TestSettingsWindowDragFromRightHeader(t *testing.T) {
	s := NewSettings(New(layoutModel(), nil, ""))
	ui := n.NewTester(s.View, 968, 756)
	saveNativeSkillImage(t, ui, "settings-drag.png")
	checkWindowDrag(t, ui, 750, 16, 1)
	checkWindowDrag(t, ui, 750, 40, 1)
	checkWindowDrag(t, ui, 750, 90, 0)

	if err := ui.Click("搜索设置"); err != nil {
		t.Fatal(err)
	}
	ui.Type("主题")
	if !ui.HasText("外观 · 主题") {
		t.Fatal("settings search did not open")
	}
	checkWindowDrag(t, ui, 750, 40, 1)
	if err := ui.Click("外观 · 主题"); err != nil {
		t.Fatal(err)
	}
	if s.query != "" {
		t.Fatal("search result did not return to appearance settings")
	}
	theme, _ := ui.Find("主题")
	ui.Scroll(750, 500, 0, 500)
	ui.Advance(time.Second)
	if after, visible := ui.Find("主题"); visible && after.Y == theme.Y {
		t.Fatal("appearance content did not scroll")
	}
	checkWindowDrag(t, ui, 750, 16, 1)
}
