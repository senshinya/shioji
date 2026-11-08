package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"testing"
	"time"
)

func TestDeviceModelsControlsUpdateMenuWithoutChangingActiveConfiguration(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 16
	state.Readiness = domain.Readiness{Harnesses: []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Models: []domain.Model{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}}, Installation: &domain.RuntimeInstallation{Configurable: true}}}}
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("Custom Agent 可执行文件"); !ok {
		ui.Click("配置 Custom Agent 路径")
	}
	ui.Advance(time.Second)
	if _, ok := ui.Find("在模型菜单显示 Alpha"); !ok {
		t.Fatal("model list missing", ui.Texts())
	}
	ui.Click("在模型菜单显示 Alpha")
	ui.Click("收藏 Beta")
	ui.Frame()
	models := m.ModelsForMenu("A", "custom", state.Readiness.Harnesses[0].Models)
	if len(models) != 1 || models[0].ID != "b" {
		t.Fatal("controls not connected", models)
	}
}

func TestKeyboardModelOrderingRetainsUnavailablePreferences(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 16
	state.Readiness = domain.Readiness{Harnesses: []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Models: []domain.Model{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}}, Installation: &domain.RuntimeInstallation{Configurable: true}}}}
	m.SetModelPreferences("A", "custom", app.ModelMenuPreferences{Order: []string{"a", "missing", "b"}})
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	ui.Click("配置 Custom Agent 路径")
	ui.Advance(time.Second)
	ui.Click("调整 Beta 顺序")
	ui.Key(0, n.KeyUp)
	if !slices.Contains(m.ModelPreference("A", "custom").Order, "missing") {
		t.Fatal("keyboard sorting lost saved preference")
	}
}
