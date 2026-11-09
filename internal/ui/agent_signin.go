package ui

import (
	"encoding/json"
	"errors"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"net/url"
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
	config          *protocol.RuntimeConfiguration
	key, device     string
	ssh             *transport.SSH
	status          domain.SignInStatus
	pending         bool
	next            time.Time
	autoOpen        bool
	editor          *deviceAgentEditor
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
	if current := v.agentSignIn(key, info.ID); current != nil && current.status.Phase.Active() {
		return
	}
	if v.Controller == nil {
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
	u := &agentSignInUI{key: key, device: v.signInDevice(key), editor: e, autoOpen: true, status: domain.SignInStatus{ID: domain.NewID(), HarnessID: info.ID, Phase: domain.SignInStarting}}
	if key == "" {
		port, _ := strconv.Atoi(strings.TrimSpace(v.sshPort))
		u.ssh = &transport.SSH{Target: strings.TrimSpace(v.sshTarget), Port: port, Worker: v.sshSetup.Info.Worker, StateDirectory: v.sshSetup.Info.Directory}
	}
	if v.agentSignIns == nil {
		v.agentSignIns = map[string]*agentSignInUI{}
	}
	v.agentSignIns[u.device+"/"+info.ID] = u
	if key != "" {
		if v.Model.SignInIDs == nil {
			v.Model.SignInIDs = map[string]map[string]string{}
		}
		if v.Model.SignInIDs[key] == nil {
			v.Model.SignInIDs[key] = map[string]string{}
		}
		v.Model.SignInIDs[key][info.ID] = u.status.ID
	}
	config := protocol.RuntimeConfiguration{HarnessID: info.ID, Executable: strings.TrimSpace(e.path)}
	if e.optionSupport[info.ID] {
		config.Options = &options
	}
	e.error = ""
	u.config = &config
	v.sendSignIn(u, "start", &protocol.SignInInput{ID: u.status.ID, HarnessID: info.ID, Method: method, Configuration: &config})
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
		u.status = status
		if status.Phase == domain.SignInWaiting && u.autoOpen && status.Challenge.URL != "" {
			u.autoOpen = false
			if err := openSignInLink(status.Challenge.URL); err != nil {
				u.status.Message = "无法打开浏览器，可再次打开授权页"
			}
		}
		if status.Phase == domain.SignInCompleted && status.Profile != nil {
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
func (v *View) agentAuthentication(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo, disabled bool) {
	u := v.agentSignIn(key, info.ID)
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
		}

		active := u != nil && u.status.Phase.Active()
		methods := v.agentSignInMethods(key, info)
		method := agentSignInMethod(key, e, methods)
		minor := v.deviceState(key).WorkerMinor
		if key == "" {
			minor = v.sshSetup.Info.Minor
		}
		supported := info.Installation != nil && minor >= 19 && slices.Contains(methods, method)
		n.Row(c.Key("auth-actions")).FillWidth().MinHeight(28).Gap(8).AlignItems(n.Baseline).Children(func() {
			caption := identity
			tooltip := ""
			if info.Account != nil {
				tooltip = info.Account.Identity
			}
			if active {
				caption = "正在准备登录"
				if u.status.Phase == domain.SignInWaiting {
					caption = "等待授权"
				}
			}
			if u != nil && u.status.Message != "" {
				caption = u.status.Message
				tooltip = u.status.Message
			}
			status := func() {
				n.Text(c.Key("auth-status"), caption).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine().Tooltip(tooltip).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			}
			if active || !supported {
				status()
			}

			if active {
				if u.status.Challenge.URL != "" && deviceAction(c.Key("open-authorization"), "打开授权页").Transition(fadeMotion(c, 160*time.Millisecond)).Disabled(disabled).Clicked() {
					if err := openSignInLink(u.status.Challenge.URL); err != nil {
						u.status.Message = "无法打开授权页"
					}
				}
				if deviceAction(c.Key("cancel-authorization"), "取消登录").Transition(fadeMotion(c, 160*time.Millisecond)).Disabled(u.pending).Clicked() {
					v.sendSignIn(u, "cancel", nil)
				}
			} else {
				if supported {
					n.Row(c.Key("sign-in-method-control")).Grow(1).MinWidth(0).Height(28).Gap(8).AlignItems(n.Center).Transition(fadeMotion(c, 160*time.Millisecond)).Children(func() {
						name := "浏览器授权"
						if method == domain.SignInDeviceCode {
							name = "验证码授权"
						}
						if len(methods) > 1 {
							trigger := control(c.Key("login-method"), 5).Width(112).Height(28).PaddingX(8).Radius(5).Border(1, t.Border).Label("选择登录方式").Disabled(disabled).Children(func() {
								n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
									n.Text(c, name).FontSize(12).FixedLineHeight(20).Grow(1).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
									inlineSymbol(c, chevron, name, n.Font{Family: t.Font, Size: 12, Weight: 400}, 12, t.TextMuted)
								})
							})
							var choices []menuChoice
							for _, value := range []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode} {
								if slices.Contains(methods, value) {
									label := "浏览器授权"
									if value == domain.SignInDeviceCode {
										label = "验证码授权"
									}
									choices = append(choices, menuChoice{id: string(value), name: label, checked: method == value, choose: func() { e.signInMethod = value }})
								}
							}
							v.choiceMenu(c, t, "sign-in-method:"+key+":"+info.ID, trigger, choices)
						} else {
							n.Text(c, name).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
						}
						status()
						label := "登录"
						if info.Account != nil && info.Account.State == "authenticated" {
							label = "重新登录"
						}
						if deviceAction(c.Key("begin-authorization"), "").Label("登录账号").Disabled(disabled).Children(func() {
							n.Text(c, label).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
						}).Clicked() {
							v.startAgentSignIn(key, e, info)
						}
					})
				} else if info.Installation != nil && len(info.Installation.SignInCommand) > 0 {
					copyLabel := "复制登录命令"
					if c.Now().Before(e.draft.copiedUntil) {
						copyLabel = "已复制"
						c.AnimationFrame()
					}
					if deviceAction(c.Key("copy-login-command"), "").Label("复制登录命令").Width(88).Disabled(disabled).Children(func() {
						n.Text(c, copyLabel).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
					}).Clicked() {
						c.WriteClipboard(v.agentLoginCommand(key, info.Installation.SignInCommand))
						e.draft.copiedUntil = c.Now().Add(2 * time.Second)
					}
				}
				if deviceAction(c.Key("recheck-account"), "重新检测").Transition(fadeMotion(c, 160*time.Millisecond)).Disabled(disabled || v.Controller == nil).Clicked() {
					if e.setup {
						v.recheckWelcomeAgent(key, info.ID)
					} else {
						v.recheckAgent(key, info.ID)
					}
				}
			}
		})
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
					c.AnimationFrame()
				}
				if deviceAction(c.Key("copy-login-code"), "").Label("复制验证码").Width(76).Disabled(!active || challenge.Code == "").Children(func() {
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
	if info.Account != nil && u.status.Profile.Account != nil && *info.Account == *u.status.Profile.Account && info.State == u.status.Profile.State {
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
	for _, value := range []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode} {
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
	return methods
}
