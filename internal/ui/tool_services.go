package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strconv"
	"time"
)

type toolServiceLoad uint8

const (
	toolServiceIdle toolServiceLoad = iota
	toolServiceLoading
	toolServiceLoaded
	toolServiceLoadFailed
)

type toolServicesUI struct {
	key, device, harness, epoch string
	editor                      *deviceAgentEditor
	configurationPath           string
	configurationOptions        domain.RuntimeOptions
	open                        bool
	phase                       toolServiceLoad
	catalog                     domain.ToolServiceCatalog
	message                     string
	authorization               *domain.ToolServiceAuthorization
	authPending, autoOpen       bool
	next                        time.Time
	reload                      bool
}

func (v *View) toolServicesFor(key, id string, e *deviceAgentEditor) *toolServicesUI {
	if v.toolServices == nil {
		v.toolServices = map[string]*toolServicesUI{}
	}
	device := v.signInDevice(key)
	epoch := v.deviceState(key).ConnectionEpoch
	slot := device + "/" + id
	u := v.toolServices[slot]
	path, options := toolServiceConfiguration(e, id)
	if u == nil || u.epoch != epoch || u.key != key || u.configurationPath != path || !runtimeOptionsEqual(u.configurationOptions, options) {
		u = &toolServicesUI{key: key, device: device, harness: id, epoch: epoch, configurationPath: path, configurationOptions: options}
		v.toolServices[slot] = u
	}
	u.editor = e
	return u
}
func (v *View) toolServiceCurrent(u *toolServicesUI) bool {
	if v.toolServices[u.device+"/"+u.harness] != u || v.signInDevice(u.key) != u.device {
		return false
	}
	path, options := toolServiceConfiguration(u.editor, u.harness)
	if path != u.configurationPath || !runtimeOptionsEqual(options, u.configurationOptions) {
		return false
	}
	return v.deviceState(u.key).ConnectionEpoch == u.epoch
}
func toolServiceConfiguration(e *deviceAgentEditor, id string) (string, domain.RuntimeOptions) {
	if e != nil {
		for _, info := range e.catalog {
			if info.ID == id && info.Installation != nil {
				return info.Installation.Override, info.Installation.Options
			}
		}
	}
	return "", domain.RuntimeOptions{}
}
func toolServiceDraftCurrent(e *deviceAgentEditor, id string) bool {
	options, err := e.draft.options()
	path, accepted := toolServiceConfiguration(e, id)
	return err == nil && e.openID == id && e.path == path && runtimeOptionsEqual(options, accepted)
}
func (v *View) toolServiceVisible(u *toolServicesUI) bool {
	if !v.toolServiceCurrent(u) || u.editor == nil || !toolServiceDraftCurrent(u.editor, u.harness) {
		return false
	}
	if u.editor.setup {
		return false
	}
	return v.devices && v.deviceSelected == u.key && v.deviceTab != "设备设置" && u.editor.openID == u.harness && u.open
}
func (v *View) toolServiceJob(u *toolServicesUI, method string, in protocol.ToolServiceInput, apply func(*app.Model, json.RawMessage, error)) bool {
	if v.Controller == nil {
		return false
	}
	job := app.Job{ConnectionID: u.key, Method: method, Input: in, Apply: apply}

	return v.do(job)
}
func (v *View) queryToolServices(u *toolServicesUI, refresh bool) {
	if u.phase == toolServiceLoading || v.Controller == nil {
		return
	}
	u.phase = toolServiceLoading
	u.message = ""
	u.reload = false
	method := "runtime.tools.list"
	if refresh {
		method = "runtime.tools.refresh"
	}
	if !v.toolServiceJob(u, method, protocol.ToolServiceInput{HarnessID: u.harness}, func(_ *app.Model, b json.RawMessage, err error) {
		if !v.toolServiceCurrent(u) {
			return
		}
		if err != nil {
			u.phase = toolServiceLoadFailed
			u.message = "无法读取 MCP 服务，请重试"
			return
		}
		var result domain.ToolServiceCatalog
		if json.Unmarshal(b, &result) != nil {
			u.phase = toolServiceLoadFailed
			u.message = "设备返回的工具服务信息无效"
			return
		}
		u.catalog = result
		u.phase = toolServiceLoaded
		if result.Authorization != nil && result.Authorization.HarnessID == u.harness {
			v.applyToolAuthorization(u, *result.Authorization)
		}
	}) {
		u.phase = toolServiceLoadFailed
		u.message = "设备操作繁忙，请重试"
	}
}
func (v *View) applyToolAuthorization(u *toolServicesUI, status domain.ToolServiceAuthorization) {
	wasActive := u.authorization != nil && u.authorization.Phase.Active()
	u.authorization = &status
	if status.Phase == domain.SignInCompleted && wasActive {
		u.reload = true
	}
	if !status.Phase.Active() {
		u.autoOpen = false
	}
}
func (v *View) startToolAuthorization(u *toolServicesUI, service domain.ToolService) {
	if u.authorization != nil && u.authorization.Phase.Active() || u.authPending {
		return
	}
	status := domain.ToolServiceAuthorization{ID: domain.NewID(), HarnessID: u.harness, ServiceID: service.ID, Phase: domain.SignInStarting}
	u.authorization = &status
	u.autoOpen = true
	v.sendToolAuthorization(u, "start")
}
func (v *View) sendToolAuthorization(u *toolServicesUI, action string) {
	if u.authPending || u.authorization == nil {
		return
	}
	u.authPending = true
	expected := *u.authorization
	in := protocol.ToolServiceInput{ID: expected.ID, HarnessID: u.harness, ServiceID: expected.ServiceID}
	if !v.toolServiceJob(u, "runtime.tools.auth."+action, in, func(_ *app.Model, b json.RawMessage, err error) {
		u.authPending = false
		u.next = v.frameTime.Add(time.Second)
		if !v.toolServiceCurrent(u) || u.authorization == nil || u.authorization.ID != expected.ID {
			return
		}
		if err != nil {
			u.authorization.Message = "连接恢复后自动核对授权"
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && action == "start" {
				u.authorization.Phase = domain.SignInFailed
				u.authorization.Message = remote.Message
			}
			return
		}
		var status domain.ToolServiceAuthorization
		if json.Unmarshal(b, &status) != nil || status.ID != expected.ID || status.HarnessID != u.harness || status.ServiceID != expected.ServiceID {
			return
		}
		v.applyToolAuthorization(u, status)
	}) {
		u.authPending = false
		u.authorization.Phase = domain.SignInFailed
		u.authorization.Message = "设备操作繁忙，请重试"
	}
}
func (v *View) syncToolServiceAuthorizations(c *n.Context) {
	for _, u := range v.toolServices {
		if !v.toolServiceCurrent(u) {
			continue
		}
		status := u.authorization
		if status != nil && status.Phase.Active() {
			if u.autoOpen && status.Phase == domain.SignInWaiting && status.Challenge.URL != "" && v.toolServiceVisible(u) {
				u.autoOpen = false
				if openToolServiceLink(status.Challenge.URL) != nil {
					status.Message = "无法打开浏览器，可再次打开授权页"
				}
			}
			connected := v.deviceState(u.key).Connected
			if !u.authPending && connected {
				if c.Now().Before(u.next) {
					c.After(u.next.Sub(c.Now()))
				} else {
					v.sendToolAuthorization(u, "status")
				}
			}
		}
		if u.reload && v.toolServiceVisible(u) && u.phase != toolServiceLoading {
			v.queryToolServices(u, u.catalog.CanRefresh)
		}
	}
}
func toolServiceLabel(service domain.ToolService) string {
	switch service.State {
	case domain.ToolServiceConnecting:
		return "连接中"
	case domain.ToolServiceReady:
		if service.Tools > 0 {
			return strconv.Itoa(service.Tools) + " 个工具"
		}
		return "可用"
	case domain.ToolServiceAuthorizationRequired:
		return "需要授权"
	case domain.ToolServiceFailed:
		return "连接失败"
	case domain.ToolServiceDisabled:
		return "已停用"
	default:
		return "待检查"
	}
}
func (v *View) agentToolServices(c *n.Context, t *n.Theme, key string, e *deviceAgentEditor, info domain.HarnessInfo, disabled bool) {
	u := v.toolServicesFor(key, info.ID, e)
	validDraft := toolServiceDraftCurrent(e, info.ID)
	disabled = disabled || !validDraft
	n.Column(c.Key("tool-services")).FillWidth().Children(func() {
		n.Row(c.Key("tool-services-heading")).FillWidth().Height(configurationSectionHeader).Gap(8).AlignItems(n.Start).Children(func() {
			toggle := control(c, 5).Height(28).Grow(1).Label("展开 MCP 服务").Children(func() {
				n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
					n.Text(c, "MCP").FontSize(configurationSectionSize).FontWeight(500).FixedLineHeight(configurationTitleLine).Grow(1)
					angle := float32(-90)
					if u.open {
						angle = 0
					}
					inlineSymbol(c, chevron, "MCP", n.Font{Family: t.Font, Size: 14, Weight: 500}, 12, t.TextMuted, angle)
				})
			})
			if toggle.Clicked() {
				u.open = !u.open
			}
			if u.phase == toolServiceLoading {
				serviceSpinner(c.Key("service-load-spinner"), "刷新", n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted).FixedLineHeight(configurationControlHeight)
			}
			if configurationAction(c, "刷新").Label("刷新 MCP").Disabled(disabled || u.phase == toolServiceLoading || u.authorization != nil && u.authorization.Phase.Active() || v.Controller == nil).Clicked() {
				u.open = true
				v.queryToolServices(u, u.catalog.CanRefresh)
			}
		})
		visible := u.open
		if visible && validDraft && u.phase == toolServiceIdle {
			v.queryToolServices(u, false)
		}
		body := n.Column(c.Key("tool-services-body")).FillWidth().Clip()
		animatedContentHeight(c, body, visible, 200*time.Millisecond, func() float32 {
			n.Column(c).FillWidth().Children(func() {
				if !validDraft {
					n.Text(c, "保存配置后查看 MCP 服务").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(8)
				} else {
					viewport := n.Scroll(c.Key("service-list")).FillWidth().MaxHeight(configurationListHeight).Label("MCP 服务列表")
					viewport.Children(func() {
						n.Column(c).FillWidth().Children(func() {
							for _, service := range u.catalog.Services {
								row := n.Row(c.Key(service.ID)).FillWidth().Height(configurationRowHeight).PaddingY(6).Gap(12).AlignItems(n.Baseline).Label("MCP 服务：" + service.Name).Transition(n.ElementTransition{Position: true, Duration: motionAnimation(c, 180*time.Millisecond).Duration, Enter: &n.Motion{Opacity: 0}, Exit: &n.Motion{Opacity: 0}})
								row.Children(func() {
									n.Text(c, service.Name).FontSize(13).FixedLineHeight(28).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").Tooltip(service.Origin)
									active := u.authorization != nil && u.authorization.ServiceID == service.ID && u.authorization.Phase.Active()
									caption := toolServiceLabel(service)
									if active {
										caption = "等待授权"
										if u.authorization.Phase == domain.SignInStarting {
											caption = "正在准备授权"
										}
									}
									n.Row(c.Key("status")).Gap(6).AlignItems(n.Baseline).Children(func() {
										if active || service.State == domain.ToolServiceConnecting {
											serviceSpinner(c.Key("spinner"), caption, n.Font{Family: t.Font, Size: 12, Weight: 400}, t.TextMuted)
										}
										n.Text(c.Key("caption"), caption).FontSize(12).FixedLineHeight(28).TextColor(t.TextMuted).SingleLine().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
									})
									authBusy := u.authorization != nil && u.authorization.Phase.Active()
									if active && u.authorization.Challenge.URL != "" {
										if configurationAction(c, "打开授权页").Disabled(disabled).Clicked() {
											if openToolServiceLink(u.authorization.Challenge.URL) != nil {
												u.authorization.Message = "无法打开授权页"
											}
										}
									} else {
										if service.CanAuthorize && (service.State == domain.ToolServiceAuthorizationRequired || service.State == domain.ToolServiceFailed) {
											if configurationAction(c, "授权").Label("授权 " + service.Name).Disabled(disabled || authBusy || u.phase == toolServiceLoading || v.Controller == nil).Clicked() {
												v.startToolAuthorization(u, service)
											}
										}
										if service.State == domain.ToolServiceFailed || service.State == domain.ToolServiceUnknown {
											if configurationAction(c, "重试").Label("重试 " + service.Name).Disabled(disabled || authBusy || u.phase == toolServiceLoading || v.Controller == nil).Clicked() {
												v.queryToolServices(u, u.catalog.CanRefresh)
											}
										}
									}
								})
							}
						})
					})
					if len(u.catalog.Services) == 0 && u.phase != toolServiceLoading && u.message == "" {
						n.Text(c, "暂无 MCP 服务").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(8)
					}
					message := u.message
					if u.authorization != nil && u.authorization.Message != "" {
						message = u.authorization.Message
					}
					feedback := n.Column(c.Key("service-feedback")).FillWidth().Clip()
					last := n.Local(feedback, "message", func() string { return message })
					if message != "" {
						*last = message
					}
					animatedContentHeight(c, feedback, message != "", 180*time.Millisecond, func() float32 {
						n.Text(c, *last).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(8)
						return 0
					})
				}
			})
			return 0
		})
	})
}

func openToolServiceLink(target string) error {
	if !domain.ValidAuthorizationURL(target) {
		return errors.New("授权链接无效")
	}
	return openAuthorizationURL(target)
}
