package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestDeviceManagementUsesContextualSidebarAndIndependentTabs(t *testing.T) {
	m := deviceUIModel()
	m.Drafts["home"] = "keep draft"
	v := New(m, app.NewController(""), "")
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("返回会话"); !ok {
		t.Fatal("missing contextual navigation")
	}
	if _, ok := ui.Find("设备设置"); !ok {
		t.Fatal("missing settings tab")
	}
	if _, ok := ui.Find("添加 Agent"); ok {
		t.Fatal("redundant add Agent")
	}
	settings, _ := ui.Find("设备设置")
	agents, _ := ui.Find("Agent")
	_, service := ui.Find("执行服务")
	if settings.X >= agents.X || v.deviceTab != "settings" || !service {
		t.Fatal("device settings must be the first and default tab", settings, agents, v.deviceTab, ui.Texts())
	}
	ui.Click("Agent")
	ui.Advance(time.Second)
	if _, ok := ui.Find("执行服务"); ok {
		t.Fatal("service mixed into agents tab", ui.Texts())
	}
	ui.Click("设备设置")
	ui.Frame()
	if _, ok := ui.Find("执行服务"); !ok {
		t.Fatal("settings tab missing services")
	}
	ui.Click("返回会话")
	ui.Frame()
	if v.devices || m.Drafts["home"] != "keep draft" {
		t.Fatal("navigation lost draft")
	}
}

func TestDeviceSidebarDestinationReopensSettings(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	for i := 0; i < 2; i++ {
		if err := ui.Click("设备"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
		if v.deviceTab != "settings" || !ui.HasText("执行服务") {
			t.Fatal("main sidebar device destination did not open settings", ui.Texts())
		}
		if err := ui.Click("Agent"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
		if v.deviceTab != "agents" {
			t.Fatal("fixture did not enter Agent", v.deviceTab)
		}
		if err := ui.Click("返回会话"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
	}
}

func TestDeviceAgentConfigurationNeverAutomaticallyExpandsOnTabEntry(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 16
	state.Readiness.Harnesses = []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}}
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if _, ok := ui.Find("Custom Agent 可执行文件"); ok {
		t.Fatal("configuration automatically expanded on entry")
	}
	ui.Click("设备设置")
	ui.Click("Agent")
	ui.Advance(time.Second)
	if _, ok := ui.Find("Custom Agent 可执行文件"); ok {
		t.Fatal("configuration automatically expanded on tab switch")
	}
	ui.Click("配置 Custom Agent 路径")
	ui.Advance(time.Second)
	if _, ok := ui.Find("Custom Agent 可执行文件"); !ok {
		t.Fatal("manual disclosure unavailable")
	}
}

func TestAgentPageReturnPreservesUnsavedConfiguration(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = 22
	state.Readiness.Harnesses = setupCatalog()
	c := app.NewController("")
	v := New(m, c, "")
	v.devices = true
	v.deviceSelected = "local"
	v.deviceTab = "agents"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("配置 Custom Agent 路径"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	ui.Click("Custom Agent 可执行文件")
	ui.Type("/tmp/edited-agent")
	ui.Click("返回 Agent 列表")
	ui.Advance(time.Second)
	ui.Click("设备设置")
	ui.Advance(time.Second)
	ui.Click("Agent")
	ui.Advance(time.Second)
	ui.Click("配置 Custom Agent 路径")
	ui.Advance(time.Second)
	if !ui.HasText("Custom Agent 可执行文件") {
		t.Fatal("tab switch discarded the open configuration")
	}
	if err := ui.Click("保存 Custom Agent 路径"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job := welcomeJob(t, c, "runtime.agents")
	input := job.Input.(protocol.DeviceAgents)
	if len(input.Agents) != 1 || input.Agents[0].Executable != "/tmp/edited-agent" {
		t.Fatal("tab switch lost unsaved draft", input)
	}
}
