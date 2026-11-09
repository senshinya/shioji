package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSetupActionsStayOnSelectedDevice(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerMinor = 16
	state.Readiness = domain.Readiness{State: domain.RuntimeUnavailable, Harnesses: []domain.HarnessInfo{{Configured: true, ID: "custom", Name: "Custom Agent", State: domain.RuntimeUnavailable, Installation: &domain.RuntimeInstallation{Configurable: true, Issue: domain.RuntimeMissing, Guide: "在设备终端安装"}}}}
	controller := app.NewController("")
	v := New(m, controller, "")
	v.devices = true
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "runtime.catalog" {
			data, _ := json.Marshal(state.Readiness)
			job.Apply(m, data, nil)
		}
	}
	ui.Frame()
	if !ui.HasText("未安装") {
		t.Fatal("setup cause missing", ui.Texts())
	}
	if _, ok := ui.Find("Custom Agent 可执行文件"); !ok {
		if err := ui.Click("配置 Custom Agent 路径"); err != nil {
			t.Fatal(err)
		}
	}
	ui.Advance(250 * time.Millisecond)
	if err := ui.Click("Custom Agent 可执行文件"); err != nil {
		t.Fatal(err)
	}
	ui.Type("/Users/remote/bin/custom")
	ui.Advance(250 * time.Millisecond)
	if err := ui.Click("保存 Custom Agent 路径"); err != nil {
		t.Fatal(err)
	}
	var found *app.Job
	for len(controller.Jobs) > 0 {
		j := <-controller.Jobs
		if j.Method == "runtime.agents" {
			found = &j
		}
	}
	if found == nil || found.ConnectionID != "air" || found.ConnectionEpoch != "b" {
		t.Fatal("setup went to wrong device", found)
	}
	if in := found.Input.(protocol.DeviceAgents); len(in.Agents) != 1 || in.Agents[0].HarnessID != "custom" || in.Agents[0].Executable != "/Users/remote/bin/custom" {
		t.Fatal("remote path changed", in)
	}
}

func TestDeviceAgentStatusUpdatesWithinSameConnectionAndKeepsPathDraft(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerMinor = 16
	state.Readiness = domain.Readiness{State: domain.RuntimeReady, Harnesses: []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}}}
	controller := app.NewController("")
	v := New(m, controller, "")
	v.devices = true
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "runtime.catalog" {
			b, _ := json.Marshal(state.Readiness)
			job.Apply(m, b, nil)
		}
	}
	ui.Frame()
	if _, ok := ui.Find("Custom Agent 可执行文件"); !ok {
		if err := ui.Click("配置 Custom Agent 路径"); err != nil {
			t.Fatal(err)
		}
	}
	ui.Advance(250 * time.Millisecond)
	ui.Click("Custom Agent 可执行文件")
	ui.Type("/Users/remote/draft")
	ui.Advance(250 * time.Millisecond)
	state.Readiness.Harnesses[0].Installation = &domain.RuntimeInstallation{Configurable: true, Issue: domain.RuntimeAuthenticationExpired}
	state.Readiness.Harnesses[0].State = domain.RuntimeUnavailable
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("登录已失效") {
		t.Fatal("same-epoch status stale", ui.Texts())
	}
	ui.Click("保存 Custom Agent 路径")
	found := false
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "runtime.agents" {
			found = true
			in := job.Input.(protocol.DeviceAgents)
			if len(in.Agents) != 1 || in.Agents[0].Executable != "/Users/remote/draft" {
				t.Fatal("path draft lost", in)
			}
		}
	}
	if !found {
		t.Fatal("missing save")
	}
}

func TestPendingSSHLoginCommandUsesRemoteDeviceBeforeAdding(t *testing.T) {
	v := New(app.NewModel(), nil, "")
	v.sshTarget = "remote@air"
	v.sshPort = "2222"
	got := v.agentLoginCommand("", []string{"/remote/bin/agent", "login"})
	if !strings.Contains(got, "ssh -t -p 2222 -- 'remote@air'") || !strings.Contains(got, "/remote/bin/agent") {
		t.Fatal("onboarding login would run locally", got)
	}
}

func TestSavingDisabledAgentDoesNotEnableIt(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerMinor = 18
	controller := app.NewController("")
	v := New(m, controller, "")
	e := agentEditor([]domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: false, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}})
	e.epoch = state.ConnectionEpoch
	e.edit(e.catalog[0])
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).FillWidth().FillHeight().Children(func() { v.deviceAgentPage(c, c.Theme(), "air", "MacBook Air", state, e, e.catalog[0]) })
	}, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click("Custom Agent 可执行文件"); err != nil {
		t.Fatal(err)
	}
	ui.Type("/usr/bin/true")
	ui.Advance(250 * time.Millisecond)
	if err := ui.Click("保存 Custom Agent 路径"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	configured := false
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "runtime.agents" {
			t.Fatal("saving disabled Agent enabled it")
		}
		if job.Method == "runtime.configure" {
			configured = true
		}
	}
	if !configured {
		t.Fatal("disabled Agent settings not saved", ui.Texts(), m.Error)
	}
}
