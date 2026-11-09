package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"testing"
	"time"
)

func welcomeModel() *app.Model {
	m := deviceUIModel()
	m.FirstRun = true
	m.SessionID = ""
	m.ActiveConnection = "local"
	m.Drafts["home"] = "保留首页草稿"
	m.DeviceStates["local"].WorkerMinor = 18
	return m
}
func TestWelcomeIsFullPageAndSkipKeepsDraft(t *testing.T) {
	m := welcomeModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	if !ui.HasText("选择设备") {
		t.Fatal("first run lacks device step", ui.Texts())
	}
	if _, ok := ui.Find("新建会话"); ok {
		t.Fatal("empty sidebar occupies welcome")
	}
	r, ok := ui.Find("欢迎内容")
	if !ok || math.Abs(float64(r.X-270)) > 1 || math.Abs(float64(r.Y-192)) > 1 || math.Abs(float64(r.W-560)) > 1 {
		t.Fatal("welcome differs from approved alignment", r)
	}
	if err := ui.Click("稍后设置"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if m.FirstRun || m.SessionID != "" || m.Drafts["home"] != "保留首页草稿" {
		t.Fatal("skip creates task or discards draft")
	}
	if _, ok := ui.Find("新建会话"); !ok {
		t.Fatal("skip did not reach home")
	}
}
func TestWelcomeDeviceConfigCancelDiscardsChanges(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	ui.Click("配置本机")
	ui.Advance(time.Second)
	if err := ui.Click("设备显示名称"); err != nil {
		t.Fatal(err)
	}
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("取消的名称")
	ui.Click("取消")
	ui.Advance(time.Second)
	ui.Click("配置本机")
	ui.Advance(time.Second)
	if v.welcome.deviceName != m.LocalName() {
		t.Fatal("cancelled name restored as draft", v.welcome.deviceName)
	}
	ui.Click("设备显示名称")
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("开发 Mac")
	ui.Click("保存")
	ui.Advance(time.Second)
	if m.LocalName() != "开发 Mac" {
		t.Fatal("local alias not saved", m.LocalName())
	}
}
func welcomeJob(t *testing.T, c *app.Controller, method string) app.Job {
	t.Helper()
	for len(c.Jobs) > 0 {
		j := <-c.Jobs
		if j.Method == method {
			return j
		}
	}
	t.Fatalf("missing %s job", method)
	return app.Job{}
}
func setupCatalog() []domain.HarnessInfo {
	return []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true, OptionsSupported: domain.RuntimeOptionSupport{Arguments: true, Directory: true, Environment: true, Instructions: true, TitleModel: true}}, Models: []domain.Model{{ID: "model", Name: "Model One"}}}}
}
func TestWelcomeAgentConfigurationAndAdmissionStayDeviceScoped(t *testing.T) {
	m := welcomeModel()
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	ui.Click("继续")
	ui.Advance(time.Second)
	catalog := welcomeJob(t, c, "runtime.catalog")
	if catalog.ConnectionID != "local" || catalog.ConnectionEpoch != "a" {
		t.Fatal("catalog escaped owning worker", catalog)
	}
	body, _ := json.Marshal(domain.Readiness{Harnesses: setupCatalog()})
	catalog.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("配置 Custom Agent"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	ui.Click("高级")
	ui.Advance(configurationTabDuration)
	if err := ui.Click("启动参数输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("--profile work")
	if err := ui.Click("保存"); err != nil {
		t.Fatal("save click", err)
	}
	ui.Advance(time.Second)
	if v.welcome.step != welcomeAgents {
		t.Fatal("save failed", v.welcome.step, v.welcome.agents.error, ui.Texts())
	}
	ui.Click("继续")
	ui.Advance(time.Second)
	accepted := welcomeJob(t, c, "runtime.agents")
	in := accepted.Input.(protocol.DeviceAgents)
	if accepted.ConnectionID != "local" || len(in.Agents) != 1 || in.Agents[0].HarnessID != "custom" || in.Agents[0].Options == nil || len(in.Agents[0].Options.Arguments) != 2 {
		t.Fatal("configuration lost at admission", accepted, in)
	}
	if ui.HasText("添加工作区") {
		t.Fatal("advanced before authoritative admission")
	}
	body, _ = json.Marshal(protocol.AgentSetupResult{Ready: true, Catalog: setupCatalog()})
	accepted.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("添加工作区") || !m.FirstRun {
		t.Fatal("admission did not open workspace or completed early", ui.Texts())
	}
}

func TestWelcomeWorkspaceWaitsForOwningWorkerAndPreservesDraft(t *testing.T) {
	m := welcomeModel()
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.key = "air"
	f.name = "算法"
	f.directory = "/Users/remote/red-black-tree"
	f.directories = []welcomeDirectory{{id: 1, path: "/Users/remote/shared"}}
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	if err := ui.Click("开始使用"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	job := welcomeJob(t, c, "workspace.create")
	in := job.Input.(protocol.WorkspaceInput)
	if job.ConnectionID != "air" || job.ConnectionEpoch != "b" || in.Name != "算法" || len(in.Folders) != 2 || in.Folders[1] != "/Users/remote/shared" {
		t.Fatal("workspace escaped owning device", job, in)
	}
	if !m.FirstRun || m.SessionID != "" {
		t.Fatal("created empty session or completed before worker")
	}
	job.Apply(m, nil, fmt.Errorf("路径不可用"))
	ui.Frame()
	ui.Advance(time.Second)
	if !m.FirstRun || f.directory != "/Users/remote/red-black-tree" || !ui.HasText("路径不可用") {
		t.Fatal("failed create discarded form")
	}
	ui.Click("开始使用")
	ui.Frame()
	job = welcomeJob(t, c, "workspace.create")
	body, _ := json.Marshal(domain.Workspace{ID: "created", DeviceID: "B", Name: "算法", Folders: in.Folders})
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if m.FirstRun || m.WorkspaceID != "created" || m.ConnectionID() != "air" || m.SessionID != "" || m.Drafts["home"] != "保留首页草稿" {
		t.Fatal("handoff changed draft or ownership", m.WorkspaceID, m.ConnectionID())
	}
}
func TestAbandonedWelcomeWorkspaceCannotStealNavigation(t *testing.T) {
	m := welcomeModel()
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeWorkspace
	f.key = "local"
	f.name = "新项目"
	f.directory = "/Users/local/project"
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	ui.Click("开始使用")
	job := welcomeJob(t, c, "workspace.create")
	v.finishWelcome()
	m.WorkspaceID = "w"
	body, _ := json.Marshal(domain.Workspace{ID: "late", DeviceID: "A", Name: "Late"})
	job.Apply(m, body, nil)
	ui.Frame()
	if m.WorkspaceID != "w" || m.FirstRun {
		t.Fatal("late callback stole navigation")
	}
}

func TestWelcomeSSHRefreshDoesNotUseLocalRuntime(t *testing.T) {
	m := welcomeModel()
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeAgentOptions
	f.key = ""
	f.agents = agentEditor(setupCatalog())
	v.sshTarget = "remote@air"
	v.sshCheck = sshSelecting
	v.sshSetup.Info = transport.WorkerInfo{DeviceID: "B", Directory: "/Users/remote/worker", Worker: "/worker", Minor: 18}
	f.agents.edit(f.agents.catalog[0])
	f.agents.draft.arguments = "--profile keep"
	v.recheckWelcomeAgent("", "custom")
	job := welcomeJob(t, c, "$device.check")
	if in := job.Input.(app.CheckSSHDevice); in.SSH.Target != "remote@air" {
		t.Fatal("refresh targets controller Mac")
	}
	body, _ := json.Marshal(app.DeviceSetupCheck{Info: v.sshSetup.Info, Catalog: setupCatalog()})
	job.Apply(m, body, nil)
	if f.agents.draft.arguments != "--profile keep" || f.step != welcomeAgentOptions {
		t.Fatal("refresh discarded draft or navigated")
	}
}

func TestWelcomeNavigationSlidesWithoutFadingAndRespectsReduceMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		m := welcomeModel()
		m.Preferences.ReduceMotion = reduced
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1100, 800)
		ui.Advance(time.Second)
		ui.Click("配置本机")
		ui.Advance(80 * time.Millisecond)
		mid, ok := ui.Find("配置设备")
		if !ok {
			t.Fatal("incoming page missing")
		}
		ui.Advance(time.Second)
		end, _ := ui.Find("配置设备")
		if reduced && math.Abs(float64(mid.X-end.X)) > 1 || !reduced && mid.X <= end.X+1 {
			t.Fatal("next did not slide left or Reduce Motion moved", reduced, mid, end)
		}
		ui.Click("取消")
		ui.Advance(80 * time.Millisecond)
		back, _ := ui.Find("选择设备")
		ui.Advance(time.Second)
		settled, _ := ui.Find("选择设备")
		if !reduced && back.X >= settled.X-1 {
			t.Fatal("Back did not reverse slide", back, settled)
		}
	}
}

func TestSSHAgentAdmissionFailureRemainsVisibleAndSelected(t *testing.T) {
	m := welcomeModel()
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeAgents
	f.key = ""
	f.agents = agentEditor(setupCatalog())
	v.sshAgents = f.agents
	v.sshCheck = sshSelecting
	v.sshTarget = "remote@air"
	v.sshSetup.Info = transport.WorkerInfo{DeviceID: "B", Directory: "/worker-state", Worker: "/worker", Minor: 18}
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	v.completeSSHDevice()
	job := welcomeJob(t, c, "$device.complete")
	catalog := setupCatalog()
	catalog[0].Installation.Issue = domain.RuntimeAuthenticationExpired
	body, _ := json.Marshal(protocol.AgentSetupResult{Ready: false, Error: "需要重新登录", Catalog: catalog})
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("需要重新登录") || !f.agents.catalog[0].Configured {
		t.Fatal("SSH setup error or selection lost", ui.Texts())
	}
	if f.agents.catalog[0].Installation.Issue != domain.RuntimeAuthenticationExpired {
		t.Fatal("returned runtime status not shown")
	}
}
func TestSSHSetupBoundsRetainCloseWithManyEnvironmentVariables(t *testing.T) {
	m := welcomeModel()
	m.FirstRun = false
	c := app.NewController("")
	v := New(m, c, "")
	v.devices = true
	v.sshCheck = sshSelecting
	f := v.setupFlow()
	f.step = welcomeAgentOptions
	f.key = ""
	f.agents = agentEditor(setupCatalog())
	v.sshAgents = f.agents
	v.sshSetup.Info.Minor = 18
	f.agents.edit(f.agents.catalog[0])
	f.agents.tab = "高级"
	for i := 0; i < 14; i++ {
		f.agents.draft.nextID++
		f.agents.draft.environment = append(f.agents.draft.environment, runtimeEnvironmentDraft{id: i + 1})
	}
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)
	r, ok := ui.Find("取消添加设备")
	if !ok || r.Y < 0 || r.Y+r.H > 600 {
		t.Fatal("modal lost close outside viewport", r, ui.Texts())
	}
	ui.Scroll(600, 300, 0, 2000)
	ui.Advance(time.Second)
	save, ok := ui.Find("保存")
	if !ok || save.Y < 0 || save.Y+save.H > 600 {
		t.Fatal("long configuration cannot scroll to Save", save)
	}
}
func TestWelcomeFinishSlidesIntoHome(t *testing.T) {
	m := welcomeModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	ui.Click("稍后设置")
	ui.Advance(80 * time.Millisecond)
	mid, ok := ui.Find("新建会话")
	ui.Advance(time.Second)
	end, _ := ui.Find("新建会话")
	if !ok || mid.X <= end.X+1 {
		t.Fatal("finish still instantly replaces page", mid, end)
	}
}
func TestWelcomeRefreshUpdatesSupportedOptionEncoding(t *testing.T) {
	m := welcomeModel()
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeAgentOptions
	f.key = ""
	catalog := setupCatalog()
	catalog[0].Installation.OptionsSupported = domain.RuntimeOptionSupport{}
	f.agents = agentEditor(catalog)
	v.sshTarget = "remote@air"
	v.sshSetup.Info = transport.WorkerInfo{DeviceID: "B", Minor: 18}
	v.recheckWelcomeAgent("", "custom")
	job := welcomeJob(t, c, "$device.check")
	body, _ := json.Marshal(app.DeviceSetupCheck{Info: v.sshSetup.Info, Catalog: setupCatalog()})
	job.Apply(m, body, nil)
	if f.agents.input().Agents[0].Options == nil {
		t.Fatal("new capability is drawn but configuration silently omitted")
	}
}

func TestWelcomeLongModelCatalogKeepsActionsVisibleWhileListScrolls(t *testing.T) {
	for _, size := range [][2]int{{1100, 800}, {850, 600}} {
		m := welcomeModel()
		v := New(m, app.NewController(""), "")
		f := v.setupFlow()
		f.step = welcomeAgentOptions
		catalog := setupCatalog()
		catalog[0].Models = nil
		for i := 1; i <= 24; i++ {
			catalog[0].Models = append(catalog[0].Models, domain.Model{ID: fmt.Sprintf("model-%d", i), Name: fmt.Sprintf("Model %d", i)})
		}
		f.agents = agentEditor(catalog)
		f.agents.edit(catalog[0])
		f.agents.tab = "模型"
		ui := n.NewTester(v.View, size[0], size[1])
		ui.Advance(time.Second)
		save, ok := ui.Find("保存")
		cancel, _ := ui.Find("取消")
		if !ok || save.Y < 0 || save.Y+save.H > float32(size[1]-24) || cancel.Y+cancel.H > float32(size[1]-24) {
			t.Fatal("long model catalog hides actions", size, save, cancel)
		}
		content, ok := ui.Find("模型列表滚动区")
		if !ok {
			t.Fatal("model list has no independent scrollport")
		}
		third, _ := ui.Find("模型：Model 3")
		fourth, _ := ui.Find("模型：Model 4")
		if math.Abs(float64(content.H-140)) > 1 || third.Y+third.H > content.Y+content.H || math.Abs(float64(fourth.H-20)) > 1 || math.Abs(float64(fourth.Y+fourth.H-content.Y-content.H)) > 1 {
			t.Fatal("catalog should show three full rows and half of the fourth", content, third, fourth)
		}
		ui.Scroll(content.X+content.W/2, content.Y+content.H/2, 0, 2400)
		ui.Advance(time.Second)
		after, _ := ui.Find("保存")
		last, _ := ui.Find("模型：Model 24")
		if math.Abs(float64(after.Y-save.Y)) > 1 || last.Y < content.Y || last.Y+last.H > content.Y+content.H+1 {
			t.Fatal("scroll moved actions or last model is unreachable", size, save, after, last, content)
		}
	}
}
