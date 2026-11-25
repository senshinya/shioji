package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestSystemProfileBadgeDistinguishesScannedAgentInDeviceAndWelcomeLists(t *testing.T) {
	catalog := []domain.HarnessInfo{
		{ID: "codex", Name: "Codex", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{},
			ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount}},
		{ID: "work", Name: "Codex 工作", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{},
			Profile: &domain.AgentProfile{ID: "work", HarnessID: "codex", Name: "Codex 工作", Authorized: true}},
	}
	for _, scenario := range []struct {
		name     string
		firstRun bool
		legacy   bool
	}{
		{name: "device"},
		{name: "first run", firstRun: true},
		{name: "legacy configuration without profile creation capabilities", legacy: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m := deviceUIModel()
			m.FirstRun = scenario.firstRun
			state := m.DeviceStates["local"]
			state.WorkerMinor = protocol.AgentProfileMinor
			state.ConnectionEpoch = ""
			state.Readiness.Harnesses = append([]domain.HarnessInfo(nil), catalog...)
			if scenario.legacy {
				state.Readiness.Harnesses[0].ProfileModes = nil
				state.Readiness.Harnesses[0].Configured = false
			}
			v := New(m, nil, "")
			if scenario.firstRun {
				m.SessionID = ""
			} else {
				v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
			}
			ui := n.NewTester(v.View, 1100, 680)
			ui.Advance(time.Second)
			if scenario.firstRun {
				if err := ui.Click("这台 Mac"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
			}
			badge, shown := ui.Find("系统")
			system, hasSystem := ui.Find("Codex")
			independent, hasIndependent := ui.Find("Codex 工作")
			if !shown || !hasSystem || !hasIndependent || badge.X < system.X+system.W-1 || badge.Y < system.Y-1 || badge.Y >= independent.Y {
				t.Fatal("system badge must sit on the discovered Agent's line", system, badge, independent, ui.Texts())
			}
			count := 0
			for _, label := range ui.Texts() {
				if label == "系统" {
					count++
				}
			}
			if count != 1 {
				t.Fatal("an independent Profile was marked as system-managed", ui.Texts())
			}
		})
	}
}

func TestAntigravityCatalogIconUsesBrandArtwork(t *testing.T) {
	ready := domain.Readiness{HarnessID: "antigravity", Harnesses: []domain.HarnessInfo{
		{ID: "antigravity", Name: "Antigravity", Icon: "antigravity"},
	}}
	icon := harnessIcon(ready.Descriptor())
	if icon == nil || icon == genericAgentIcon || icon == openAIBlossom {
		t.Fatal("Antigravity needs its own catalog icon")
	}
}

func TestRunMenuUsesSelectedHarnessCatalogAndHidesUnsupportedControls(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.WorkerMinor = protocol.AgentProfileMinor
	m.Drafts["home"] = "keep draft"
	m.Readiness = domain.Readiness{HarnessID: "alpha", Harnesses: []domain.HarnessInfo{
		{ID: "alpha", Name: "Alpha Agent", State: "ready", ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount}, Models: []domain.Model{{ID: "a", Name: "A model", Default: true, Efforts: []string{"normal"}, DefaultEffort: "normal", Fast: true}}, Permissions: []string{"limited"}, Plan: true},
		{ID: "beta", Name: "Beta Agent", State: "ready", Profile: &domain.AgentProfile{ID: "beta", HarnessID: "alpha", Name: "Beta Agent", Authorized: true}, Models: []domain.Model{{ID: "b", Name: "B model", Default: true}}, Permissions: []string{"ask"}, PermissionChoices: []domain.OptionChoice{{ID: "ask", Name: "先询问"}}},
	}}
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("运行配置"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("Alpha Agent") || ui.HasText("Codex") {
		t.Fatal("menu contains hardcoded harness", ui.Texts())
	}
	if err := ui.Click("Agent"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(configurationTabDuration)
	if _, ok := ui.Find("系统"); !ok {
		t.Fatal("the Agent picker does not distinguish the system Profile", ui.Texts())
	}
	if err := ui.Click("Beta Agent"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if m.HarnessID() != "beta" || m.Config().Model != "b" || m.Drafts["home"] != "keep draft" {
		t.Fatal("selection did not bind run configuration")
	}
	if !ui.HasText("Beta Agent") || ui.HasText("推理强度") || ui.HasText("Plan") || ui.HasText("Fast") {
		t.Fatal("unsupported controls remain visible", ui.Texts())
	}
	if ui.HasText("Beta Agent · 系统") {
		t.Fatal("the independent choice inherited the system marker", ui.Texts())
	}
	if err := ui.Click("运行配置"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("先询问") {
		t.Fatal("permission label did not come from catalog", ui.Texts())
	}
}
