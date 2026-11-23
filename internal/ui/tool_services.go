package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
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
	draft                       *toolConfigurationDraft
	saving, saved               bool
	mutation, catalogRead       uint64
	mutationID, removingID      string
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
	return v.devices && v.deviceSelected == u.key && u.editor.pageID == u.harness && u.editor.tab == "MCP"
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
	u.catalogRead++
	read := u.catalogRead
	u.message = ""
	checkMessage := "已保存，正在检查连接"
	checkFailure := "配置已保存，连接检查未完成，请刷新核对"
	if u.catalog.BasicEditing {
		checkMessage = "已保存，正在核对设备配置"
		checkFailure = "配置已保存，读取未完成，请刷新核对"
	}
	if u.saved && !slices.ContainsFunc(u.catalog.Services, func(service domain.ToolService) bool { return service.ID == u.mutationID }) {
		u.message = checkMessage
	}
	u.reload = false
	method := "runtime.tools.list"
	if refresh {
		method = "runtime.tools.refresh"
	}
	if !v.toolServiceJob(u, method, protocol.ToolServiceInput{HarnessID: u.harness}, func(_ *app.Model, b json.RawMessage, err error) {
		if !v.toolServiceCurrent(u) || read != u.catalogRead || u.phase != toolServiceLoading {
			return
		}
		if err != nil {
			u.phase = toolServiceLoadFailed
			u.message = "无法读取 MCP 服务，请重试"
			if u.saved {
				u.message = checkFailure
			}
			return
		}
		var result domain.ToolServiceCatalog
		if json.Unmarshal(b, &result) != nil {
			u.phase = toolServiceLoadFailed
			u.message = "设备返回的工具服务信息无效"
			if u.saved {
				u.message = checkFailure
			}
			return
		}
		u.catalog = result
		u.phase = toolServiceLoaded
		if u.saved {
			u.saved = false
			u.mutationID = ""
			u.message = "已保存，下轮生效"
			if result.BasicEditing {
				u.message = "已保存到设备配置，当前会话生效状态未知"
			}
		}
		if result.Authorization != nil && result.Authorization.HarnessID == u.harness {
			v.applyToolAuthorization(u, *result.Authorization)
		}
	}) {
		u.phase = toolServiceLoadFailed
		u.message = "设备操作繁忙，请重试"
		if u.saved {
			u.message = checkFailure
		}
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
	valid := toolServiceDraftCurrent(e, info.ID)
	if valid && u.phase == toolServiceIdle {
		v.queryToolServices(u, false)
	}
	if !valid {
		n.Text(c, "保存配置后查看 MCP 服务").FontSize(12).FixedLineHeight(20).PaddingX(configurationHoverInset).TextColor(t.TextMuted)
		return
	}
	n.Column(c.Key("tool-services")).FillWidth().FillHeight().Grow(1).Children(func() {
		page, index := "list", 0
		if u.draft != nil {
			page, index = "editor", 1
		}
		configurationTabPanel(c.Key("service-page"), page, index, configurationPanelFill, func() {
			n.Column(c).FillWidth().FillHeight().Grow(1).PaddingX(configurationHoverInset).Children(func() {
				if u.draft != nil {
					v.toolConfigurationEditor(c, t, u, disabled)
					return
				}
				v.toolServiceList(c, t, u, disabled)
			})
		}, false)
	})
}

func openToolServiceLink(target string) error {
	if !domain.ValidAuthorizationURL(target) {
		return errors.New("授权链接无效")
	}
	return openAuthorizationURL(target)
}
