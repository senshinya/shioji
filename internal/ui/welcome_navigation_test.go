package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"testing"
	"time"
)

func TestWelcomeSharedActionsFollowChangingPageHeightTogether(t *testing.T) {
	v := New(welcomeNetworkModel(), app.NewController(""), "")
	v.beginTailnet()
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.Click("选择 Tailnet 设备：MacBook Air")
	before, _ := ui.Find("返回")
	ui.Click("继续")
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("返回")
	next, _ := ui.Find("连接并准备服务")
	ui.Advance(time.Second)
	end, _ := ui.Find("返回")
	if mid.Y <= end.Y+1 || mid.Y >= before.Y-1 || math.Abs(float64(mid.Y+mid.H/2-next.Y-next.H/2)) > 1 {
		t.Fatal("shared actions jump or animate separately from the page height", before, mid, next, end)
	}
}

func TestWelcomeEntryNavigatesWithoutSelectionOrFooter(t *testing.T) {
	m := welcomeModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	if !ui.HasText("选择执行位置") {
		t.Fatal("entry still mixes selection and configuration", ui.Texts())
	}
	for _, label := range []string{"继续", "稍后设置", "配置本机"} {
		if _, ok := ui.Find(label); ok {
			t.Fatal("entry has an action that applies to only one route", label)
		}
	}
	if err := ui.Click("这台 Mac"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.welcome.step != welcomeAgents || v.welcome.key != "local" || !ui.HasText("选择 Agent") {
		t.Fatal("local row is not direct navigation", ui.Texts())
	}
	if m.SessionID != "" || m.Drafts["home"] != "保留首页草稿" {
		t.Fatal("entry created a session or consumed the ordinary draft")
	}
}

func welcomeNetworkModel() *app.Model {
	m := welcomeModel()
	m.Tailnet = tailnet.Snapshot{State: "Running", Network: "example", Peers: []tailnet.Peer{
		{ID: "peer-air", Name: "MacBook Air", Address: "100.64.0.1", OS: "macOS", Online: true},
		{ID: "peer-studio", Name: "Mac Studio", Address: "100.64.0.2", OS: "macOS", Online: true},
	}}
	return m
}

func TestWelcomeTailnetSSHReturnsToSelectedPeer(t *testing.T) {
	m := welcomeNetworkModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.step, f.key, f.route = welcomeSSH, "", welcomeRouteTailnet
	v.sshTailnetPeer, v.sshTarget, v.sshName, v.sshPort, v.sshCheck = "peer-air", "review@100.64.0.1", "Review Air", "8022", sshEditing
	v.networkUI().selected = "peer-air"
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.Click("返回")
	ui.Advance(time.Second)
	if f.step != welcomeTailnetPeers || v.networkUI().selected != "peer-air" {
		t.Fatal("Back lost the Tailnet peer list", f.step)
	}
	if err := ui.Click("继续"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.sshTarget != "review@100.64.0.1" || v.sshName != "Review Air" || v.sshPort != "8022" {
		t.Fatal("returning from the peer list overwrote the connection draft", v.sshTarget, v.sshName, v.sshPort)
	}
}

func TestWelcomeSSHAndTailnetKeepSeparateDrafts(t *testing.T) {
	m := welcomeNetworkModel()
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.Click("通过 SSH 连接")
	ui.Advance(time.Second)
	v.sshTarget, v.sshName, v.sshPort = "direct@192.168.7.119", "Direct Air", "2222"
	ui.Frame()
	ui.Click("返回")
	ui.Advance(time.Second)
	ui.Click("从 Tailnet 添加")
	ui.Advance(time.Second)
	ui.Click("选择 Tailnet 设备：MacBook Air")
	if err := ui.Click("继续"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	for len(c.Jobs) > 0 {
		if job := <-c.Jobs; job.Method == "$device.check" || job.Method == "$device.complete" {
			t.Fatal("selecting a peer started a connection", job.Method)
		}
	}
	v.sshTarget, v.sshName, v.sshPort = "tail@100.64.0.1", "Tail Air", "8022"
	ui.Frame()
	ui.Click("返回")
	ui.Advance(time.Second)
	ui.Click("返回")
	ui.Advance(time.Second)
	ui.Click("通过 SSH 连接")
	ui.Advance(time.Second)
	if v.sshTailnetPeer != "" || v.sshTarget != "direct@192.168.7.119" || v.sshName != "Direct Air" || v.sshPort != "2222" {
		t.Fatal("Tailnet overwrote direct SSH", v.sshTailnetPeer, v.sshTarget, v.sshName, v.sshPort)
	}
	ui.Click("返回")
	ui.Advance(time.Second)
	ui.Click("从 Tailnet 添加")
	ui.Advance(time.Second)
	ui.Click("继续")
	ui.Advance(time.Second)
	if v.sshTarget != "tail@100.64.0.1" || v.sshName != "Tail Air" || v.sshPort != "8022" {
		t.Fatal("Tailnet peer draft was discarded")
	}
	ui.Click("返回")
	ui.Advance(time.Second)
	ui.Click("选择 Tailnet 设备：Mac Studio")
	ui.Click("继续")
	ui.Advance(time.Second)
	if v.sshTarget != "100.64.0.2" || v.sshName != "Mac Studio" || v.sshPort != "" {
		t.Fatal("a different peer inherited the previous peer's SSH draft")
	}
}

func TestWelcomeAdmittedRemoteAgentBackStillUsesSSH(t *testing.T) {
	m := welcomeNetworkModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.step, f.key, f.route = welcomeAgents, "air", welcomeRouteTailnet
	f.agents = agentEditor(setupCatalog())
	v.sshTailnetPeer, v.sshTarget, v.sshName = "peer-air", "remote@100.64.0.1", "MacBook Air"
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.Click("返回")
	ui.Advance(time.Second)
	if f.step != welcomeSSH || f.key != "" || v.sshTailnetPeer != "peer-air" {
		t.Fatal("an already-admitted remote device returned to local setup", f.step, f.key)
	}
}

func TestWelcomeRemoteRecheckKeepsOnlySameDeviceAgentDrafts(t *testing.T) {
	m := welcomeModel()
	m.Connections = nil // This scenario is an unadmitted device; saved devices now merge automatically.
	v := New(m, nil, "")
	f := v.setupFlow()
	f.key, f.route, f.step = "", welcomeRouteSSH, welcomeSSH
	result := app.DeviceSetupCheck{Info: transport.WorkerInfo{DeviceID: "B", Minor: 18}, Catalog: setupCatalog()}
	v.finishSSHSetup(result)
	f.agents.paths["custom"] = "/remote/edited-agent"
	f.agents.catalog[0].Configured = false
	v.welcomeStep(welcomeSSH, -1)
	// Each device check arrives as a separately decoded response.
	result.Catalog = setupCatalog()
	v.finishSSHSetup(result)
	if f.agents.paths["custom"] != "/remote/edited-agent" || f.agents.catalog[0].Configured {
		t.Fatal("a fresh check of the same device discarded the user's Agent draft")
	}
	v.welcomeStep(welcomeSSH, -1)
	result.Info.DeviceID = "C"
	result.Catalog = setupCatalog()
	v.finishSSHSetup(result)
	if f.agents.paths["custom"] == "/remote/edited-agent" || !f.agents.catalog[0].Configured {
		t.Fatal("another device inherited an Agent draft")
	}
}

func TestWelcomeWorkspaceDraftsStayOnOwningDevice(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.key, f.step = "local", welcomeWorkspace
	f.name, f.directory = "Local project", "/Users/local/project"
	f.directories = []welcomeDirectory{{id: 1, path: "/Users/local/shared"}}
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	f.key = "air"
	ui.Frame()
	ui.Advance(time.Second)
	if f.directory != "" || len(f.directories) != 0 || f.workspace.ID != "" {
		t.Fatal("remote workspace inherited controller Mac directories", f.directory)
	}
	f.name, f.directory = "Remote project", "/Users/remote/project"
	f.key = "local"
	ui.Frame()
	ui.Advance(time.Second)
	if f.name != "Local project" || f.directory != "/Users/local/project" || len(f.directories) != 1 || f.directories[0].path != "/Users/local/shared" {
		t.Fatal("returning to the owning device lost its workspace draft", f.name, f.directory)
	}
}

func TestWelcomeAgentNameOpensConfigurationAndBackKeepsSavedDraft(t *testing.T) {
	m := welcomeModel()
	m.DeviceStates["local"].Readiness.Harnesses = setupCatalog()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	if err := ui.Click("这台 Mac"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("Custom Agent"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.welcome.step != welcomeAgentOptions || ui.HasText("配置 Custom Agent") {
		t.Fatal("Agent name is not the configuration entry", ui.Texts())
	}
	ui.Click("高级")
	ui.Advance(time.Second)
	ui.Click("启动参数输入")
	ui.Type("--profile keep")
	ui.Click("保存")
	ui.Advance(time.Second)
	ui.Click("返回")
	ui.Advance(time.Second)
	ui.Click("这台 Mac")
	ui.Advance(time.Second)
	ui.Click("Custom Agent")
	ui.Advance(time.Second)
	if v.welcome.agents.draft.arguments != "--profile keep" {
		t.Fatal("returning through entry discarded configured Agent draft", v.welcome.agents.draft.arguments)
	}
}

func TestWelcomeTitleBackKeepsUnsavedAgentDraft(t *testing.T) {
	m := welcomeModel()
	v := New(m, nil, "")
	f := v.setupFlow()
	f.step, f.key = welcomeAgents, "local"
	catalog := setupCatalog()
	other := setupCatalog()[0]
	other.ID, other.Name = "other", "Other Agent"
	f.agents = agentEditor(append(catalog, other))
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.Click("配置 Custom Agent")
	ui.Advance(time.Second)
	f.agents.path, f.agents.draft.arguments = "/unsaved/agent", "--profile 'unfinished"
	ui.Frame()
	ui.Click("返回 Agent 列表")
	ui.Advance(time.Second)
	ui.Click("配置 Other Agent")
	ui.Advance(time.Second)
	ui.Click("返回 Agent 列表")
	ui.Advance(time.Second)
	ui.Click("配置 Custom Agent")
	ui.Advance(time.Second)
	if f.agents.path != "/unsaved/agent" || f.agents.draft.arguments != "--profile 'unfinished" {
		t.Fatal("title Back discarded the unsaved draft", f.agents.path, f.agents.draft.arguments)
	}
}

func TestWelcomeOfflineAgentDoesNotQueueRuntimeChecks(t *testing.T) {
	m := welcomeModel()
	m.DeviceStates["local"].Connected = false
	m.DeviceStates["local"].Readiness.Harnesses = setupCatalog()
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.Click("这台 Mac")
	ui.Advance(time.Second)
	ui.Click("配置 Custom Agent")
	ui.Advance(time.Second)
	ui.Click("模型")
	ui.Advance(time.Second)
	ui.Click("刷新")
	v.recheckWelcomeAgent("local", "custom")
	for len(c.Jobs) > 0 {
		if job := <-c.Jobs; job.Method == "runtime.check" || job.Method == "runtime.catalog" {
			t.Fatal("offline welcome queued a runtime operation", job.Method)
		}
	}
}

func TestWelcomeLocalConnectionArrivalRefreshesCurrentPage(t *testing.T) {
	m := welcomeModel()
	m.DeviceStates["local"].Connected = false
	m.DeviceStates["local"].ConnectionEpoch = ""
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	ui.Click("这台 Mac")
	ui.Advance(time.Second)
	m.DeviceStates["local"].Connected, m.DeviceStates["local"].ConnectionEpoch = true, "connected"
	m.DeviceStates["local"].Readiness.Harnesses = setupCatalog()
	ui.Frame()
	ui.Advance(time.Second)
	job := welcomeJob(t, c, "runtime.catalog")
	if job.ConnectionEpoch != "connected" {
		t.Fatal("catalog refresh used stale epoch", job.ConnectionEpoch)
	}
	body, _ := json.Marshal(domain.Readiness{Harnesses: setupCatalog()})
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if v.welcome.step != welcomeAgents || !ui.HasText("Custom Agent") || v.welcome.agents.epoch != "connected" {
		t.Fatal("worker connection did not refresh the already-open Agent page", ui.Texts())
	}
}

func TestWelcomeRemoteDirectoryWaitsForAdmittedConnection(t *testing.T) {
	m := welcomeModel()
	m.DeviceStates["air"].Connected = false
	m.DeviceStates["air"].ConnectionEpoch = ""
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step, f.key = welcomeWorkspace, "air"
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	ui.Click("选择主目录")
	ui.Frame()
	if v.directoryPicker != nil {
		t.Fatal("directory picker captured a not-yet-connected device epoch")
	}
	ui.Click("添加文件夹")
	ui.Frame()
	if v.directoryPicker != nil {
		t.Fatal("additional directory picker opened before connection was ready")
	}
	m.DeviceStates["air"].Connected = true
	m.DeviceStates["air"].ConnectionEpoch = "ready"
	m.DeviceStates["air"].WorkerMinor = 30
	ui.Frame()
	ui.Click("选择主目录")
	ui.Frame()
	job := welcomeJob(t, c, "device.directories")
	if job.ConnectionID != "air" || job.ConnectionEpoch != "ready" || v.directoryPicker == nil {
		t.Fatal("ready connection did not unlock its owning directory picker", job)
	}
}

func TestWelcomeDirectSSHDoesNotConnectBeforeSubmission(t *testing.T) {
	m := welcomeModel()
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	if err := ui.Click("通过 SSH 连接"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.welcome.step != welcomeSSH || v.sshTailnetPeer != "" || v.sshCheck != sshEditing {
		t.Fatal("direct SSH did not enter the common preparation form")
	}
	for len(c.Jobs) > 0 {
		if job := <-c.Jobs; job.Method == "$device.check" || job.Method == "$device.complete" {
			t.Fatal("entry initiated a connection", job.Method)
		}
	}
}
