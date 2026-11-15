package ui

import (
	"fmt"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestDeviceTabsNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_DEVICE_TABS_RECORDING")
	if dir == "" {
		t.Skip("opt-in native device tab recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	m := deviceUIModel()
	for _, state := range m.DeviceStates {
		state.ConnectionEpoch = ""
		state.WorkerMinor = 24
		state.Readiness.Harnesses = []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}}
	}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected = true, "local"
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	for frame := 0; frame < 420; frame++ {
		label := ""
		switch frame {
		case 30, 210, 300:
			label = "Agent"
		case 90:
			label = "设备设置"
		case 150:
			label = "选择设备：MacBook Air"
		case 270:
			label = "选择设备：" + m.LocalName()
		case 330:
			label = "返回会话"
		case 360:
			label = "设备"
		}
		if label != "" {
			if err := ui.Click(label); err != nil {
				t.Fatal(err)
			}
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
}
