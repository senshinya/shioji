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
	"strings"
	"testing"
	"time"
)

func signInUI(t *testing.T, key string) (*app.Model, *View, *deviceAgentEditor, *n.Tester) {
	t.Helper()
	previous := openAuthorizationURL
	openAuthorizationURL = func(string) error { return nil }
	t.Cleanup(func() { openAuthorizationURL = previous })
	m := deviceUIModel()
	m.DeviceStates[key].WorkerMinor = 20
	v := New(m, app.NewController(""), "")
	catalog := setupCatalog()
	catalog[0].Installation.SignInMethods = []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode}
	catalog[0].Account = &domain.RuntimeAccount{State: "required", Label: "未登录"}
	e := agentEditor(catalog)
	e.edit(catalog[0])
	e.draft.login = true
	ui := n.NewTester(func(c *n.Context) {
		v.frameTime = c.Now()
		v.syncAgentSignIns(c)
		n.Column(c).Width(560).Children(func() { v.agentConfiguration(c, c.Theme(), key, true, e, e.catalog[0], false, func() {}) })
	}, 850, 700)
	ui.Advance(time.Second)
	return m, v, e, ui
}
func TestAgentSignInUsesOwningWorkerAndPreservesConfigurationDraft(t *testing.T) {
	_, v, e, ui := signInUI(t, "air")
	e.draft.arguments = "--profile draft"
	if err := ui.Click("登录账号"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	if job.ConnectionID != "air" || in.Method != domain.SignInDeviceCode || in.Configuration == nil || in.Configuration.Options.Arguments[1] != "draft" {
		t.Fatal("login escaped device/configuration", job)
	}
	b, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInWaiting, Challenge: domain.SignInChallenge{URL: "https://example.test/device", Code: "ABCD-EFGH"}})
	job.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("ABCD-EFGH") || !ui.HasText("等待授权") {
		t.Fatal("device code not shown", ui.Texts())
	}
	if e.draft.arguments != "--profile draft" {
		t.Fatal("login replaced draft")
	}
	if err := ui.Click("取消登录"); err != nil {
		t.Fatal(err)
	}
	cancel := welcomeJob(t, v.Controller, "runtime.signin.cancel")
	if cancel.Input.(protocol.SignInInput).ID != in.ID {
		t.Fatal("canceled another login")
	}
}
func TestAgentSignInCompletionRefreshesMetadataWithoutReplacingDraft(t *testing.T) {
	m, v, e, ui := signInUI(t, "local")
	e.draft.arguments = "--profile keep"
	ui.Click("登录账号")
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	profile := e.catalog[0]
	profile.Account = &domain.RuntimeAccount{State: "authenticated", Label: "Profile", Subscription: "Plus"}
	profile.Models = []domain.Model{{ID: "new-model", Name: "New Model"}}
	b, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInCompleted, Profile: &profile})
	job.Apply(m, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if e.catalog[0].Account.Subscription != "Plus" || e.catalog[0].Models[0].ID != "new-model" || e.draft.arguments != "--profile keep" {
		t.Fatal("verified metadata/draft not retained", e.catalog[0], e.draft)
	}
	for _, items := range m.Details {
		for _, item := range items.Items {
			if item.Kind == "signin" {
				t.Fatal("auth inserted in conversation")
			}
		}
	}
}

func TestAgentSignInLostStartReplyQueriesSameIDWithoutOpeningAgain(t *testing.T) {
	_, v, _, ui := signInUI(t, "air")
	ui.Click("登录账号")
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	job.Apply(v.Model, nil, fmt.Errorf("connection lost"))
	ui.Frame()
	ui.Advance(1200 * time.Millisecond)
	query := welcomeJob(t, v.Controller, "runtime.signin.status")
	if query.Input.(protocol.SignInInput).ID != in.ID {
		t.Fatal("uncertain authorization replaced")
	}
	for len(v.Controller.Jobs) > 0 {
		if (<-v.Controller.Jobs).Method == "runtime.signin.start" {
			t.Fatal("unknown start replayed")
		}
	}
}
func TestAgentSignInCodeEntranceMovesFollowingFieldsContinuously(t *testing.T) {
	_, v, e, ui := signInUI(t, "air")
	before, _ := ui.Find("Custom Agent 可执行文件")
	ui.Click("登录账号")
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	b, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInWaiting, Challenge: domain.SignInChallenge{URL: "https://example.test/device", Code: "ABCD-EFGH"}})
	job.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("Custom Agent 可执行文件")
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("Custom Agent 可执行文件")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("code entered without synchronized height", before, mid, end)
	}
	e.draft.login = false
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	closing, _ := ui.Find("Custom Agent 可执行文件")
	ui.Advance(500 * time.Millisecond)
	closed, _ := ui.Find("Custom Agent 可执行文件")
	if closing.Y >= end.Y-1 || closing.Y <= closed.Y+1 {
		t.Fatal("closing account jumps following fields", end, closing, closed)
	}
}
func TestAgentSignInKnownRejectionAllowsRetry(t *testing.T) {
	_, v, _, ui := signInUI(t, "air")
	ui.Click("登录账号")
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	job.Apply(v.Model, nil, &protocol.RemoteError{Message: "already signing in"})
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("登录账号"); err != nil {
		t.Fatal("rejected authorization stuck", err, ui.Texts())
	}
	welcomeJob(t, v.Controller, "runtime.signin.start")
}

func TestAgentSignInDoesNotApplyMetadataToChangedConfiguration(t *testing.T) {
	m, v, e, ui := signInUI(t, "local")
	ui.Click("登录账号")
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	e.draft.directory = "/another/account"
	profile := e.catalog[0]
	profile.Account = &domain.RuntimeAccount{State: "authenticated", Label: "Different Account"}
	b, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInCompleted, Profile: &profile})
	job.Apply(m, b, nil)
	if e.catalog[0].Account.Label == "Different Account" {
		t.Fatal("old login replaced changed configuration's identity")
	}
}

func TestFirstRunSSHSignInKeepsPinnedPreEnrollmentDevice(t *testing.T) {
	m := welcomeModel()
	v := New(m, app.NewController(""), "")
	f := v.setupFlow()
	f.key = ""
	f.step = welcomeAgentOptions
	info := setupCatalog()[0]
	info.Installation.SignInMethods = []domain.SignInMethod{domain.SignInDeviceCode}
	f.agents = agentEditor([]domain.HarnessInfo{info})
	f.agents.edit(info)
	f.agents.setup = true
	f.agents.tab = "常规"
	f.agents.draft.login = true
	v.sshTarget = "remote@air"
	v.sshPort = "2222"
	v.sshSetup.Info = transport.WorkerInfo{DeviceID: "B", Worker: "/remote/shioji-worker", Directory: "/remote/state", Minor: 19}
	v.startAgentSignIn("", f.agents, info)
	job := welcomeJob(t, v.Controller, "$device.signin")
	in := job.Input.(app.SignInSSHDevice)
	if in.DeviceID != "B" || in.SSH.Target != "remote@air" || in.SSH.Port != 2222 || in.Input.Method != domain.SignInDeviceCode || in.Action != "start" {
		t.Fatal("login used another device", in)
	}
	if len(m.SignInIDs) != 0 {
		t.Fatal("pre-enrollment credentials persisted")
	}
}
func TestReopenedClientOnlyQueriesRememberedAuthorization(t *testing.T) {
	m := deviceUIModel()
	m.DeviceStates["air"].WorkerMinor = 19
	m.SignInIDs = map[string]map[string]string{"air": {"custom": "remembered"}}
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	job := welcomeJob(t, v.Controller, "runtime.signin.status")
	if job.ConnectionID != "air" || job.Input.(protocol.SignInInput).ID != "remembered" {
		t.Fatal("restoration did not query owning worker")
	}
}

func TestAgentSignInMessagesStayInActionRowWithoutAddingHeight(t *testing.T) {
	for _, tc := range []struct {
		phase   domain.SignInPhase
		message string
	}{
		{domain.SignInFailed, "登录未完成，请重试或重新检测账号"},
		{domain.SignInExpired, "登录已过期，请重新登录"},
	} {
		t.Run(string(tc.phase), func(t *testing.T) {
			m, v, _, ui := signInUI(t, "air")
			before, _ := ui.Find("Custom Agent 可执行文件")
			ui.Click("登录账号")
			job := welcomeJob(t, v.Controller, "runtime.signin.start")
			in := job.Input.(protocol.SignInInput)
			body, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: tc.phase, Message: tc.message})
			job.Apply(m, body, nil)
			ui.Frame()
			ui.Advance(time.Second)
			message, ok := ui.Find(tc.message)
			button, _ := ui.Find("登录账号")
			after, _ := ui.Find("Custom Agent 可执行文件")
			if !ok || message.Y < button.Y-1 || message.Y+message.H > button.Y+button.H+1 || message.X+message.W > button.X+1 {
				t.Fatal("login message is outside button row", message, button)
			}
			if math.Abs(float64(before.Y-after.Y)) > 1 {
				t.Fatal("login message adds a separate row", before, after)
			}
		})
	}
}

func TestAgentSignInMethodSelectionIsSeparateFromStarting(t *testing.T) {
	for _, key := range []string{"local", "air"} {
		t.Run(key, func(t *testing.T) {
			_, v, e, ui := signInUI(t, key)
			if err := ui.Click("选择登录方式"); err != nil {
				t.Fatal(err, ui.Texts())
			}
			ui.Advance(200 * time.Millisecond)
			choice := "验证码授权"
			want := domain.SignInDeviceCode
			if key == "air" {
				choice = "浏览器授权"
				want = domain.SignInBrowser
			}
			if err := ui.Click(choice); err != nil {
				t.Fatal(err, ui.Texts())
			}
			ui.Advance(200 * time.Millisecond)
			if e.signInMethod != want {
				t.Fatal("choice lost", e.signInMethod)
			}
			if len(v.Controller.Jobs) != 0 {
				t.Fatal("selecting a method started authorization")
			}
			if !ui.HasText("登录") {
				t.Fatal("missing independent login action", ui.Texts())
			}
			if err := ui.Click("登录账号"); err != nil {
				t.Fatal(err)
			}
			job := welcomeJob(t, v.Controller, "runtime.signin.start")
			if job.Input.(protocol.SignInInput).Method != want {
				t.Fatal("device default overrode explicit choice")
			}
		})
	}
}

func TestAgentSignInDoesNotReplaceActiveAuthorization(t *testing.T) {
	m, v, e, _ := signInUI(t, "air")
	v.startAgentSignIn("air", e, e.catalog[0])
	first := welcomeJob(t, v.Controller, "runtime.signin.start").Input.(protocol.SignInInput)
	v.startAgentSignIn("air", e, e.catalog[0])
	if len(v.Controller.Jobs) != 0 || m.SignInIDs["air"]["custom"] != first.ID {
		t.Fatal("repeat activation replaced the query identity of a running login")
	}
}

func TestLegacyWorkerKeepsRemoteBrowserUnavailableWithoutCallbackContract(t *testing.T) {
	m, _, _, ui := signInUI(t, "air")
	m.DeviceStates["air"].WorkerMinor = 19
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("选择登录方式"); ok {
		t.Fatal("legacy remote worker offered browser without callback metadata")
	}
	if !ui.HasText("验证码授权") || !ui.HasText("登录") {
		t.Fatal("legacy device-code login lost")
	}
}

func TestAgentSignInMethodControlFadesWhenAuthorizationStartsAndEnds(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			_, v, e, ui := signInUI(t, "local")
			ui.SetDark(dark)
			ui.Advance(time.Second)
			selector, ok := ui.Find("选择登录方式")
			if !ok {
				t.Fatal("missing method selector")
			}
			// Sample the top border, away from text and pointer feedback.
			x, y := int(selector.X+selector.W/2), int(selector.Y)
			idle := ui.Image().RGBAAt(x, y)
			v.startAgentSignIn("local", e, e.catalog[0])
			ui.Frame()
			ui.Advance(50 * time.Millisecond)
			leaving := ui.Image().RGBAAt(x, y)
			ui.Advance(200 * time.Millisecond)
			active := ui.Image().RGBAAt(x, y)
			if idle == active || leaving == idle || leaving == active {
				t.Fatal("method selector disappears abruptly", idle, leaving, active)
			}
			v.agentSignIn("local", e.openID).status.Phase = domain.SignInCanceled
			ui.Frame()
			ui.Advance(50 * time.Millisecond)
			entering := ui.Image().RGBAAt(x, y)
			ui.Advance(200 * time.Millisecond)
			restored := ui.Image().RGBAAt(x, y)
			if restored != idle || entering == active || entering == restored {
				t.Fatal("method selector reappears abruptly", active, entering, restored, idle)
			}
		})
	}
}

func TestAgentSignInDisclosureDoesNotJumpWhenGuidanceMoves(t *testing.T) {
	_, _, e, ui := signInUI(t, "local")
	e.catalog[0].Installation.Issue = domain.RuntimeSignInRequired
	e.draft.login = false
	ui.Frame()
	ui.Advance(time.Second)
	for _, open := range []bool{true, false} {
		before, ok := ui.Find("Custom Agent 可执行文件")
		if !ok {
			t.Fatal("missing following configuration field")
		}
		e.draft.login = open
		ui.Frame()
		start, _ := ui.Find("Custom Agent 可执行文件")
		if math.Abs(float64(start.Y-before.Y)) > 1 {
			t.Fatal("login guidance jumps at disclosure boundary", open, before, start)
		}
		ui.Advance(350 * time.Millisecond)
	}
}

func TestAgentSignInMethodAlignsWithConfigurationControlEdge(t *testing.T) {
	for _, setup := range []bool{false, true} {
		t.Run(fmt.Sprint("setup=", setup), func(t *testing.T) {
			_, _, e, ui := signInUI(t, "local")
			e.setup = setup
			e.tab = "常规"
			ui.Frame()
			ui.Advance(time.Second)
			method, ok := ui.Find("选择登录方式")
			if !ok {
				t.Fatal("missing authorization selector")
			}
			executable, ok := ui.Find(e.catalog[0].Name + " 可执行文件")
			if !ok {
				t.Fatal("missing executable field")
			}
			if math.Abs(float64(method.X-executable.X)) > 1 {
				t.Fatalf("authorization selector left edge %v differs from executable field %v", method.X, executable.X)
			}
		})
	}
}

func TestAgentSignInHasNoDuplicateGuidanceButton(t *testing.T) {
	for _, issue := range []domain.RuntimeIssue{domain.RuntimeSignInRequired, domain.RuntimeAuthenticationExpired} {
		t.Run(string(issue), func(t *testing.T) {
			_, _, e, ui := signInUI(t, "local")
			e.catalog[0].Installation.Issue = issue
			e.draft.login = false
			ui.Frame()
			ui.Advance(time.Second)
			if _, ok := ui.Find("登录账号"); ok {
				t.Fatal("duplicate login entry below configuration fields")
			}
			if err := ui.Click("管理账号"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if _, ok := ui.Find("选择登录方式"); !ok {
				t.Fatal("account disclosure lost the authorization selector")
			}
		})
	}
}

func TestAgentSignInPreservesDisclosureAfterStartingAuthorization(t *testing.T) {
	_, _, e, ui := signInUI(t, "local")
	if err := ui.Click("登录账号"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(350 * time.Millisecond)
	if !e.draft.login || !ui.HasText("取消登录") {
		t.Fatal("starting authorization changed the user's account disclosure")
	}
}

func TestSystemAPIShowsReadOnlyConfigurationWithoutLoginControls(t *testing.T) {
	_, _, e, ui := signInUI(t, "local")
	e.catalog[0].Account = &domain.RuntimeAccount{Mode: "api", State: "authenticated", Label: "Team API", Endpoint: "https://api.example/v1", Model: "custom-model", Credential: "configured"}
	ui.Frame()
	ui.Advance(time.Second)
	for _, text := range []string{"Team API", "https://api.example/v1", "custom-model", "已配置"} {
		if !ui.HasText(text) {
			t.Fatal("missing effective setting", text, ui.Texts())
		}
	}
	if _, found := ui.Find("登录账号"); found {
		t.Fatal("existing system API should not offer account replacement")
	}
}

func TestUnconfiguredAPIConnectUsesOwningWorkerWithoutManualModel(t *testing.T) {
	m, v, e, ui := signInUI(t, "air")
	m.DeviceStates["air"].WorkerMinor = 21
	e.catalog[0].Installation.APISetup = true
	e.draft.apiEndpoint = "https://api.example/v1"
	e.draft.apiCredential = "transient-secret"
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("自定义 API"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	if ui.HasText("获取模型") {
		t.Fatal("manual discovery still required")
	}
	if err := ui.Click("连接 API"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	if job.ConnectionID != "air" || in.Method != domain.SignInAPI || in.API == nil || in.API.Model != "" || in.API.Credential != "transient-secret" {
		t.Fatal("wrong connection request", in)
	}
	if e.draft.apiCredential != "transient-secret" {
		t.Fatal("failed attempts must retain transient editor draft")
	}
	body, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInCompleted, Profile: &domain.HarnessInfo{ID: in.HarnessID, State: domain.RuntimeReady, Account: &domain.RuntimeAccount{Mode: "api", State: "authenticated"}}})
	job.Apply(m, body, nil)
	if e.draft.apiCredential != "" {
		t.Fatal("confirmed success retained draft secret")
	}
	data, _ := json.Marshal(m)
	if strings.Contains(string(data), "transient-secret") {
		t.Fatal("secret persisted")
	}
}
func TestAPIConfigurationEntranceUsesActualHeightAnimation(t *testing.T) {
	m, _, e, ui := signInUI(t, "local")
	m.DeviceStates["local"].WorkerMinor = 21
	e.catalog[0].Installation.APISetup = true
	ui.Frame()
	ui.Advance(time.Second)
	before, _ := ui.Find("Agent 接入内容")
	if err := ui.Click("自定义 API"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("Agent 接入内容")
	ui.Advance(time.Second)
	end, _ := ui.Find("Agent 接入内容")
	if mid.H <= before.H+1 || mid.H >= end.H-1 {
		t.Fatal("API height jumps", before, mid, end)
	}
}

func TestInitialAccountKeepsMethodVisibleWhileWaiting(t *testing.T) {
	m, v, e, ui := signInUI(t, "local")
	m.DeviceStates["local"].WorkerMinor = 21
	e.catalog[0].Installation.APISetup = true
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("登录账号")
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	data, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInWaiting, Challenge: domain.SignInChallenge{URL: "https://example.test/auth"}})
	job.Apply(m, data, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("选择登录方式"); !ok {
		t.Fatal("method row replaced on waiting", ui.Texts())
	}
	if !ui.HasText("授权方式") {
		t.Fatal("missing aligned field label", ui.Texts())
	}
}

func TestConfiguredAccountWithNoModelsKeepsRetryAndAccount(t *testing.T) {
	_, v, e, ui := signInUI(t, "local")
	e.setup = true
	e.tab = "模型"
	e.catalog[0].State = domain.RuntimeUnavailable
	e.catalog[0].Account = &domain.RuntimeAccount{Mode: "account", State: "authenticated", Label: "Profile", Subscription: "Pro"}
	e.catalog[0].Models = nil
	e.catalog[0].Error = "模型读取失败"
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("模型读取失败") {
		t.Fatal("model failure not shown", ui.Texts())
	}
	if err := ui.Click("重新读取模型"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	// A recheck is meaningful only inside setup; its visible retry remains available
	// without repeating account authorization.
	_ = v
}

func TestAPISnapshotDoesNotReplaceActiveConnectionForm(t *testing.T) {
	m, v, e, ui := signInUI(t, "local")
	m.DeviceStates["local"].WorkerMinor = 21
	e.catalog[0].Installation.APISetup = true
	e.signInMethod = domain.SignInAPI
	e.draft.apiEndpoint = "https://api.example/v1"
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("连接 API")
	welcomeJob(t, v.Controller, "runtime.signin.start")
	e.catalog[0].Account = &domain.RuntimeAccount{Mode: "api", State: "authenticated", Label: "Custom API", Endpoint: e.draft.apiEndpoint, Model: "actual"}
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("正在连接") {
		t.Fatal("snapshot replaced active form before setup completion", ui.Texts())
	}
	if _, ok := ui.Find("连接 API"); !ok {
		t.Fatal("connection action disappeared before completion")
	}
}

func TestModelDiscoveryChangesActualConfigurationHeight(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	info := setupCatalog()[0]
	info.Models = nil
	e := agentEditor([]domain.HarnessInfo{info})
	e.edit(info)
	e.setup = true
	e.tab = "模型"
	e.workerMinor = 21
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(560).Children(func() {
			v.agentConfiguration(c, c.Theme(), "local", true, e, e.catalog[0], true, func() {})
			n.Text(c, "下方操作")
		})
	}, 700, 700)
	ui.Advance(time.Second)
	before, _ := ui.Find("下方操作")
	e.catalog[0].Models = []domain.Model{{ID: "actual-a", Name: "Actual A"}, {ID: "actual-b", Name: "Actual B"}, {ID: "actual-c", Name: "Actual C"}}
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	mid, _ := ui.Find("下方操作")
	ui.Advance(time.Second)
	end, _ := ui.Find("下方操作")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("discovered catalog height jumps", before, mid, end)
	}
}

func TestAPIModeMovesEntireGeneralForm(t *testing.T) {
	m, _, e, ui := signInUI(t, "local")
	m.DeviceStates["local"].WorkerMinor = 21
	e.catalog[0].Installation.APISetup = true
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("自定义 API")
	ui.Advance(80 * time.Millisecond)
	mid, _ := ui.Find("Custom Agent 可执行文件")
	ui.Advance(time.Second)
	end, _ := ui.Find("Custom Agent 可执行文件")
	if mid.X <= end.X+1 {
		t.Fatal("mode change leaves the rest of the form stationary", mid, end)
	}
}

func TestAgentManualAuthorizationUsesTheOriginalDeviceReceipt(t *testing.T) {
	m, v, _, ui := signInUI(t, "air")
	m.DeviceStates["air"].WorkerMinor = protocol.SignInManualCodeMinor
	feedbackClick(t, ui, "登录账号")
	started := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := started.Input.(protocol.SignInInput)
	raw, _ := json.Marshal(domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInWaiting,
		Challenge: domain.SignInChallenge{URL: "https://accounts.google.com/o/oauth2/v2/auth", ManualCode: true}})
	started.Apply(m, raw, nil)
	ui.Frame()
	feedbackClick(t, ui, "Google 授权码")
	ui.Type("fixture-auth-code")
	feedbackClick(t, ui, "提交授权")
	code := welcomeJob(t, v.Controller, "runtime.signin.code")
	forwarded := code.Input.(protocol.SignInInput)
	if code.ConnectionID != "air" || forwarded.ID != in.ID || forwarded.HarnessID != in.HarnessID ||
		forwarded.AuthorizationCode != "fixture-auth-code" || v.agentSignIn("air", in.HarnessID).manualCode != "" {
		t.Fatal("authorization response escaped the attempt or stayed in the form")
	}
}

func TestCompletedManualAuthorizationReplacesOlderWorkerVerificationMessage(t *testing.T) {
	m, v, e, ui := signInUI(t, "local")
	feedbackClick(t, ui, "登录账号")
	job := welcomeJob(t, v.Controller, "runtime.signin.start")
	in := job.Input.(protocol.SignInInput)
	profile := e.catalog[0]
	profile.Account = &domain.RuntimeAccount{Label: "Antigravity", State: "authenticated"}
	message := "已提交授权信息，正在核对账号"
	body, _ := json.Marshal(domain.SignInStatus{
		ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInCompleted, Profile: &profile, Message: message,
	})
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText(message) || !ui.HasText("已登录") || !ui.HasText("重新登录") {
		t.Fatal("completed authorization still looks pending", ui.Texts())
	}
}

func TestAgentQuotaDetailsAreCollapsedAndUseCompactRowsWhenOpened(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = protocol.SignInManualCodeMinor
	v := New(m, app.NewController(""), "")
	info := domain.HarnessInfo{
		ID: "antigravity", Name: "Antigravity", Icon: "antigravity", State: domain.RuntimeReady,
		Installation: &domain.RuntimeInstallation{SignInMethods: []domain.SignInMethod{domain.SignInBrowser}},
		Capabilities: domain.HarnessCapabilities{SkillManagement: true, ToolServices: true, HookManagement: true},
	}
	reset := time.Date(2026, 11, 30, 17, 27, 0, 0, time.Local)
	info.Account = &domain.RuntimeAccount{Label: "Antigravity", State: "authenticated", QuotaWindows: []domain.RuntimeQuotaWindow{
		{Group: "Gemini Models", DurationSeconds: 604800, UsedPercent: 1, ResetsAt: &reset},
		{Group: "Gemini Models", DurationSeconds: 18000, UsedPercent: 0, ResetsAt: &reset},
		{Group: "Claude and GPT models", DurationSeconds: 604800, UsedPercent: 0, ResetsAt: &reset},
		{Group: "Claude and GPT models", DurationSeconds: 18000, UsedPercent: 0, ResetsAt: &reset},
		{Group: "Gemini 4 Argon", DurationSeconds: 604800, UsedPercent: 0, ResetsAt: &reset},
	}}
	e := agentEditor([]domain.HarnessInfo{info})
	e.edit(info)
	e.draft.login = true
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).FillWidth().FillHeight().Background(c.Theme().Surface).Children(func() {
			v.deviceAgentPage(c, c.Theme(), "local", "本机", state, e, info)
		})
	}, 950, 670)
	ui.SetDark(true)
	ui.Advance(time.Second)
	const title = "Claude and GPT models · 5 小时额度"
	const usage = "已用 0% · 11-30 17:27 重置"
	if ui.HasText(title) || ui.HasText(usage) {
		t.Fatal("quota details are visible by default")
	}
	auditCapture(t, ui, "agy-quota-collapsed")
	if err := ui.Click("额度"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	auditCapture(t, ui, "agy-quota-expanded")
	ui.SetSize(550, 630)
	ui.SetDark(false)
	ui.Advance(time.Second)
	name, hasName := ui.Find(title)
	last, hasLast := ui.Find("Gemini 4 Argon · 每周额度")
	if !hasName || !hasLast || name.W < 200 || name.H > 24 || last.Y-name.Y > 30 {
		t.Fatalf("quota names should fit in adjacent compact rows: name=%+v next=%+v", name, last)
	}
	auditCapture(t, ui, "agy-quota-expanded-narrow")
	if err := ui.Click("额度"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText(title) || ui.HasText(usage) {
		t.Fatal("quota details remain visible after collapsing")
	}
}

func TestAgentReopenShowsAuthenticatedAccountAndQuotaWithoutSignInAttempt(t *testing.T) {
	for _, stateName := range []string{"authenticated", "configured"} {
		t.Run(stateName, func(t *testing.T) {
			m := deviceUIModel()
			state := m.DeviceStates["local"]
			state.WorkerMinor = protocol.SignInManualCodeMinor
			v := New(m, app.NewController(""), "")
			reset := time.Date(2026, 11, 30, 17, 27, 0, 0, time.Local)
			info := domain.HarnessInfo{
				ID: "antigravity", Name: "Antigravity", Icon: "antigravity", State: domain.RuntimeReady,
				Account: &domain.RuntimeAccount{
					Mode: "account", Label: "Antigravity", State: stateName,
					QuotaWindows: []domain.RuntimeQuotaWindow{
						{Group: "Gemini Models", DurationSeconds: 18000, UsedPercent: 12, ResetsAt: &reset},
					},
				},
				Installation: &domain.RuntimeInstallation{SignInMethods: []domain.SignInMethod{domain.SignInBrowser}},
			}
			e := agentEditor([]domain.HarnessInfo{info})
			e.edit(info)
			e.draft.login = true
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).FillWidth().FillHeight().Background(c.Theme().Surface).Children(func() {
					v.deviceAgentPage(c, c.Theme(), "local", "本机", state, e, info)
				})
			}, 950, 670)
			ui.Advance(time.Second)
			if !ui.HasText("已登录") || !ui.HasText("重新登录") || !ui.HasText("额度") {
				t.Fatalf("reopened agent view lost login or quota for state=%q: %v", stateName, ui.Texts())
			}
		})
	}
}
