package ui

import (
	"encoding/json"
	"errors"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"net/url"
	"reflect"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"slices"
	"strconv"
	"strings"
	"time"
)

var openAuthorizationURL = mygo.Shell.OpenExternal

type agentSignInUI struct {
	profileConsumed bool
	initial         bool
	config          *protocol.RuntimeConfiguration
	key, device     string
	ssh             *transport.SSH
	status          domain.SignInStatus
	pending         bool
	next            time.Time
	autoOpen        bool
	editor          *deviceAgentEditor
	manualCode      string
}

func (v *View) signInDevice(key string) string {
	if key == "" {
		return v.sshSetup.Info.DeviceID
	}
	return v.Model.ConnectionDeviceID(key)
}
func (v *View) agentSignIn(key, id string) *agentSignInUI {
	return v.agentSignIns[v.signInDevice(key)+"/"+id]
}
func (v *View) syncAgentSignIns(c *n.Context) {
	if v.agentSignIns == nil {
		v.agentSignIns = map[string]*agentSignInUI{}
	}
	for key, refs := range v.Model.SignInIDs {
		for harness, id := range refs {
			device := v.signInDevice(key)
			if device == "" {
				continue
			}
			slot := device + "/" + harness
			if v.agentSignIns[slot] == nil {
				v.agentSignIns[slot] = &agentSignInUI{key: key, device: device, status: domain.SignInStatus{ID: id, HarnessID: harness, Phase: domain.SignInStarting}}
			}
		}
	}
	if v.Controller == nil {
		return
	}
	for _, entry := range v.agentSignIns {
		if !entry.status.Phase.Active() || entry.pending {
			continue
		}
		if entry.key != "" {
			state := v.deviceState(entry.key)
			if !state.Connected {
				continue
			}
			if state.WorkerMinor < 19 {
				entry.status.Phase = domain.SignInExpired
				entry.status.Challenge = domain.SignInChallenge{}
				entry.status.Message = "更新执行服务后重新登录"
				delete(v.Model.SignInIDs[entry.key], entry.status.HarnessID)
				continue
			}
		}
		if !entry.next.IsZero() && c.Now().Before(entry.next) {
			c.After(entry.next.Sub(c.Now()))
			continue
		}
		v.sendSignIn(entry, "status", nil)
	}
}
func (v *View) startAgentSignIn(key string, e *deviceAgentEditor, info domain.HarnessInfo) {
	if current := v.agentSignIn(key, info.ID); current != nil && current.status.Phase.Active() || v.Controller == nil {
		return
	}
	options, err := e.draft.options()
	if err != nil {
		e.error = err.Error()
		return
	}
	methods := v.agentSignInMethods(key, info)
	method := agentSignInMethod(key, e, methods)
	if !slices.Contains(methods, method) {
		e.error = "该 Agent 暂不支持此登录方式"
		return
	}
	config := protocol.RuntimeConfiguration{HarnessID: info.ID, Executable: strings.TrimSpace(e.path)}
	if e.optionSupport[info.ID] {
		config.Options = &options
	}
	e.error = ""
	in := protocol.SignInInput{HarnessID: info.ID, Method: method, Configuration: &config}
	if method == domain.SignInAPI {
		in.API = &domain.APIConfiguration{Endpoint: strings.TrimSpace(e.draft.apiEndpoint), Credential: e.draft.apiCredential}
	}
	v.beginAgentSignIn(key, e, in, slices.Contains(methods, domain.SignInAPI))
}

func (v *View) beginAgentSignIn(key string, e *deviceAgentEditor, in protocol.SignInInput, initial bool) {
	if current := v.agentSignIn(key, in.HarnessID); current != nil && current.status.Phase.Active() {
		return
	}
	if v.Controller == nil {
		return
	}
	in.ID = domain.NewID()
	u := &agentSignInUI{initial: initial, key: key, device: v.signInDevice(key), editor: e, config: in.Configuration, autoOpen: true, status: domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInStarting}}
	if key == "" {
		port, _ := strconv.Atoi(strings.TrimSpace(v.sshPort))
		u.ssh = setupWorkerSSHPointer(v.setupWorkerSSH(port, v.sshSetup.Info))
	}
	if v.agentSignIns == nil {
		v.agentSignIns = map[string]*agentSignInUI{}
	}
	v.agentSignIns[u.device+"/"+in.HarnessID] = u
	if key != "" {
		if v.Model.SignInIDs == nil {
			v.Model.SignInIDs = map[string]map[string]string{}
		}
		if v.Model.SignInIDs[key] == nil {
			v.Model.SignInIDs[key] = map[string]string{}
		}
		v.Model.SignInIDs[key][in.HarnessID] = u.status.ID
	}
	v.sendSignIn(u, "start", &in)
}
func (v *View) sendSignIn(u *agentSignInUI, action string, input *protocol.SignInInput) {
	if u.pending || v.Controller == nil {
		return
	}
	u.pending = true
	in := protocol.SignInInput{ID: u.status.ID, HarnessID: u.status.HarnessID}
	if input != nil {
		in = *input
	}
	job := app.Job{ConnectionID: u.key, Method: "runtime.signin." + action, Input: in}
	if action == "start" && v.Saver != nil {
		v.queueState()
		job.Prepare = v.Saver.FlushLatest
	}
	if u.key == "" {
		job.Method = "$device.signin"
		job.Input = app.SignInSSHDevice{SSH: *u.ssh, DeviceID: u.device, Action: action, Input: in}
	}
	job.Apply = func(m *app.Model, body json.RawMessage, err error) {
		u.pending = false
		u.next = v.frameTime.Add(time.Second)
		if v.agentSignIns[u.device+"/"+u.status.HarnessID] != u {
			return
		}
		if err != nil {
			// The start might have reached the worker. Keep its ID and query; never
			// automatically issue a second start after a lost reply.
			u.status.Message = "连接暂时不可用，恢复后自动核对登录"
			if action == "cancel" {
				u.status.Message = "取消尚未确认，连接恢复后可重试"
			}
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && action == "code" {
				u.status.Message = remote.Message
			}
			if errors.As(err, &remote) && action == "start" {
				u.status.Phase = domain.SignInFailed
				u.status.Message = remote.Message
				if u.status.Message == "" {
					u.status.Message = "无法开始登录，请重试或检查 Agent 配置"
				}
				if u.key != "" {
					delete(m.SignInIDs[u.key], u.status.HarnessID)
				}
			}
			return
		}
		var status domain.SignInStatus
		if json.Unmarshal(body, &status) != nil || status.ID != u.status.ID || status.HarnessID != u.status.HarnessID {
			return
		}
		if status.Phase == domain.SignInCompleted {
			// Older workers could retain a pending verification message on success.
			status.Message = ""
		}
		u.status = status
		if !status.Challenge.ManualCode {
			u.manualCode = ""
		}
		if status.Phase == domain.SignInWaiting && u.autoOpen && status.Challenge.URL != "" {
			u.autoOpen = false
			if err := openSignInLink(status.Challenge.URL); err != nil {
				u.status.Message = "无法打开浏览器，可再次打开授权页"
			}
		}
		if status.Phase == domain.SignInCompleted && status.Profile != nil {
			if u.editor != nil {
				u.editor.draft.apiCredential = ""
			}
			if u.editor != nil && signInConfigurationMatches(u, u.editor) {
				u.editor.refresh([]domain.HarnessInfo{*status.Profile})
			}
			if f := v.welcome; f != nil && f.agents != nil && v.signInDevice(f.key) == u.device && signInConfigurationMatches(u, f.agents) {
				f.agents.refresh([]domain.HarnessInfo{*status.Profile})
			}
		}
		if !status.Phase.Active() && u.key != "" {
			delete(m.SignInIDs[u.key], status.HarnessID)
		}
	}
	accepted := false
	if u.key == "" {
		accepted = v.Controller.Do(job)
	} else {
		accepted = v.do(job)
	}
	if !accepted {
		u.pending = false
		u.status.Phase = domain.SignInFailed
		u.status.Message = "设备操作繁忙，请重试"
	}
}
func openSignInLink(target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("授权链接无效")
	}
	return openAuthorizationURL(target)
}
func accountAuthenticated(account *domain.RuntimeAccount) bool {
	return account != nil && (account.State == "authenticated" || account.Mode != "api" && account.State == "configured")
}

func (v *View) agentAuthentication(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo, disabled bool) {
	u := v.agentSignIn(key, info.ID)
	initialActive := u != nil && u.initial && u.status.Phase.Active()
	if info.Account != nil && info.Account.Mode == "api" && !initialActive {
		v.agentSystemAPI(c, t, key, e, info, disabled)
		return
	}
	if agentSignInMethod(key, e, v.agentSignInMethods(key, info)) == domain.SignInAPI {
		v.agentAPIConfiguration(c, t, key, e, info, disabled)
		return
	}
	if slices.Contains(v.agentSignInMethods(key, info), domain.SignInAPI) {
		v.agentInitialAccount(c, t, key, e, info, disabled)
		return
	}
	if u != nil {
		u.editor = e
	}

	n.Column(c.Key("account-authentication")).FillWidth().Children(func() {
		identity := ""
		if info.Account != nil {
			identity = info.Account.Identity
			if at := strings.Index(identity, "@"); at > 1 {
				identity = string([]rune(identity[:at])[:1]) + "…" + identity[at:]
			}
			if identity == "" && accountAuthenticated(info.Account) {
				identity = "已登录"
			}
		}

		active := u != nil && u.status.Phase.Active()
		methods := v.agentSignInMethods(key, info)
		method := agentSignInMethod(key, e, methods)
		minor := v.deviceState(key).WorkerMinor
		if key == "" {
			minor = v.sshSetup.Info.Minor
		}
		supported := info.Installation != nil && minor >= 19 && slices.Contains(methods, method)
		n.Row(c.Key("auth-actions")).FillWidth().MinHeight(28).Gap(6).AlignItems(n.Center).Children(func() {
			caption := identity
			tooltip := ""
			if info.Account != nil {
				tooltip = info.Account.Identity
			}
			if active {
				caption = "正在准备登录"
				if method == domain.SignInAPI {
					caption = "正在保存配置"
				}
				if u.status.Phase == domain.SignInWaiting {
					caption = "等待授权"
				}
			}
			if u != nil && u.status.Message != "" {
				caption = u.status.Message
				tooltip = u.status.Message
			}
			status := func() {
				text := n.Text(c.Key("auth-status"), caption).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
				if tooltip != caption {
					text.Tooltip(tooltip)
				} else {
					text.TooltipWhenTruncated(caption)
				}
			}
			if active || !supported {
				if active {
					serviceSpinner(c.Key("auth-spinner"), caption, n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
				}
				status()
			}

			if active {
				if u.status.Challenge.URL != "" && configurationAction(c.Key("open-authorization"), "打开授权页").Transition(fadeMotion(c, 160*time.Millisecond)).Disabled(disabled).Clicked() {
					if err := openSignInLink(u.status.Challenge.URL); err != nil {
						u.status.Message = "无法打开授权页"
					}
				}
				if method != domain.SignInAPI && configurationAction(c.Key("cancel-authorization"), "取消登录").Transition(fadeMotion(c, 160*time.Millisecond)).Disabled(u.pending).Clicked() {
					v.sendSignIn(u, "cancel", nil)
				}
			} else {
				if supported {
					n.Row(c.Key("sign-in-method-control")).Grow(1).MinWidth(0).Height(28).Gap(6).AlignItems(n.Center).Transition(fadeMotion(c, 160*time.Millisecond)).Children(func() {
						name := signInMethodLabel(method)
						if len(methods) > 1 {
							trigger := configurationChoiceField(c.Key("login-method"), "选择登录方式", name, disabled).Width(104)
							var choices []menuChoice
							for _, value := range []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode} {
								if slices.Contains(methods, value) {
									label := signInMethodLabel(value)
									choices = append(choices, menuChoice{id: string(value), name: label, checked: method == value, choose: func() { e.signInMethod = value }})
								}
							}
							v.choiceMenu(c, t, "sign-in-method:"+key+":"+info.ID, trigger, choices)
						} else {
							n.Text(c, name).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).MarginStart(configurationFieldInset)
						}
						status()
						label := "登录"
						if accountAuthenticated(info.Account) {
							label = "重新登录"
						}
						actionLabel := "登录账号"
						if method == domain.SignInAPI {
							label = "保存"
							actionLabel = "保存 API 配置"
						}
						if configurationAction(c.Key("begin-authorization"), "").Label(actionLabel).Disabled(disabled).Children(func() {
							n.Text(c, label).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
						}).Clicked() {
							v.startAgentSignIn(key, e, info)
						}
					})
				} else if info.Installation != nil && len(info.Installation.SignInCommand) > 0 {
					copyLabel := "复制登录命令"
					if c.Now().Before(e.draft.copiedUntil) {
						copyLabel = "已复制"
						c.After(e.draft.copiedUntil.Sub(c.Now()))
					}
					if configurationAction(c.Key("copy-login-command"), "").Label("复制登录命令").Width(88).Disabled(disabled).Children(func() {
						n.Text(c, copyLabel).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
					}).Clicked() {
						c.WriteClipboard(v.agentLoginCommand(key, info.Installation.SignInCommand))
						e.draft.copiedUntil = c.Now().Add(2 * time.Second)
					}
				}
				if !slices.Contains(methods, domain.SignInAPI) {
					if configurationAction(c.Key("recheck-account"), "重新检测").Transition(fadeMotion(c, 160*time.Millisecond)).Disabled(disabled || v.Controller == nil).Clicked() {
						if e.setup {
							v.recheckWelcomeAgent(key, info.ID)
						} else {
							v.recheckAgent(key, info.ID)
						}
					}
				}
			}
		})
		v.agentManualAuthorization(c, u, disabled)
		body := n.Column(c.Key("auth-code")).FillWidth().Clip()
		var challenge domain.SignInChallenge
		if u != nil {
			challenge = u.status.Challenge
		}
		last := n.Local(body, "code", func() string { return challenge.Code })
		if challenge.Code != "" {
			*last = challenge.Code
		}
		animatedContentHeight(c, body, active && challenge.Code != "", 180*time.Millisecond, func() float32 {
			n.Row(c).FillWidth().MinHeight(28).Margin(8, 0, 0).Gap(8).AlignItems(n.Baseline).Children(func() {
				n.Text(c, *last).Font(v.Model.Preferences.CodeFont).FontSize(13).FixedLineHeight(20).Grow(1).Selectable()
				copyLabel := "复制验证码"
				if c.Now().Before(e.draft.copiedUntil) {
					copyLabel = "已复制"
					c.After(e.draft.copiedUntil.Sub(c.Now()))
				}
				if configurationAction(c.Key("copy-login-code"), "").Label("复制验证码").Width(76).Disabled(!active || challenge.Code == "").Children(func() {
					n.Text(c, copyLabel).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
				}).Clicked() {
					c.WriteClipboard(challenge.Code)
					e.draft.copiedUntil = c.Now().Add(2 * time.Second)
				}
			})
			return 0
		})
	})
}

func (v *View) agentManualAuthorization(c *n.Context, u *agentSignInUI, disabled bool) {
	if u == nil || u.status.Phase != domain.SignInWaiting || !u.status.Challenge.ManualCode {
		return
	}
	n.Row(c.Key("manual-google-auth")).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
		textField(c, &u.manualCode).Grow(1).Basis(0).MinWidth(0).Height(configurationControlHeight).Padding(4, configurationFieldInset).
			Radius(configurationFieldRadius).FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).Background(c.Theme().Background).
			Label("Google 授权码").Placeholder("粘贴授权码或回调地址").Password().Disabled(disabled || u.pending)
		if configurationAction(c, "提交授权").Shrink(0).Disabled(disabled || u.pending || strings.TrimSpace(u.manualCode) == "").Clicked() {
			v.sendSignIn(u, "code", &protocol.SignInInput{ID: u.status.ID, HarnessID: u.status.HarnessID, AuthorizationCode: u.manualCode})
			if u.pending {
				u.manualCode = ""
			}
		}
	})
}

func signInConfigurationMatches(u *agentSignInUI, e *deviceAgentEditor) bool {
	if u.config == nil || e.openID != u.status.HarnessID {
		return false
	}
	options, err := e.draft.options()
	return err == nil && strings.TrimSpace(e.path) == u.config.Executable && (u.config.Options == nil || runtimeOptionsEqual(options, *u.config.Options))
}
func (v *View) signInProfileInfo(key string, e *deviceAgentEditor, info domain.HarnessInfo) domain.HarnessInfo {
	u := v.agentSignIn(key, info.ID)
	if u == nil || u.status.Phase != domain.SignInCompleted || u.status.Profile == nil || !signInConfigurationMatches(u, e) {
		return info
	}
	if u.profileConsumed {
		return info
	}
	if info.Account != nil && u.status.Profile.Account != nil && reflect.DeepEqual(info.Account, u.status.Profile.Account) && info.State == u.status.Profile.State {
		u.profileConsumed = true
		return info
	}

	result := *u.status.Profile
	result.Configured = info.Configured
	return result
}

func agentSignInMethod(key string, e *deviceAgentEditor, methods []domain.SignInMethod) domain.SignInMethod {
	if e.signInMethod != "" && slices.Contains(methods, e.signInMethod) {
		return e.signInMethod
	}
	preferred := domain.SignInBrowser
	if key != "local" {
		preferred = domain.SignInDeviceCode
	}
	if slices.Contains(methods, preferred) {
		return preferred
	}
	for _, value := range []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode, domain.SignInAPI} {
		if slices.Contains(methods, value) {
			return value
		}
	}
	return ""
}

func (v *View) agentSignInMethods(key string, info domain.HarnessInfo) []domain.SignInMethod {
	if info.Installation == nil {
		return nil
	}
	minor := v.deviceState(key).WorkerMinor
	if key == "" {
		minor = v.sshSetup.Info.Minor
	}
	var methods []domain.SignInMethod
	for _, method := range info.Installation.SignInMethods {
		if method == domain.SignInBrowser && key != "local" && minor < 20 {
			continue
		}
		methods = append(methods, method)
	}
	u := v.agentSignIn(key, info.ID)
	activeInitial := u != nil && u.initial && u.status.Phase.Active()
	if minor >= 21 && info.Installation.APISetup && (activeInitial || info.Account == nil && info.Installation.Issue == domain.RuntimeSignInRequired || info.Account != nil && info.Account.State == "required") {
		methods = append(methods, domain.SignInAPI)
	}
	return methods
}

func signInMethodLabel(method domain.SignInMethod) string {
	switch method {
	case domain.SignInDeviceCode:
		return "验证码授权"
	case domain.SignInAPI:
		return "自定义 API"
	default:
		return "浏览器授权"
	}
}
func (v *View) agentSystemAPI(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo, disabled bool) {
	row := deviceConfigRow
	if e.setup {
		row = setupRow
	}
	account := info.Account
	credential := map[string]string{"configured": "已配置", "managed": "由系统管理", "missing": "缺少凭据", "notRequired": "无需凭据"}[account.Credential]
	endpoint := account.Endpoint
	if info.Installation != nil && info.Installation.APIKeyOnly {
		endpoint = ""
	}
	for _, field := range []struct{ label, value string }{{"服务地址", endpoint}, {"默认模型", account.Model}, {"API Key", credential}} {
		if field.value == "" {
			continue
		}
		row(c, field.label, func() {
			n.Text(c, field.value).FillWidth().Padding(4, 8).FontSize(12).FixedLineHeight(20).SingleLine().TooltipWhenTruncated(field.value)
		})
	}
	row(c, "", func() {
		n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
			n.Text(c, info.Error).Grow(1).MinWidth(0).SingleLine().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Tooltip(info.Error)
			if configurationAction(c, "重新检测").Disabled(disabled || v.Controller == nil).Clicked() {
				if e.setup {
					v.recheckWelcomeAgent(key, info.ID)
				} else {
					v.recheckAgent(key, info.ID)
				}
			}
		})
	})
}

func (v *View) agentAPIConfiguration(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo, disabled bool) {
	row := deviceConfigRow
	if e.setup {
		row = setupRow
	}
	u := v.agentSignIn(key, info.ID)
	active := u != nil && u.status.Phase.Active()
	if u != nil {
		u.editor = e
	}
	agentAPIFields(c, row, &e.draft, disabled || active)
	row(c, "", func() {
		n.Row(c.Key("api-connection-status")).FillWidth().Height(28).AlignItems(n.Baseline).Gap(8).Children(func() {
			caption := ""
			if active {
				caption = "正在连接"
			}
			if u != nil && u.status.Message != "" {
				caption = u.status.Message
			}
			if active {
				serviceSpinner(c.Key("spinner"), caption, n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
			}
			n.Text(c.Key("caption"), caption).FontSize(12).FixedLineHeight(20).Grow(1).MinWidth(0).SingleLine().TextColor(t.TextMuted).TooltipWhenTruncated(caption).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			if configurationAction(c.Key("connect-api"), "连接").Label("连接 API").MinWidth(64).Disabled(disabled || active || strings.TrimSpace(e.draft.apiEndpoint) == "").Clicked() {
				v.startAgentSignIn(key, e, info)
			}
		})
	})
}

func agentAPIFields(c *n.Context, row func(*n.Context, string, func()), draft *runtimeOptionDraft, disabled bool) (submit bool) {
	row(c, "服务地址", func() {
		field := setupField(c, "API 地址", &draft.apiEndpoint, "https://api.example.com/v1", disabled)
		submit = field.Submitted() && !field.Composing()
	})
	return agentAPIKeyField(c, row, draft, disabled, false) || submit
}

func agentAPIKeyField(c *n.Context, row func(*n.Context, string, func()), draft *runtimeOptionDraft, disabled, required bool) (submit bool) {
	placeholder := "可选"
	if required {
		placeholder = "输入 API Key"
	}
	row(c, "API Key", func() {
		n.Column(c).FillWidth().Height(configurationControlHeight).Children(func() {
			field := configurationTextField(c, &draft.apiCredential).Padding(4, 36, 4, 8).Label("输入 API Key").Placeholder(placeholder).Disabled(disabled)
			if !draft.apiReveal {
				field.Password()
			}
			submit = submit || field.Submitted() && !field.Composing()
			label := "显示 API Key"
			if draft.apiReveal {
				label = "隐藏 API Key"
			}
			if iconButton(c, modelEye, label).Absolute().Right(0).Top(0).Disabled(disabled).Clicked() {
				draft.apiReveal = !draft.apiReveal
			}
		})
	})
	return submit
}

func (v *View) agentConnectionMode(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo, disabled bool) {
	method := agentSignInMethod(key, e, v.agentSignInMethods(key, info))
	u := v.agentSignIn(key, info.ID)
	active := u != nil && u.status.Phase.Active()
	selected := "account"
	if method == domain.SignInAPI {
		selected = "api"
	}
	next := configurationTabs(c, selected, []configurationTabItem{{ID: "account", Label: "账号授权"}, {ID: "api", Label: "自定义 API"}}, configurationSegmented, disabled || active)
	if next != selected {
		e.signInMethod = ""
		if next == "api" {
			e.signInMethod = domain.SignInAPI
		}
		e.draft.login = true
	}
}

// Initial setup retains its method row while authorization progresses below.
func (v *View) agentInitialAccount(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo, disabled bool) {
	methods := v.agentSignInMethods(key, info)
	method := agentSignInMethod(key, e, methods)
	u := v.agentSignIn(key, info.ID)
	if u != nil {
		u.editor = e
	}
	active := u != nil && u.status.Phase.Active()
	n.Column(c).FillWidth().Children(func() {
		n.Row(c).FillWidth().Height(28).Gap(8).AlignItems(n.Baseline).Children(func() {
			name := signInMethodLabel(method)
			trigger := configurationChoiceField(c.Key("login-method"), "选择登录方式", name, disabled || active).Width(112)
			var choices []menuChoice
			for _, value := range []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode} {
				if slices.Contains(methods, value) {
					choices = append(choices, menuChoice{id: string(value), name: signInMethodLabel(value), checked: value == method, choose: func() { e.signInMethod = value }})
				}
			}
			v.choiceMenu(c, t, "sign-in-method:"+key+":"+info.ID, trigger, choices)
			n.Spacer(c).Grow(1)
			if configurationAction(c, "登录").Label("登录账号").Disabled(disabled || active).Clicked() {
				v.startAgentSignIn(key, e, info)
			}
		})
		body := n.Column(c.Key("initial-auth-progress")).FillWidth().Clip()
		message := ""
		if active {
			message = "正在准备登录"
			if u.status.Phase == domain.SignInWaiting {
				message = "等待授权"
			}
		}
		if u != nil && u.status.Message != "" {
			message = u.status.Message
		}
		last := n.Local(body, "message", func() string { return message })
		if message != "" {
			*last = message
		}
		challenge := domain.SignInChallenge{}
		if u != nil {
			challenge = u.status.Challenge
		}
		code := n.Local(body, "code", func() string { return challenge.Code })
		if challenge.Code != "" {
			*code = challenge.Code
		}
		animatedContentHeight(c, body, active || message != "", 180*time.Millisecond, func() float32 {
			n.Row(c).FillWidth().MinHeight(28).Margin(8, 0, 0).Gap(8).AlignItems(n.Baseline).Children(func() {
				if active {
					serviceSpinner(c.Key("spinner"), *last, n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
				}
				n.Text(c.Key("caption"), *last).FontSize(12).FixedLineHeight(20).Grow(1).MinWidth(0).SingleLine().TooltipWhenTruncated(*last).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
				if active {
					if challenge.URL != "" && configurationAction(c, "打开授权页").Disabled(disabled).Clicked() {
						if openSignInLink(challenge.URL) != nil {
							u.status.Message = "无法打开授权页"
						}
					}
					if configurationAction(c, "取消登录").Disabled(u.pending).Clicked() {
						v.sendSignIn(u, "cancel", nil)
					}
				}
			})
			v.agentManualAuthorization(c, u, disabled)
			challengeBody := n.Column(c.Key("initial-auth-code")).FillWidth().Clip()
			animatedContentHeight(c, challengeBody, active && challenge.Code != "", 180*time.Millisecond, func() float32 {
				n.Row(c).FillWidth().Height(32).Gap(8).AlignItems(n.Baseline).Children(func() {
					n.Text(c, *code).Font(v.Model.Preferences.CodeFont).FontSize(13).FixedLineHeight(20).Grow(1).Selectable()
					caption := "复制"
					if c.Now().Before(e.draft.copiedUntil) {
						caption = "已复制"
						c.After(e.draft.copiedUntil.Sub(c.Now()))
					}
					if configurationAction(c, caption).Label("复制验证码").Disabled(!active).Clicked() {
						c.WriteClipboard(challenge.Code)
						e.draft.copiedUntil = c.Now().Add(2 * time.Second)
					}
				})
				return 0
			})
			return 0
		})
	})
}
