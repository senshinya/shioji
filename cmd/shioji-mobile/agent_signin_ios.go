package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

type mobileAgentSignIn struct {
	status     domain.SignInStatus
	method     domain.SignInMethod
	pending    bool
	message    string
	timer      *time.Timer
	callback   *transport.LoopbackForward
	stop       context.CancelFunc
	opening    bool
	route      string
	observed   bool
	manualCode string
}

func (a *mobileApp) retireAgentSignIns() {
	for _, attempt := range a.agentSignIns {
		attempt.closeCallback()
		if attempt.timer != nil {
			attempt.timer.Stop()
		}
	}
	a.agentSignIns = nil
}

func (p *mobileAgentSignIn) closeCallback() {
	if p.stop != nil {
		p.stop()
	}
	p.stop, p.callback, p.route, p.opening = nil, nil, "", false
}

func (a *mobileApp) keepAgentSignIn(attempt *mobileAgentSignIn) {
	if a.agentSignIns == nil {
		a.agentSignIns = map[string]*mobileAgentSignIn{}
	}
	a.agentSignIns[attempt.status.HarnessID] = attempt
}

// Restore opaque operation IDs for the selected device. Only an explicit
// start action submits authorization or configuration again.
func (a *mobileApp) resumeAgentSignIns() {
	if a.preview || !a.model.Connected {
		return
	}
	references := a.model.SignInIDs[a.endpoint.DeviceID]
	for harness, id := range references {
		attempt := a.agentSignIns[harness]
		if attempt == nil {
			attempt = &mobileAgentSignIn{status: domain.SignInStatus{ID: id, HarnessID: harness, Phase: domain.SignInStarting}}
			a.keepAgentSignIn(attempt)
		}
		if a.model.WorkerMinor < 19 {
			attempt.closeCallback()
			attempt.status.Phase = domain.SignInExpired
			attempt.status.Challenge = domain.SignInChallenge{}
			attempt.message = "更新执行服务后重新登录"
			delete(references, harness)
			continue
		}
		a.pollAgentSignIn(attempt)
	}
}

func (a *mobileApp) pollAgentSignIn(attempt *mobileAgentSignIn) {
	if a.preview || !a.model.Connected || attempt.pending || attempt.timer != nil || !attempt.status.Phase.Active() {
		return
	}
	attempt.timer = time.AfterFunc(time.Second, func() {
		mygo.RunOnMain(func() {
			attempt.timer = nil
			if a.agentSignIns[attempt.status.HarnessID] == attempt && a.model.Connected {
				a.sendAgentSignIn(attempt, "status", protocol.SignInInput{ID: attempt.status.ID, HarnessID: attempt.status.HarnessID})
			}
		})
	})
}

func (a *mobileApp) startAgentSignIn(agent domain.HarnessInfo, method domain.SignInMethod, api *domain.APIConfiguration) bool {
	prior := a.agentSignIns[agent.ID]
	if prior != nil && prior.status.Phase.Active() || !slices.Contains(agentMobileSignInMethods(agent, a.model.WorkerMinor), method) {
		return false
	}
	attempt := &mobileAgentSignIn{method: method, status: domain.SignInStatus{ID: domain.NewID(), HarnessID: agent.ID, Phase: domain.SignInStarting}}
	a.keepAgentSignIn(attempt)
	if a.preview {
		switch method {
		case domain.SignInAPI:
			attempt.status = previewAgentSignInResult(attempt.status, api)
		case domain.SignInBrowser:
			attempt.status.Phase = domain.SignInWaiting
			attempt.status.Challenge = domain.SignInChallenge{URL: "https://127.0.0.1:9/authorize", Callback: &domain.LoopbackEndpoint{Host: "localhost", Port: 1455}}
		default:
			attempt.status.Phase = domain.SignInWaiting
			attempt.status.Challenge = domain.SignInChallenge{URL: "https://127.0.0.1:9/device", Code: "ABCD-EFGH"}
		}
		a.refresh()
		return true
	}
	device := a.endpoint.DeviceID
	if device == "" || a.stateWriter == nil {
		attempt.status.Phase, attempt.message = domain.SignInFailed, "无法保存该设备的登录状态"
		return false
	}
	if a.model.SignInIDs == nil {
		a.model.SignInIDs = map[string]map[string]string{}
	}
	if a.model.SignInIDs[device] == nil {
		a.model.SignInIDs[device] = map[string]string{}
	}
	a.model.SignInIDs[device][agent.ID] = attempt.status.ID
	a.save()
	// The adapter uses the configuration already saved on the owning device.
	// Public catalog data never needs to reconstruct arguments or private options.
	return a.sendAgentSignIn(attempt, "start", protocol.SignInInput{ID: attempt.status.ID, HarnessID: agent.ID, Method: method, API: api})
}

func (a *mobileApp) sendAgentSignIn(attempt *mobileAgentSignIn, action string, input protocol.SignInInput) bool {
	if attempt.pending || !attempt.status.Phase.Active() {
		return false
	}
	if attempt.timer != nil {
		attempt.timer.Stop()
		attempt.timer = nil
	}
	if a.preview {
		if action == "cancel" {
			attempt.status.Phase = domain.SignInCanceled
			attempt.status.Challenge = domain.SignInChallenge{}
		} else {
			attempt.status = previewAgentSignInResult(attempt.status, nil)
		}
		a.refresh()
		return true
	}
	owner, model, device := a.controller, a.model, a.endpoint.DeviceID
	reference := protocol.SignInInput{ID: input.ID, HarnessID: input.HarnessID}
	job := app.Job{Method: "runtime.signin." + action, Input: input, ConnectionID: device, ConnectionEpoch: model.ConnectionEpoch}
	if action == "start" {
		job.Prepare = a.stateWriter.FlushLatest
	}
	job.Apply = func(_ *app.Model, raw json.RawMessage, err error) {
		if a.controller != owner || a.model != model || a.endpoint.DeviceID != device || a.agentSignIns[reference.HarnessID] != attempt || model.SignInIDs[device][reference.HarnessID] != reference.ID {
			return
		}
		attempt.pending = false
		var status domain.SignInStatus
		var rejected *protocol.RemoteError
		switch {
		case err == nil && json.Unmarshal(raw, &status) == nil && validSignInReceipt(reference, status):
			attempt.status = status
			if !status.Challenge.ManualCode {
				attempt.manualCode = ""
			}
			attempt.message = ""
		case action == "start" && errors.As(err, &rejected):
			attempt.status.Phase = domain.SignInFailed
			attempt.message = rejected.Message
		case action == "code" && errors.As(err, &rejected):
			attempt.message = rejected.Message
		case action == "cancel":
			attempt.message = "取消尚未确认，连接恢复后可重试"
		default:
			attempt.message = "等待核对登录结果"
		}
		if attempt.status.Phase.Active() {
			a.pollAgentSignIn(attempt)
		} else {
			attempt.closeCallback()
			delete(model.SignInIDs[device], reference.HarnessID)
			a.save()
		}
		a.refresh()
	}
	attempt.pending = true
	if owner != nil && model.Connected && owner.Do(job) {
		a.refresh()
		return true
	}
	attempt.pending = false
	attempt.message = "连接执行设备后重试"
	if action == "start" {
		attempt.status.Phase = domain.SignInFailed
		delete(model.SignInIDs[device], input.HarnessID)
		a.save()
	} else {
		a.pollAgentSignIn(attempt)
	}
	a.refresh()
	return false
}

func validSignInReceipt(input protocol.SignInInput, status domain.SignInStatus) bool {
	if status.ID != input.ID || status.HarnessID != input.HarnessID || status.Profile != nil && status.Profile.ID != input.HarnessID {
		return false
	}
	switch status.Phase {
	case domain.SignInStarting, domain.SignInWaiting, domain.SignInFailed, domain.SignInCanceled, domain.SignInExpired:
		return true
	case domain.SignInCompleted:
		return status.Profile != nil
	}
	return false
}

func (a *mobileApp) openAgentAuthorization(w *mygo.Window, attempt *mobileAgentSignIn) {
	challenge := attempt.status.Challenge
	if attempt.status.Phase != domain.SignInWaiting || attempt.opening || !agentAuthorizationURL(challenge.URL) {
		return
	}
	open := func() {
		if a.pageCurrent(w) {
			a.callUIKit("openAgentAuthorization", map[string]any{"window": w.NativeHandle(), "url": challenge.URL}, func(err error) {
				if err != nil && a.agentSignIns[attempt.status.HarnessID] == attempt {
					attempt.message = "无法打开授权页，请重试"
					a.refresh()
				}
			})
		}
	}
	if challenge.Callback == nil || a.preview {
		open()
		return
	}
	endpoint := a.activeEndpoint()
	routeID := endpoint.RouteID()
	if attempt.callback != nil && attempt.route == routeID {
		open()
		return
	}
	if endpoint.DeviceID == "" || endpoint.DeviceID != a.endpoint.DeviceID || !a.model.Connected || !challenge.Callback.Valid() {
		attempt.message = "连接该设备后再打开授权页"
		a.refresh()
		return
	}
	signer, err := a.identity()
	if err != nil {
		attempt.message = "无法读取设备的 SSH 密钥"
		a.refresh()
		return
	}
	previous := attempt.callback
	attempt.closeCallback()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	attempt.stop, attempt.opening, attempt.message = cancel, true, ""
	route := a.routeDial()
	go func() {
		if previous != nil {
			_ = previous.Close()
		}
		forward, err := endpoint.ForwardAgentCallback(ctx, signer, route, *challenge.Callback)
		if err == nil {
			err = forward.Check(ctx)
		}
		mygo.RunOnMain(func() {
			if ctx.Err() != nil || a.agentSignIns[attempt.status.HarnessID] != attempt || attempt.status.Phase != domain.SignInWaiting || attempt.status.Challenge.URL != challenge.URL {
				cancel()
				return
			}
			attempt.opening = false
			if err != nil || a.activeEndpoint().RouteID() != routeID {
				attempt.closeCallback()
				attempt.message = "无法连接登录回调，请重试或改用验证码登录"
				a.refresh()
				return
			}
			attempt.callback, attempt.route = forward, routeID
			open()
		})
	}()
	a.refresh()
}

func agentAuthorizationURL(target string) bool {
	parsed, err := url.Parse(target)
	return err == nil && len(target) <= 16<<10 && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil
}

func agentMobileSignInMethods(agent domain.HarnessInfo, minor int) []domain.SignInMethod {
	install := agent.Installation
	if minor < 19 || install == nil || agent.Account != nil && agent.Account.Mode == "api" {
		return nil
	}
	var methods []domain.SignInMethod
	if slices.Contains(install.SignInMethods, domain.SignInDeviceCode) {
		methods = append(methods, domain.SignInDeviceCode)
	}
	if minor >= 20 && slices.Contains(install.SignInMethods, domain.SignInBrowser) {
		methods = append(methods, domain.SignInBrowser)
	}
	if minor >= 21 && install.APISetup && (agent.Account == nil && install.Issue == domain.RuntimeSignInRequired || agent.Account != nil && agent.Account.State == "required") {
		methods = append(methods, domain.SignInAPI)
	}
	return methods
}

func (a *mobileApp) signedInAgent(agent domain.HarnessInfo) domain.HarnessInfo {
	attempt := a.agentSignIns[agent.ID]
	if attempt != nil && attempt.status.Phase == domain.SignInCompleted && attempt.status.Profile != nil {
		profile := *attempt.status.Profile
		for _, known := range a.model.Readiness.Harnesses {
			if known.ID != profile.ID {
				continue
			}
			if profile.Account != nil && known.Account != nil && *profile.Account == *known.Account && known.State == profile.State {
				attempt.observed = true
			}
			if attempt.observed {
				return known
			}
			break
		}
		profile.Configured = agent.Configured
		return profile
	}
	return agent
}

func agentAccountName(account *domain.RuntimeAccount) string {
	if account != nil && account.Label != "" {
		return account.Label
	}
	return "尚未登录"
}

func (a *mobileApp) agentSignInProgress(c *ui.Context, w *mygo.Window, p *mobileAgentSignIn) {
	ui.Column(c.Key("agent-sign-in-status")).Clip().Transition(mobilePanelTransition).Children(func() {
		if p == nil {
			return
		}
		message := p.message
		if message == "" {
			message = p.status.Message
		}
		if message == "" {
			message = map[domain.SignInPhase]string{domain.SignInStarting: "正在准备登录", domain.SignInWaiting: "等待授权", domain.SignInCanceled: "已取消登录", domain.SignInFailed: "登录未完成", domain.SignInExpired: "登录已过期"}[p.status.Phase]
			if p.method == domain.SignInAPI && p.status.Phase.Active() {
				message = "正在连接"
			}
		}
		if message != "" {
			mobileMessage(c, message)
		}
		if !p.status.Phase.Active() {
			return
		}
		challenge := p.status.Challenge
		if challenge.Code != "" {
			mobileDetailRow(c, challenge.Code, "", "验证码", nil, nil, func() {
				action(c, "复制验证码", func() { mygo.Clipboard.WriteText(challenge.Code) })
			})
		}
		input := protocol.SignInInput{ID: p.status.ID, HarnessID: p.status.HarnessID}
		if challenge.ManualCode {
			formInput(c, "Google 授权码", "粘贴授权码或回调地址", &p.manualCode).Password().Disabled(p.pending || !a.model.Connected)
			action(c, "提交授权", func() {
				codeInput := input
				codeInput.AuthorizationCode = p.manualCode
				if a.sendAgentSignIn(p, "code", codeInput) {
					p.manualCode = ""
				}
			}).Disabled(p.pending || !a.model.Connected || strings.TrimSpace(p.manualCode) == "")
		}
		ui.Row(c).Wrap().Gap(4).Children(func() {
			if agentAuthorizationURL(challenge.URL) {
				action(c, "打开授权页", func() { a.openAgentAuthorization(w, p) }).Disabled(p.opening || !a.model.Connected)
			}
			action(c, "检查授权结果", func() { a.sendAgentSignIn(p, "status", input) }).Disabled(p.pending || !a.model.Connected)
			if p.method != domain.SignInAPI && (p.method != "" || challenge.URL != "") {
				action(c, "取消登录", func() { a.sendAgentSignIn(p, "cancel", input) }).Disabled(p.pending || !a.model.Connected)
			}
		})
	})
}

type mobileAgentAccountDraft struct {
	method               domain.SignInMethod
	endpoint, credential string
	reauthorize          bool
}

func (d *mobileAgentAccountDraft) selected(methods []domain.SignInMethod) domain.SignInMethod {
	if slices.Contains(methods, d.method) {
		return d.method
	}
	if len(methods) == 0 {
		return ""
	}
	return methods[0]
}

func (a *mobileApp) connectAgentAPI(agent domain.HarnessInfo, draft *mobileAgentAccountDraft) {
	if endpoint := strings.TrimSpace(draft.endpoint); endpoint != "" && a.startAgentSignIn(agent, domain.SignInAPI,
		&domain.APIConfiguration{Endpoint: endpoint, Credential: draft.credential}) {
		draft.credential = ""
	}
}

func (a *mobileApp) agentAccountView(c *ui.Context, w *mygo.Window, agent domain.HarnessInfo, draft *mobileAgentAccountDraft, blocked bool, refresh func()) {
	attempt := a.agentSignIns[agent.ID]
	active := attempt != nil && attempt.status.Phase.Active()
	methods := agentMobileSignInMethods(agent, a.model.WorkerMinor)
	if agent.Account != nil && !active && !draft.reauthorize {
		for i, window := range agent.Account.QuotaWindows {
			title, value := app.RuntimeQuotaLabels(window)
			mobileRow(c.Key(i), title, "", value, nil, nil)
		}
	}
	if account := agent.Account; account != nil && account.Mode != "api" && (account.State == "authenticated" || account.State == "notRequired") && !draft.reauthorize {
		mobileRow(c, "账号", "", agentAccountName(account), accountSymbol, nil)
		if account.Identity != "" {
			mobileRow(c, "账号标识", "", account.Identity, nil, nil)
		}
		if account.Subscription != "" {
			mobileRow(c, "订阅", "", account.Subscription, nil, nil)
		}
		action(c, "重新检测", refresh).Disabled(blocked || active)
		if len(methods) != 0 && account.State == "authenticated" {
			action(c, "更换账号", func() { draft.reauthorize = true }).Disabled(blocked || active)
		}
		return
	}
	if agent.Account == nil && !active && !draft.reauthorize && !a.preview {
		message := "设备尚未返回账号状态"
		if blocked && a.model.Connected {
			message = "正在读取设备账号…"
		}
		mobileMessage(c, message)
		action(c, "重新检测", refresh).Disabled(blocked)
		return
	}
	selected := draft.selected(methods)
	accountMode := "订阅账号"
	if selected == domain.SignInAPI || agent.Account != nil && agent.Account.Mode == "api" {
		accountMode = "自定义 API"
	}
	apiAvailable := slices.Contains(methods, domain.SignInAPI)
	if apiAvailable && methods[0] != domain.SignInAPI {
		formChoice(c, "接入方式", accountMode, func(menu *ui.Menu) {
			menuChoice(menu, "订阅账号", selected != domain.SignInAPI, func() { draft.method = methods[0] })
			menuChoice(menu, "自定义 API", selected == domain.SignInAPI, func() { draft.method = domain.SignInAPI })
		}).Disabled(active || blocked)
	} else if agent.Account != nil || len(methods) != 0 {
		mobileRow(c, "接入方式", "", accountMode, nil, nil)
	}
	if agent.Account != nil && agent.Account.Mode == "api" {
		account := agent.Account
		mobileRow(c, agentAccountName(account), "", readinessName(agent.State), accountSymbol, nil)
		if account.Endpoint != "" {
			mobileRow(c, "服务地址", "", account.Endpoint, nil, nil)
		}
		if account.Model != "" {
			mobileRow(c, "默认模型", "", account.Model, nil, nil)
		}
		if value := map[string]string{"configured": "已配置", "managed": "由系统管理", "missing": "缺少凭据", "notRequired": "无需凭据"}[account.Credential]; value != "" {
			mobileRow(c, "API Key", "", value, nil, nil)
		}
		action(c, "重新检测", refresh).Disabled(blocked || active)
	} else if apiAvailable && selected == domain.SignInAPI {
		formRow(c, "服务地址", func() {
			formInput(c, "API 地址", "https://api.example.com/v1", &draft.endpoint).
				Autocorrect(false).Capitalize(ui.CapitalizeNone).ReturnKey(ui.ReturnNext).Disabled(blocked || active)
		})
		formRow(c, "API Key", func() {
			formInput(c, "API Key", "可选", &draft.credential).Password().ReturnKey(ui.ReturnGo).
				OnSubmit(func() { a.connectAgentAPI(agent, draft) }).Disabled(blocked || active)
		})
	} else {
		title := agentAccountName(agent.Account)
		if slices.Contains(methods, domain.SignInDeviceCode) && slices.Contains(methods, domain.SignInBrowser) {
			name := "验证码授权"
			if selected == domain.SignInBrowser {
				name = "浏览器授权"
			}
			formChoice(c, "登录方式", name, func(menu *ui.Menu) {
				menuChoice(menu, "验证码授权", selected == domain.SignInDeviceCode, func() { draft.method = domain.SignInDeviceCode })
				menuChoice(menu, "浏览器授权", selected == domain.SignInBrowser, func() { draft.method = domain.SignInBrowser })
			}).Disabled(active || blocked)
		}
		mobileDetailRow(c, title, "登录凭据由远程设备保存", title, accountSymbol, nil, func() {
			if active {
				return
			}
			if len(methods) == 0 {
				action(c, "重新检测", refresh).Disabled(blocked)
				return
			}
			caption := "开始登录"
			if agent.Account != nil && agent.Account.State == "authenticated" {
				caption = "重新登录"
			}
			action(c, caption, func() { a.startAgentSignIn(agent, selected, nil) }).Disabled(blocked)
		})
	}
	a.agentSignInProgress(c, w, attempt)
}
