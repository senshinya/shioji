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
		before, ok := ui.Find("模型列表滚动区")
		if !ok {
			t.Fatal("missing following model list")
		}
		e.draft.login = open
		ui.Frame()
		start, _ := ui.Find("模型列表滚动区")
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
