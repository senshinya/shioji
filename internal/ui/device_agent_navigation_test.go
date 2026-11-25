package ui

import (
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
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
		v.deviceTab = "agents"
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

func TestDeviceSidebarSelectionReturnsToSettingsFromAgentConfiguration(t *testing.T) {
	m := deviceUIModel()
	m.DeviceStates["local"].ConnectionEpoch = ""
	m.DeviceStates["local"].WorkerMinor = 24
	m.DeviceStates["local"].Readiness.Harnesses = []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected = true, "local"
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("Agent")
	ui.Advance(time.Second)
	if err := ui.Click("配置 Custom Agent 路径"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	ui.Click("Custom Agent 可执行文件")
	ui.Type("/tmp/kept-agent-draft")
	if err := ui.Click("选择设备：" + m.LocalName()); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("执行服务") || ui.HasText("Custom Agent 可执行文件") {
		t.Fatal("reselecting a device kept the Agent configuration page", ui.Texts())
	}
	ui.Click("Agent")
	ui.Advance(time.Second)
	ui.Click("配置 Custom Agent 路径")
	ui.Advance(time.Second)
	if err := ui.Click("保存 Custom Agent 路径"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "runtime.agents")
	input := job.Input.(protocol.DeviceAgents)
	if len(input.Agents) != 1 || input.Agents[0].Executable != "/tmp/kept-agent-draft" {
		t.Fatal("sidebar navigation discarded the unsaved Agent draft", input)
	}
	if err := ui.Click("选择设备：MacBook Air"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("执行服务") || v.deviceSelected != "air" {
		t.Fatal("selecting another device kept the Agent tab", v.deviceSelected, ui.Texts())
	}
}

func TestCreateDeviceProfileUsesSelectedAgentAndLoginMode(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = protocol.AgentProfileMinor
	state.Readiness.Harnesses = []domain.HarnessInfo{
		{ID: "codex", Name: "Codex", Configured: true, Installation: &domain.RuntimeInstallation{}, ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount, domain.AgentProfileAPI}},
		{ID: "antigravity", Name: "Antigravity", Configured: true, Installation: &domain.RuntimeInstallation{}, ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount}},
	}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click("新增 Profile"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	if err := ui.Click("Profile 名称"); err != nil {
		t.Fatal(err)
	}
	ui.Type("OpenAI API")
	if err := ui.Click("Antigravity"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText("自定义 API") {
		t.Fatal("Antigravity offered an unsupported Profile sign-in method")
	}
	if err := ui.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("自定义 API"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("创建 Profile"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "runtime.profile.create")
	draft, ok := job.Input.(domain.AgentProfile)
	if !ok || draft.ID == "" || draft.HarnessID != "codex" || draft.Mode != domain.AgentProfileAPI || draft.Name != "OpenAI API" {
		t.Fatalf("wrong device Profile request: %+v", job.Input)
	}
}
