package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"testing"
	"time"
)

func TestDeviceModelsFullListUsesThePageScroll(t *testing.T) {
	for _, setup := range []bool{false, true} {
		v := New(deviceUIModel(), nil, "")
		info := domain.HarnessInfo{ID: "custom", State: domain.RuntimeReady}
		for i := 1; i <= 12; i++ {
			info.Models = append(info.Models, domain.Model{ID: fmt.Sprint(i), Name: fmt.Sprintf("Model %02d", i)})
		}
		ui := n.NewTester(func(c *n.Context) {
			n.Scroll(c).Fill().Label("配置页面滚动区").Children(func() {
				if setup {
					v.deviceModels(c, c.Theme(), "local", info, "A")
				} else {
					v.deviceModels(c, c.Theme(), "local", info)
				}
				n.Text(c, "列表后续设置").FixedLineHeight(20)
			})
		}, 600, 360)
		ui.SetDark(false)
		ui.Advance(time.Second)
		area := "模型列表区域"
		if setup {
			area = "模型列表滚动区"
		}
		viewport, _ := ui.Find(area)
		// Find reports the visible, clipped box rather than the full layout box.
		want := float32(360 - configurationSectionHeader)
		if setup {
			want = configurationListHeight
		}
		saveNativeSkillImage(t, ui, fmt.Sprintf("models-setup-%v.png", setup))
		if math.Abs(float64(viewport.H-want)) > 1 {
			t.Fatalf("setup=%v: model height %v, want %v", setup, viewport.H, want)
		}
		if setup {
			continue
		}
		eighth, _ := ui.Find("模型：Model 08")
		if eighth.H != configurationRowHeight {
			t.Fatal("full device page does not expose all available model rows", eighth)
		}
		ui.Scroll(300, 160, 0, 600)
		ui.Frame()
		ui.Advance(time.Second)
		afterFirst, _ := ui.Find("模型：Model 01")
		last, ok := ui.Find("模型：Model 12")
		heading, _ := ui.Find("模型")
		footer, _ := ui.Find("列表后续设置")
		page, _ := ui.Find("配置页面滚动区")
		if !ok || last.H != configurationRowHeight || last.Y < page.Y || last.Y+last.H > page.Y+page.H+1 || heading.H > 0 || afterFirst.H > 0 || footer.H != 20 {
			t.Fatal("long device model list does not scroll with its page", heading, afterFirst, last, footer, page)
		}
	}
}

func TestDeviceModelsNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_DEVICE_MODELS_RECORDING")
	if dir == "" {
		t.Skip("opt-in native full model list recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch, state.WorkerMinor = "", 24
	info := setupCatalog()[0]
	info.Models = nil
	for i := 1; i <= 20; i++ {
		info.Models = append(info.Models, domain.Model{ID: fmt.Sprint(i), Name: fmt.Sprintf("Model %02d", i)})
	}
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	if err := ui.Click("配置 Custom Agent 路径"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("模型"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	viewport, _ := ui.Find("模型列表区域")
	eighth, _ := ui.Find("模型：Model 08")
	if viewport.H <= configurationListHeight || eighth.H != configurationRowHeight {
		t.Fatal("real device page still constrains the model list", viewport)
	}
	for frame := 0; frame < 240; frame++ {
		switch frame {
		case 60, 180:
			ui.Scroll(850, 400, 0, 600)
		case 120:
			ui.Scroll(850, 400, 0, -600)
		}
		ui.Advance(time.Second / 30)
		if frame == 90 {
			last, _ := ui.Find("模型：Model 20")
			if last.H != configurationRowHeight {
				t.Fatal("page scrolling cannot reach the final model", last)
			}
		}
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
}

func TestDeviceModelsControlsUpdateMenuWithoutChangingActiveConfiguration(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 16
	state.Readiness = domain.Readiness{Harnesses: []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Models: []domain.Model{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}}, Installation: &domain.RuntimeInstallation{Configurable: true}}}}
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "local"
	v.deviceTab = "agents"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("Custom Agent 可执行文件"); !ok {
		ui.Click("配置 Custom Agent 路径")
	}
	ui.Advance(time.Second)
	ui.Click("模型")
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
	v.deviceTab = "agents"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	ui.Click("配置 Custom Agent 路径")
	ui.Advance(time.Second)
	ui.Click("模型")
	ui.Advance(time.Second)
	ui.Click("调整 Beta 顺序")
	ui.Key(0, n.KeyUp)
	if !slices.Contains(m.ModelPreference("A", "custom").Order, "missing") {
		t.Fatal("keyboard sorting lost saved preference")
	}
}

func TestPreviousModelsAreReadOnlyAfterRefreshFailure(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	info := domain.HarnessInfo{ID: "custom", State: domain.RuntimeUnavailable, Models: []domain.Model{{ID: "a", Name: "Alpha"}}}
	ui := n.NewTester(func(c *n.Context) { v.deviceModels(c, c.Theme(), "local", info) }, 560, 300)
	ui.Advance(time.Second)
	if !ui.HasText("上次读取") {
		t.Fatal("old catalog presented as current", ui.Texts())
	}
	ui.Click("收藏 Alpha")
	ui.Click("在模型菜单显示 Alpha")
	pref := m.ModelPreference("A", "custom")
	if len(pref.Favorites) != 0 || len(pref.Hidden) != 0 {
		t.Fatal("stale catalog remained editable", pref)
	}
}
