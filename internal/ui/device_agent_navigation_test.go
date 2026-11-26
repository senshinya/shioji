package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestDeviceProfileEmptyStateGuidesFirstConfiguration(t *testing.T) {
	for _, width := range []int{850, 1240} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := deviceUIModel()
			state := m.DeviceStates["air"]
			state.ConnectionEpoch = ""
			state.WorkerMinor = protocol.AgentProfileMinor
			state.Readiness.Harnesses = []domain.HarnessInfo{{
				ID: "codex", Name: "Codex", Configured: true, State: domain.RuntimeUnavailable,
				Installation: &domain.RuntimeInstallation{Issue: domain.RuntimeSignInRequired, SignInMethods: []domain.SignInMethod{domain.SignInBrowser}},
				ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount},
			}}
			v := New(m, app.NewController(""), "")
			v.devices, v.deviceSelected, v.deviceTab = true, "air", "agents"
			ui := n.NewTester(v.View, width, 680)
			ui.SetDark(width == 850)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("profile-empty-%d.png", width))
			if !ui.HasText("为这台设备保存 Agent 的账号授权或 API Key。") || !ui.HasText("还没有 Profile") {
				t.Fatal("empty Profile page must explain what the first configuration is for", ui.Texts())
			}
			state.Connected = false
			ui.Frame()
			if ui.HasText("还没有 Profile") || !ui.HasText("连接后查看这台设备的 Profile。") {
				t.Fatal("offline Profile page looks like a confirmed empty list", ui.Texts())
			}
			state.Connected = true
			ui.Frame()
			if err := ui.Click("新增 Profile"); err != nil {
				t.Fatal(err)
			}
			if v.deviceProfile == nil {
				t.Fatal("toolbar action did not open the Profile form")
			}
		})
	}
}

func TestProfileModelPageShowsEffortSettings(t *testing.T) {
	id := "effort-profile"
	agent := domain.HarnessInfo{
		ID: id, Name: "Gemini 公司专用", Configured: true, State: domain.RuntimeReady,
		Profile:      &domain.AgentProfile{ID: id, Name: "Gemini 公司专用", HarnessID: domain.AgentAnthropic, Mode: domain.AgentProfileAPI, Authorized: true},
		Installation: &domain.RuntimeInstallation{},
		Models:       []domain.Model{{ID: "gemini-4-neon", Default: true, Efforts: []string{"low", "medium", "high", "xhigh", "max"}}},
	}
	open := func(agent domain.HarnessInfo, width int, dark bool) *n.Tester {
		t.Helper()
		m := deviceUIModel()
		state := m.DeviceStates["air"]
		state.ConnectionEpoch = ""
		state.WorkerMinor = protocol.ProfileEffortsMinor
		state.Readiness.Harnesses = []domain.HarnessInfo{agent}
		v := New(m, app.NewController(""), "")
		v.devices, v.deviceSelected, v.deviceTab = true, "air", "agents"
		ui := n.NewTester(v.View, width, 740)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		if err := ui.Click("打开 " + agent.Name + " 配置"); err != nil {
			t.Fatal(err, ui.Texts())
		}
		ui.Advance(time.Second)
		if err := ui.Click("模型"); err != nil {
			t.Fatal(err, ui.Texts())
		}
		ui.Advance(time.Second)
		if !ui.HasText("推理档位") {
			t.Fatal("model tab does not expose per-profile effort settings", ui.Texts())
		}
		return ui
	}
	ui := open(agent, 1160, false)
	if !ui.HasText("自动") || ui.HasText("恢复自动") {
		t.Fatal("automatic settings show a manual override", ui.Texts())
	}
	saveNativeSkillImage(t, ui, "profile-effort-automatic.png")

	agent.Models[0].Efforts = []string{"low", "medium", "high", "xhigh"}
	agent.EffortOverrides = map[string][]string{"gemini-4-neon": agent.Models[0].Efforts}
	ui = open(agent, 850, true)
	if !ui.HasText("手工配置") || !ui.HasText("恢复自动") {
		t.Fatal("stored override cannot be restored to automatic", ui.Texts())
	}
	saveNativeSkillImage(t, ui, "profile-effort-manual.png")

	agent.Name = "Codex API"
	agent.Profile = &domain.AgentProfile{ID: id, Name: agent.Name, HarnessID: domain.AgentResponses, Mode: domain.AgentProfileAPI, Authorized: true}
	agent.EffortOverrides = nil
	agent.Models = []domain.Model{
		{ID: "gpt-6.1-sol", Default: true, Efforts: []string{"low", "medium", "high", "xhigh"}},
		{ID: "gpt-6-luna", Efforts: []string{"medium", "high"}},
	}
	ui = open(agent, 1160, false)
	if !ui.HasText("配置模型") {
		t.Fatal("Codex Profile does not expose a model-specific editor", ui.Texts())
	}
	saveNativeSkillImage(t, ui, "profile-effort-codex.png")
}

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

func TestProfileAuthorizationResumesFromDeviceListAndChecksModelsSeparately(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = protocol.AgentProfileMinor
	profile := domain.AgentProfile{ID: "profile", Name: "工作账号", HarnessID: "codex", Mode: domain.AgentProfileAccount}
	agent := domain.HarnessInfo{ID: "codex", Name: "Codex", Configured: true,
		Installation: &domain.RuntimeInstallation{SignInMethods: []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode}},
		ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount}}
	info := domain.HarnessInfo{ID: profile.ID, Name: profile.Name, Profile: &profile, Configured: true,
		Installation: &domain.RuntimeInstallation{Issue: domain.RuntimeSignInRequired, SignInMethods: agent.Installation.SignInMethods}}
	state.Readiness.Harnesses = []domain.HarnessInfo{agent, info}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "air", "agents"
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)
	click := func(label string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(err, ui.Texts())
		}
		ui.Advance(time.Second)
	}
	click("打开 工作账号 配置")
	click("继续授权")
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	if job.ConnectionID != "air" || in.HarnessID != profile.ID || in.Method != domain.SignInDeviceCode || in.Configuration != nil {
		t.Fatal("resuming authorization did not use the selected Profile on its owning device")
	}
	body, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: profile.ID, Phase: domain.SignInWaiting})
	job.Apply(m, body, nil)
	ui.Frame()
	if !ui.HasText("等待账号授权") {
		t.Fatal(ui.Texts())
	}
	// Apply the worker's cancellation receipt without opening a real provider.
	if err := ui.Click("取消授权"); err != nil {
		t.Fatal(err)
	}
	cancel := welcomeJob(t, v.Controller, "runtime.signin.cancel")
	if cancel.Input.(protocol.SignInInput).ID != in.ID {
		t.Fatal("cancel targeted another authorization")
	}
	body, _ = json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: profile.ID, Phase: domain.SignInCanceled})
	cancel.Apply(m, body, nil)
	ui.Frame()
	click("稍后设置")
	click("打开 工作账号 配置")
	click("继续授权")
	job = welcomeJob(t, v.Controller, "runtime.signin.start")
	next := job.Input.(protocol.SignInInput)
	if next.HarnessID != profile.ID || next.ID == in.ID {
		t.Fatal("retry must keep the Profile and start a fresh authorization")
	}
	profile.Authorized = true
	info.Profile, info.State, info.Error = &profile, domain.RuntimeUnavailable, "测试模型读取失败"
	info.Installation = &domain.RuntimeInstallation{Issue: domain.RuntimeUnavailableIssue}
	info.Account = &domain.RuntimeAccount{Mode: "account", State: "authenticated"}
	body, _ = json.Marshal(domain.SignInStatus{ID: next.ID, HarnessID: profile.ID, Phase: domain.SignInCompleted, Profile: &info})
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("测试模型读取失败") {
		t.Fatal(ui.Texts())
	}
	click("稍后设置")
	if !ui.HasText("模型待确认") {
		t.Fatal(ui.Texts())
	}
	click("打开 工作账号 配置")
	click("重试读取模型")
	check := welcomeJob(t, v.Controller, "runtime.check")
	if check.ConnectionID != "air" || check.Input.(protocol.RuntimeConfiguration).HarnessID != profile.ID || len(v.Controller.Jobs) != 0 {
		t.Fatal("model retry did not target the bound Profile or restarted authorization")
	}
	info.State, info.Error = domain.RuntimeReady, ""
	info.Installation = &domain.RuntimeInstallation{}
	body, _ = json.Marshal(domain.Readiness{Harnesses: []domain.HarnessInfo{agent, info}})
	check.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if _, listed := ui.Find("打开 工作账号 配置"); !listed || ui.HasText("重试读取模型") {
		t.Fatal("ready Profile did not return to the list", ui.Texts())
	}
}

func TestProfileCreationRetriesUncertainReceiptAndIgnoresLateNavigation(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = protocol.AgentProfileMinor
	agent := domain.HarnessInfo{ID: "codex", Name: "Codex", Configured: true,
		Installation: &domain.RuntimeInstallation{SignInMethods: []domain.SignInMethod{domain.SignInBrowser}},
		ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount}}
	state.Readiness.Harnesses = []domain.HarnessInfo{agent}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1100, 680)
	ui.Advance(time.Second)
	click := func(label string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(err, ui.Texts())
		}
		ui.Advance(time.Second)
	}
	click("新增 Profile")
	click("选择 Agent")
	click("Codex")
	click("Profile 名称")
	ui.Type("测试账号")
	click("创建并登录")
	create := welcomeJob(t, v.Controller, "runtime.profile.create")
	initial := create.Input.(domain.AgentProfile)
	create.Apply(m, nil, errors.New("test lost reply"))
	ui.Frame()
	if !ui.HasText("创建结果待确认") {
		t.Fatal(ui.Texts())
	}
	click("创建并登录")
	retry := welcomeJob(t, v.Controller, "runtime.profile.create")
	if retry.Input != initial {
		t.Fatal("an uncertain result changed the worker's creation identity")
	}
	retry.Apply(m, nil, &protocol.RemoteError{Code: "notSent", Message: "测试连接已变化"})
	ui.Frame()
	click("创建并登录")
	retry = welcomeJob(t, v.Controller, "runtime.profile.create")
	if retry.Input != initial {
		t.Fatal("an unsent retry discarded an earlier unknown creation identity")
	}
	retry.Apply(m, nil, &protocol.RemoteError{Code: "worker", Message: "测试执行服务正在更新"})
	ui.Frame()
	click("创建并登录")
	retry = welcomeJob(t, v.Controller, "runtime.profile.create")
	if retry.Input != initial {
		t.Fatal("a worker admission rejection discarded an earlier unknown creation identity")
	}
	click("选择设备：MacBook Air")
	info := domain.HarnessInfo{ID: initial.ID, Name: initial.Name, Profile: &initial, Configured: true,
		Installation: &domain.RuntimeInstallation{Issue: domain.RuntimeSignInRequired, SignInMethods: agent.Installation.SignInMethods}}
	catalog := []domain.HarnessInfo{agent, info}
	body, _ := json.Marshal(protocol.AgentSetupResult{Ready: true, Catalog: catalog, Readiness: domain.Readiness{Harnesses: catalog}})
	retry.Apply(m, body, nil)
	ui.Advance(time.Second)
	if v.deviceSelected != "air" || ui.HasText("测试账号") {
		t.Fatal("a late creation result navigated away from the other device", ui.Texts())
	}
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; job.Method == "runtime.signin.start" {
			t.Fatal("a late receipt started authorization after leaving the creation page")
		}
	}
}

func TestProfilePageUsesAuthoritativeBindingWithoutSignInReceipt(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = protocol.AgentProfileMinor
	profile := domain.AgentProfile{ID: "api-profile", Name: "工作 API", HarnessID: "codex", Mode: domain.AgentProfileAPI}
	info := domain.HarnessInfo{ID: profile.ID, Name: profile.Name, Configured: true, Profile: &profile,
		Installation: &domain.RuntimeInstallation{Issue: domain.RuntimeSignInRequired, APISetup: true}}
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1100, 680)
	ui.Advance(time.Second)
	if err := ui.Click("打开 工作 API 配置"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("API 地址"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	bound := profile
	bound.Authorized = true
	info.Profile, info.State = &bound, domain.RuntimeReady
	info.Installation = &domain.RuntimeInstallation{}
	info.Account = &domain.RuntimeAccount{Mode: "api", State: "authenticated"}
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	ui.Frame()
	ui.Advance(time.Second)
	if _, listed := ui.Find("打开 工作 API 配置"); !listed || ui.HasText("重试连接") {
		t.Fatal("authoritative binding did not exit the flow without resending credentials", ui.Texts())
	}
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; job.Method == "runtime.signin.start" {
			t.Fatal("authoritative binding caused another sign-in")
		}
	}
}

func TestProfileWithMissingRuntimeOpensExistingAgentDetail(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = protocol.AgentProfileMinor
	profile := domain.AgentProfile{ID: "profile", Name: "工作账号", HarnessID: "codex", Mode: domain.AgentProfileAccount}
	state.Readiness.Harnesses = []domain.HarnessInfo{{
		ID: profile.ID, Name: profile.Name, Profile: &profile, Configured: true, State: domain.RuntimeUnavailable,
		Installation: &domain.RuntimeInstallation{Issue: domain.RuntimeMissing, Documentation: "https://example.test/install"},
	}}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1100, 680)
	ui.Advance(time.Second)
	if err := ui.Click("打开 工作账号 配置"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("未安装") || !ui.HasText("可执行文件") || ui.HasText("继续授权") {
		t.Fatal("runtime repairs lost the existing Agent detail", ui.Texts())
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
		{ID: "codex", Name: "Codex", Configured: true, Installation: &domain.RuntimeInstallation{APISetup: true, SignInMethods: []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode}}, ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount, domain.AgentProfileAPI}},
		{ID: "antigravity", Name: "Antigravity", Configured: true, Installation: &domain.RuntimeInstallation{SignInMethods: []domain.SignInMethod{domain.SignInBrowser}}, ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount}},
	}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click("新增 Profile"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	if _, ok := ui.Find("配置 Codex 路径"); ok {
		t.Fatal("the creation form left the Agent list visible", ui.Texts())
	}
	if err := ui.Click("选择 Agent"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("Antigravity"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText("自定义 API") {
		t.Fatal("Antigravity offered an unsupported Profile sign-in method")
	}
	if err := ui.Click("选择 Agent"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("Profile 名称"); err != nil {
		t.Fatal(err)
	}
	ui.Type("OpenAI API")
	if err := ui.Click("自定义 API"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("API 地址"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Type("https://api.example.test/v1")
	if err := ui.Click("输入 API Key"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Type("transient-test-key")
	if err := ui.Click("创建并连接"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "runtime.profile.create")
	draft, ok := job.Input.(domain.AgentProfile)
	if !ok || draft.ID == "" || draft.HarnessID != "codex" || draft.Mode != domain.AgentProfileAPI || draft.Name != "OpenAI API" {
		t.Fatalf("wrong device Profile request: %+v", job.Input)
	}
	if len(v.Controller.Jobs) != 0 {
		t.Fatal("authorization started without a worker receipt")
	}
	info := domain.HarnessInfo{ID: draft.ID, Name: draft.Name, Profile: &draft, Configured: true, Installation: &domain.RuntimeInstallation{APISetup: true, Issue: domain.RuntimeSignInRequired}}
	catalog := append(append([]domain.HarnessInfo(nil), state.Readiness.Harnesses...), info)
	body, _ := json.Marshal(protocol.AgentSetupResult{Ready: true, Catalog: catalog, Readiness: domain.Readiness{Harnesses: catalog}})
	job.Apply(m, body, nil)
	signIn := welcomeJob(t, v.Controller, "runtime.signin.start")
	input := signIn.Input.(protocol.SignInInput)
	if signIn.ConnectionID != "local" || input.HarnessID != draft.ID || input.Method != domain.SignInAPI ||
		input.API == nil || input.API.Endpoint != "https://api.example.test/v1" || input.API.Credential != "transient-test-key" {
		t.Fatalf("connection did not use the newly created Profile and form (owner %q, profile match %v, method %q, API present %v, endpoint match %v, key match %v)",
			signIn.ConnectionID, input.HarnessID == draft.ID, input.Method, input.API != nil,
			input.API != nil && input.API.Endpoint == "https://api.example.test/v1", input.API != nil && input.API.Credential == "transient-test-key")
	}
	body, _ = json.Marshal(domain.SignInStatus{ID: input.ID, HarnessID: draft.ID, Phase: domain.SignInFailed, Message: "测试连接失败"})
	signIn.Apply(m, body, nil)
	ui.Advance(time.Second)
	if !ui.HasText("测试连接失败") || ui.HasText("常规") {
		t.Fatal("failure left the creation flow", ui.Texts())
	}
	if err := ui.Click("重试连接"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	retry := welcomeJob(t, v.Controller, "runtime.signin.start")
	next := retry.Input.(protocol.SignInInput)
	if next.HarnessID != draft.ID || next.ID == input.ID || next.API == nil || *next.API != *input.API || len(v.Controller.Jobs) != 0 {
		t.Fatal("retry did not reuse the Profile and its in-memory form")
	}
	bound := draft
	bound.Authorized = true
	info.Profile, info.State = &bound, domain.RuntimeReady
	info.Installation = &domain.RuntimeInstallation{}
	info.Account = &domain.RuntimeAccount{Mode: "api", State: "authenticated"}
	body, _ = json.Marshal(domain.SignInStatus{ID: next.ID, HarnessID: draft.ID, Phase: domain.SignInCompleted, Profile: &info})
	retry.Apply(m, body, nil)
	ui.Advance(time.Second)
	if _, ok := ui.Find("打开 OpenAI API 配置"); !ok || ui.HasText("API Key") {
		t.Fatal("completion did not return to the new Agent row", ui.Texts())
	}
}

func TestDeviceProfilesHideUnconfiguredTypesAndDeepSeekOnlyAsksForKey(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = protocol.AgentProfileMinor
	profile := domain.AgentProfile{ID: "personal", Name: "个人配置", HarnessID: "deepseek", Mode: domain.AgentProfileAPI, Authorized: true}
	state.Readiness.Harnesses = []domain.HarnessInfo{
		{ID: "codex", Name: "Codex", Configured: true, ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAccount}, Installation: &domain.RuntimeInstallation{SignInMethods: []domain.SignInMethod{domain.SignInBrowser}}},
		{ID: "deepseek", Name: "deepseek", Icon: "deepseek", ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAPI}, Installation: &domain.RuntimeInstallation{APISetup: true, APIKeyOnly: true}},
		{ID: profile.ID, Name: profile.Name, Profile: &profile, Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{}},
	}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1100, 680)
	ui.Advance(time.Second)
	if ui.HasText("Codex") || !ui.HasText(profile.Name) {
		t.Fatal("the profile list must show saved profiles only", ui.Texts())
	}
	if _, ok := ui.Find("启用 " + profile.Name); ok {
		t.Fatal("a login profile has a power switch")
	}
	click := func(label string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(err, ui.Texts())
		}
		ui.Advance(time.Second)
	}
	click("新增 Profile")
	click("选择 Agent")
	click("deepseek")
	if ui.HasText("服务地址") {
		t.Fatal("DeepSeek should use its fixed service URL")
	}
	click("输入 API Key")
	ui.Type("fixture-key")
	click("选择 Agent")
	click("Codex")
	click("选择 Agent")
	click("deepseek")
	if v.deviceProfile.auth.apiCredential != "" || v.deviceProfile.auth.apiReveal {
		t.Fatal("switching agents retained a previous credential")
	}
	click("输入 API Key")
	ui.Type("fresh-fixture-key")
	click("连接")
	create := welcomeJob(t, v.Controller, "runtime.profile.create")
	draft := create.Input.(domain.AgentProfile)
	if draft.Name != "deepseek" || draft.HarnessID != "deepseek" || draft.Mode != domain.AgentProfileAPI {
		t.Fatal("unexpected DeepSeek profile metadata", draft)
	}
	info := domain.HarnessInfo{ID: draft.ID, Name: draft.Name, Profile: &draft, Configured: true,
		Installation: &domain.RuntimeInstallation{Issue: domain.RuntimeSignInRequired, APISetup: true, APIKeyOnly: true}}
	catalog := append(append([]domain.HarnessInfo(nil), state.Readiness.Harnesses...), info)
	data, _ := json.Marshal(protocol.AgentSetupResult{Ready: true, Catalog: catalog, Readiness: domain.Readiness{Harnesses: catalog}})
	create.Apply(m, data, nil)
	signIn := welcomeJob(t, v.Controller, "runtime.signin.start")
	input := signIn.Input.(protocol.SignInInput)
	if signIn.ConnectionID != "local" || input.HarnessID != draft.ID || input.Method != domain.SignInAPI || input.API == nil || input.API.Endpoint != "" || input.API.Credential != "fresh-fixture-key" {
		t.Fatal("DeepSeek setup did not send a key-only request for its own Profile")
	}
}

func TestCustomProfileUsesDropdownExplicitIconAndOwningDeviceModelList(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = protocol.CustomAgentProfileMinor
	state.Readiness.Harnesses = []domain.HarnessInfo{
		{ID: domain.AgentResponses, Name: "Responses API", ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAPI}, Installation: &domain.RuntimeInstallation{APISetup: true, Issue: domain.RuntimeMissing}},
		{ID: domain.AgentAnthropic, Name: "Anthropic API", ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAPI}, Installation: &domain.RuntimeInstallation{APISetup: true, Issue: domain.RuntimeMissing}},
	}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1100, 700)
	click := func(label string) {
		t.Helper()
		ui.Advance(time.Second)
		if err := ui.Click(label); err != nil {
			t.Fatal(err, ui.Texts())
		}
		ui.Advance(time.Second)
	}
	click("新增 Profile")
	click("选择 Agent")
	click("自定义")
	if !ui.HasText("图标") {
		t.Fatal("explicit icon row is missing", ui.Texts())
	}
	click("选择图标")
	saveNativeSkillImage(t, ui, "custom-api-icons-light.png")
	for _, label := range []string{"Codex", "DeepSeek", "Antigravity", "Anthropic"} {
		if _, ok := ui.Find(label); !ok {
			t.Fatalf("missing profile icon %q", label)
		}
	}
	if _, ok := ui.Find("从文件选取…"); ok {
		t.Fatal("the icon picker should only offer the four brand icons")
	}
	click("DeepSeek")
	if icon := v.deviceProfile.draft.Icon; icon != "deepseek" {
		t.Fatalf("DeepSeek selection saved %q", icon)
	}
	ui.SetDark(true)
	click("选择图标")
	saveNativeSkillImage(t, ui, "custom-api-icons-dark.png")
	click("Anthropic")
	click("Anthropic API")
	click("Profile 名称")
	ui.Type("自定义测试")
	click("输入 API 地址")
	ui.Type("http://203.0.113.42:8080")
	click("输入 API Key")
	ui.Type("fixture-key")
	click("获取模型")
	models := welcomeJob(t, v.Controller, "runtime.profile.models")
	in := models.Input.(protocol.ProfileModelInput)
	if models.ConnectionID != "local" || in.HarnessID != domain.AgentAnthropic || in.API.Endpoint != "http://203.0.113.42:8080" {
		t.Fatal("model list did not target the selected device and protocol")
	}
	body, _ := json.Marshal([]domain.Model{{ID: "test-model", Name: "测试模型"}})
	models.Apply(m, body, nil)
	ui.Frame()
	click("test-model")
	click("安装并连接")
	create := welcomeJob(t, v.Controller, "runtime.profile.create")
	draft := create.Input.(domain.AgentProfile)
	if draft.HarnessID != domain.AgentAnthropic || draft.Icon != "claude" || draft.Mode != domain.AgentProfileAPI {
		t.Fatal("the saved Profile did not retain its API type and selected icon", draft)
	}
	info := domain.HarnessInfo{ID: draft.ID, Name: draft.Name, Profile: &draft, Installation: &domain.RuntimeInstallation{APISetup: true, Issue: domain.RuntimeMissing}}
	catalog := append(state.Readiness.Harnesses, info)
	body, _ = json.Marshal(protocol.AgentSetupResult{Ready: true, Catalog: catalog, Readiness: domain.Readiness{Harnesses: catalog}})
	create.Apply(m, body, nil)
	start := welcomeJob(t, v.Controller, "runtime.signin.start")
	api := start.Input.(protocol.SignInInput).API
	if api == nil || !api.InstallRuntime || api.Model != "test-model" || api.Endpoint != "http://203.0.113.42:8080" {
		t.Fatal("the selected model or installation intent was not sent")
	}
}

func TestBoundClaudeProfileInstallsMissingRuntimeWithoutNewCredentials(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.ConnectionEpoch = ""
	state.WorkerMinor = protocol.CustomAgentProfileMinor
	profile := &domain.AgentProfile{ID: "saved-claude", Name: "Claude Work", HarnessID: domain.AgentAnthropic, Mode: domain.AgentProfileAPI, Authorized: true}
	state.Readiness.Harnesses = []domain.HarnessInfo{
		{ID: profile.ID, Name: profile.Name, Profile: profile, Configured: true, State: domain.RuntimeUnavailable,
			Installation: &domain.RuntimeInstallation{CanRepair: true, Issue: domain.RuntimeMissing}},
	}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "local", "agents"
	ui := n.NewTester(v.View, 1100, 700)
	for _, label := range []string{profile.Name, "安装运行环境"} {
		ui.Advance(time.Second)
		if err := ui.Click(label); err != nil {
			t.Fatal(err, ui.Texts())
		}
	}
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	input := job.Input.(protocol.SignInInput)
	if job.ConnectionID != "local" || input.HarnessID != profile.ID || input.Method != domain.SignInAPI ||
		input.API == nil || *input.API != (domain.APIConfiguration{InstallRuntime: true}) {
		t.Fatal("runtime repair did not target the bound Profile")
	}
}

func TestAnthropicProviderOnlyAppearsInCustomProfileSetup(t *testing.T) {
	m := deviceUIModel()
	v := New(m, app.NewController(""), "")
	editor := agentEditor([]domain.HarnessInfo{
		{ID: "codex", Name: "Codex", Configured: true},
		{ID: domain.AgentAnthropic, Name: "Anthropic API", Configured: true,
			ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAPI},
			Installation: &domain.RuntimeInstallation{APISetup: true}},
		{ID: "saved-api-profile", Name: "My API", Profile: &domain.AgentProfile{HarnessID: domain.AgentAnthropic}},
	})
	f := &welcomeFlow{key: "local", width: 560, agents: editor}
	welcome := n.NewTester(func(c *n.Context) { v.welcomeAgents(c, c.Theme(), f, false) }, 800, 480)
	welcome.Advance(time.Second)
	if welcome.HasText("Anthropic API") || !welcome.HasText("Codex") || !welcome.HasText("My API") {
		t.Fatal("first-run list exposed the provider instead of the saved Profile", welcome.Texts())
	}
	legacy := n.NewTester(func(c *n.Context) { v.agentRows(c, c.Theme(), "local", true, editor, false, func() {}) }, 800, 480)
	if legacy.HasText("Anthropic API") || !legacy.HasText("My API") {
		t.Fatal("legacy device list exposed a standalone provider", legacy.Texts())
	}
	choices := deviceProfileProviders(editor)
	if len(choices) != 1 || choices[0].ID != customProfileChoice {
		t.Fatalf("expected the existing custom Profile entry, got %+v", choices)
	}
	if in := editor.input(); len(in.Agents) != 1 || in.Agents[0].HarnessID != "codex" {
		t.Fatalf("device setup submitted a custom provider as an Agent: %+v", in)
	}
}
