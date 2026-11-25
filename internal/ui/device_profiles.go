package ui

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type deviceProfileEditor struct {
	key, device                                string
	draft                                      domain.AgentProfile
	info                                       domain.HarnessInfo
	request                                    domain.AgentProfile
	auth                                       runtimeOptionDraft
	method                                     domain.SignInMethod
	agentChoice                                string
	working                                    bool
	modelRequest                               uint64
	loadingModels                              bool
	models                                     []domain.Model
	modelError, modelEndpoint, modelCredential string
	apiDrafts                                  map[string]runtimeOptionDraft
	installRequested                           bool
	seenSignIn                                 string
	error                                      string
}

func profileModeLabel(mode domain.AgentProfileMode) string {
	if mode == domain.AgentProfileAPI {
		return "自定义 API"
	}
	return "账号授权"
}

func deviceProfileProviders(e *deviceAgentEditor) []domain.HarnessInfo {
	var agents []domain.HarnessInfo
	custom := false
	for _, info := range e.catalog {
		install := info.Installation
		if info.IsCustomProfileProvider() {
			custom = custom || slices.Contains(info.ProfileModes, domain.AgentProfileAPI)
			continue
		}
		if len(info.ProfileModes) == 0 || install == nil || install.Issue == domain.RuntimeMissing || install.Issue == domain.RuntimeIncompatible {
			continue
		}
		var modes []domain.AgentProfileMode
		for _, mode := range info.ProfileModes {
			if mode == domain.AgentProfileAPI && install.APISetup || mode == domain.AgentProfileAccount && len(profileSignInMethods(info)) != 0 {
				modes = append(modes, mode)
			}
		}
		if len(modes) != 0 {
			info.ProfileModes = modes
			agents = append(agents, info)
		}
	}
	if custom {
		agents = append(agents, domain.HarnessInfo{ID: customProfileChoice, Name: "自定义", Icon: "profile-sliders", ProfileModes: []domain.AgentProfileMode{domain.AgentProfileAPI}})
	}
	return agents
}

func profileModelsPending(info domain.HarnessInfo) bool {
	return info.Profile != nil && info.Profile.Authorized && info.State == domain.RuntimeUnavailable &&
		accountAuthenticated(info.Account) && (info.Installation == nil || info.Installation.Issue == "" || info.Installation.Issue == domain.RuntimeUnavailableIssue)
}

func (v *View) canContinueDeviceProfile(key string, info domain.HarnessInfo) bool {
	if info.Profile == nil || info.Installation == nil {
		return false
	}
	if info.Profile.Authorized {
		return info.CanRepairAPIRuntime() || info.Configured && profileModelsPending(info)
	}
	if domain.IsCustomAgent(info.Profile.HarnessID) {
		return true
	}
	if info.Installation.Issue == domain.RuntimeMissing || info.Installation.Issue == domain.RuntimeIncompatible {
		return false
	}
	if info.Profile.Mode == domain.AgentProfileAPI {
		return slices.Contains(v.agentSignInMethods(key, info), domain.SignInAPI)
	}
	return len(profileSignInMethods(info)) != 0
}

func (v *View) openDeviceProfile(key string, info *domain.HarnessInfo) {
	p := &deviceProfileEditor{key: key, device: v.signInDevice(key)}
	if info != nil && info.Profile != nil {
		p.draft, p.info = *info.Profile, *info
		if op := v.agentSignIn(key, info.ID); info.Profile.Authorized && op != nil && op.status.Phase == domain.SignInCompleted {
			p.seenSignIn = op.status.ID
		}
	}
	v.deviceProfile = p
}

func (p *deviceProfileEditor) selectAgent(info domain.HarnessInfo) {
	p.apiDrafts = nil
	p.modelRequest++
	p.loadingModels = false
	p.modelError = ""
	p.draft = domain.AgentProfile{HarnessID: info.ID, Mode: info.ProfileModes[0]}
	if info.ID == customProfileChoice {
		p.draft.HarnessID, p.draft.Icon = domain.AgentResponses, "profile-sliders"
	}
	p.auth = runtimeOptionDraft{}
	p.method, p.error = "", ""
}

func (v *View) syncDeviceProfile() {
	p := v.deviceProfile
	if p != nil && (!v.devices || v.deviceSelected != p.key || v.deviceTab != "agents" || v.signInDevice(p.key) != p.device) {
		v.closeDeviceProfile(p)
	}
}

func (v *View) closeDeviceProfile(p *deviceProfileEditor) {
	if op := v.agentSignIns[p.device+"/"+p.info.ID]; op != nil && op.status.Phase.Active() {
		// Keep polling an explicit operation after leaving the device page.
		// A challenge arriving later is available when the profile is reopened.
		op.autoOpen = false
	}
	p.auth = runtimeOptionDraft{}
	delete(v.menuOpen, "profile-method:"+p.key)
	delete(v.menuOpen, "profile-agent:"+p.key)
	delete(v.menuOpen, "profile-api-models:"+p.key)
	delete(v.menuOpen, "profile-icon:"+p.key)
	p.models, p.apiDrafts, p.modelCredential = nil, nil, ""
	if v.deviceProfile == p {
		v.deviceProfile = nil
	}
}

func (v *View) returnFromDeviceProfile(e *deviceAgentEditor, p *deviceProfileEditor) {
	if p.info.ID != "" {
		e.profileAnchor, e.revealProfile = p.info.ID, true
	} else {
		e.focusProfileEntry = true
	}
	v.closeDeviceProfile(p)
}

func (v *View) deviceProfileEntry(c *n.Context, key string, state *app.DeviceState, e *deviceAgentEditor) {
	if state.WorkerMinor < protocol.AgentProfileMinor {
		return
	}
	agents := deviceProfileProviders(e)
	if len(agents) == 0 {
		return
	}
	button := deviceTabAction(c, "新增 Profile").Disabled(!state.Connected || e.busy || e.loading || v.Controller == nil)
	if e.focusProfileEntry {
		e.focusProfileEntry = false
		button.Focus()
	}
	if button.Clicked() {
		e.profileAnchor = ""
		v.openDeviceProfile(key, nil)
		c.Services().Invalidate()
	}
}

func (v *View) submitDeviceProfile(key string, state *app.DeviceState, e *deviceAgentEditor, p *deviceProfileEditor) {
	p.error = ""
	if p.info.CanRepairAPIRuntime() {
		p.installRequested = true
		v.beginAgentSignIn(key, nil, protocol.SignInInput{HarnessID: p.info.ID, Method: domain.SignInAPI,
			API: &domain.APIConfiguration{InstallRuntime: true}}, true)
		return
	}
	if p.info.Profile != nil && p.info.Profile.Authorized {
		v.refreshDeviceProfileModels(key, state, e, p)
		return
	}
	keyOnly := p.info.Installation != nil && p.info.Installation.APIKeyOnly
	for _, info := range e.catalog {
		if info.ID == p.draft.HarnessID && info.Installation != nil && info.Installation.APIKeyOnly {
			keyOnly = true
			break
		}
	}
	if keyOnly && strings.TrimSpace(p.auth.apiCredential) == "" {
		p.error = "请输入 API Key"
		return
	}
	if p.draft.Mode == domain.AgentProfileAPI && !keyOnly {
		address := strings.TrimSpace(p.auth.apiEndpoint)
		u, err := url.Parse(address)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(address) > 4096 {
			p.error = "请输入不含凭据的 API 地址"
			return
		}
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
			p.error = "API 地址须使用 HTTPS，本机服务可使用 HTTP"
			return
		}
	}
	if domain.IsCustomAgent(p.draft.HarnessID) {
		if strings.TrimSpace(p.auth.apiModel) == "" || strings.TrimSpace(p.auth.apiCredential) == "" {
			p.error = "请输入模型 ID 和 API Key"
			return
		}
		p.installRequested = p.needsRuntime(e)
	}
	if p.info.ID != "" {
		v.connectDeviceProfile(p)
		return
	}
	p.installRequested = domain.IsCustomAgent(p.draft.HarnessID) && p.needsRuntime(e)
	draft := p.draft
	draft.Name = strings.TrimSpace(draft.Name)
	if keyOnly {
		draft.Name = draft.HarnessID
	}
	if draft.Name == "" || len(draft.Name) > 256 || strings.ContainsFunc(draft.Name, unicode.IsControl) {
		p.error = "请输入有效的 Profile 名称"
		return
	}
	firstAttempt := p.request.ID == ""
	if firstAttempt {
		draft.ID = domain.NewID()
		p.request = draft
	}
	v.createDeviceProfile(key, state, e, p, firstAttempt)
}

func (v *View) createDeviceProfile(key string, state *app.DeviceState, e *deviceAgentEditor, p *deviceProfileEditor, firstAttempt bool) {
	request, epoch := p.request, state.ConnectionEpoch
	p.working = true
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.profile.create", Input: request, Apply: func(m *app.Model, data json.RawMessage, err error) {
		p.working = false
		current := m.DeviceStates[key]
		if current == nil || current.ConnectionEpoch != epoch || m.ConnectionDeviceID(key) != p.device {
			p.error = "创建结果待确认，连接恢复后可重试"
			return
		}
		var result protocol.AgentSetupResult
		if err == nil {
			err = json.Unmarshal(data, &result)
		}
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) {
				if firstAttempt {
					p.request = domain.AgentProfile{}
				}
				p.error = remote.Message
			} else {
				p.error = "创建结果待确认，重试会核对同一个 Profile"
			}
			return
		}
		if !result.Ready {
			if firstAttempt {
				p.request = domain.AgentProfile{}
			}
			p.error = result.Error
			return
		}
		e.applyDeviceProfileCatalog(m, current, result)
		i := slices.IndexFunc(result.Catalog, func(info domain.HarnessInfo) bool {
			a := info.Profile
			return info.ID == request.ID && a != nil && a.ID == request.ID &&
				a.Name == request.Name && a.Mode == request.Mode && a.HarnessID == request.HarnessID
		})
		if i < 0 {
			p.error = "设备未返回新建的 Profile，重试会核对同一个 Profile"
			return
		}
		p.info, p.draft = result.Catalog[i], request
		if v.deviceProfile == p && v.devices && v.deviceSelected == key && v.deviceTab == "agents" && current.Connected {
			v.connectDeviceProfile(p)
		}
	}}) {
		p.working = false
		if firstAttempt {
			p.request = domain.AgentProfile{}
		}
		p.error = "设备操作繁忙，请重试"
	}
}

func profileSignInMethods(info domain.HarnessInfo) []domain.SignInMethod {
	var methods []domain.SignInMethod
	if info.Installation != nil {
		for _, method := range info.Installation.SignInMethods {
			switch method {
			case domain.SignInBrowser, domain.SignInDeviceCode:
				methods = append(methods, method)
			}
		}
	}
	return methods
}

func (v *View) connectDeviceProfile(p *deviceProfileEditor) {
	info := p.info
	if info.Profile == nil || info.Profile.Authorized {
		return
	}
	in := protocol.SignInInput{HarnessID: info.ID}
	if p.draft.Mode == domain.AgentProfileAPI {
		if methods := v.agentSignInMethods(p.key, info); !slices.Contains(methods, domain.SignInAPI) && !domain.IsCustomAgent(p.draft.HarnessID) {
			p.error = "此 Profile 当前无法连接 API，请检查设备的 Agent"
			return
		}
		in.Method = domain.SignInAPI
		in.API = &domain.APIConfiguration{Endpoint: strings.TrimSpace(p.auth.apiEndpoint), Credential: p.auth.apiCredential, Model: strings.TrimSpace(p.auth.apiModel), InstallRuntime: p.installRequested}
	} else {
		in.Method = agentSignInMethod(p.key, &deviceAgentEditor{signInMethod: p.method}, profileSignInMethods(info))
		if in.Method == "" {
			p.error = "此 Profile 当前没有可用的授权方式，请检查设备的 Agent"
			return
		}
	}
	// A Profile already has its own saved adapter configuration on the worker.
	v.beginAgentSignIn(p.key, nil, in, p.draft.Mode == domain.AgentProfileAPI)
}

func (v *View) syncDeviceProfileSetup(state *app.DeviceState, e *deviceAgentEditor, p *deviceProfileEditor) {
	if p.info.ID == "" {
		return
	}
	if op := v.agentSignIn(p.key, p.info.ID); op != nil && op.status.Phase == domain.SignInCompleted && p.seenSignIn != op.status.ID {
		p.seenSignIn = op.status.ID
		info := op.status.Profile
		if info != nil && info.ID == p.info.ID && info.Profile != nil && info.Profile.ID == p.info.ID && info.Profile.Authorized {
			e.refresh([]domain.HarnessInfo{*info})
			for i, previous := range state.Readiness.Harnesses {
				if previous.ID == info.ID {
					state.Readiness.Harnesses[i] = *info
					state.Readiness.Harnesses[i].Configured = previous.Configured
					break
				}
			}
			v.Model.RefreshDevices()
		} else {
			p.error = "设备未确认此 Profile 已接入，请重新检测"
		}
	}

	// The owning device can publish a binding without this client's receipt.
	// Never regress a confirmed binding while a catalog refresh is in flight.
	if i := slices.IndexFunc(e.catalog, func(info domain.HarnessInfo) bool { return info.ID == p.info.ID }); i >= 0 {
		current := e.catalog[i]
		if current.Profile != nil && (!p.info.Profile.Authorized || current.Profile.Authorized) {
			if current.Profile.Authorized && !p.info.Profile.Authorized {
				p.auth, p.error = runtimeOptionDraft{}, ""
			}
			p.info = current
		}
	}
	if state.Connected && p.info.Profile.Authorized && p.info.State == domain.RuntimeReady {
		v.returnFromDeviceProfile(e, p)
	}
	if op := v.agentSignIn(p.key, p.info.ID); op != nil && op.status.Setup != nil && op.status.Setup.Phase == "verifying" && p.info.Installation != nil && !p.info.Profile.Authorized {
		installation := *p.info.Installation
		installation.Issue = domain.RuntimeSignInRequired
		p.info.Installation = &installation
	}
}

func (v *View) refreshDeviceProfileModels(key string, state *app.DeviceState, e *deviceAgentEditor, p *deviceProfileEditor) {
	p.working = true
	epoch, id := state.ConnectionEpoch, p.info.ID
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.check", Input: protocol.RuntimeConfiguration{HarnessID: id}, Apply: func(m *app.Model, data json.RawMessage, err error) {
		p.working = false
		current := m.DeviceStates[key]
		if current == nil || current.ConnectionEpoch != epoch || m.ConnectionDeviceID(key) != p.device {
			p.error = "连接已变化，请重新检测模型"
			return
		}
		var ready domain.Readiness
		if err == nil {
			err = json.Unmarshal(data, &ready)
		}
		if err != nil {
			p.error = err.Error()
			return
		}
		i := slices.IndexFunc(ready.Harnesses, func(info domain.HarnessInfo) bool { return info.ID == id })
		if i < 0 {
			p.error = "设备未返回模型状态，请确认此 Profile 已启用"
			return
		}
		ready.Sessions = current.Readiness.Sessions
		current.Readiness, p.info = ready, ready.Harnesses[i]
		e.refresh([]domain.HarnessInfo{p.info})
		m.RefreshDevices()
		if v.deviceProfile == p && p.info.State == domain.RuntimeReady {
			v.returnFromDeviceProfile(e, p)
		}
	}}) {
		p.working = false
		p.error = "设备操作繁忙，请重试"
	}
}

func (v *View) setDeviceProfileEnabled(key string, e *deviceAgentEditor, id string, enabled bool) {
	e.busy, e.error = true, ""
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.agent.selection", Input: protocol.RuntimeAgentSelection{HarnessID: id, Enabled: &enabled}, Apply: func(m *app.Model, data json.RawMessage, err error) {
		e.busy = false
		state := m.DeviceStates[key]
		if state == nil || state.ConnectionEpoch != e.epoch {
			return
		}
		var result protocol.AgentSetupResult
		if err == nil {
			err = json.Unmarshal(data, &result)
		}
		if err == nil && !result.Ready {
			err = errors.New(result.Error)
		}
		if err != nil {
			e.error = err.Error()
			return
		}
		e.applyDeviceProfileCatalog(m, state, result)
	}}) {
		e.busy = false
		e.error = "设备操作繁忙，请重试"
	}
}

func (e *deviceAgentEditor) applyDeviceProfileCatalog(m *app.Model, state *app.DeviceState, result protocol.AgentSetupResult) {
	next := agentEditor(result.Catalog)
	e.catalog, e.paths, e.options, e.optionSupport = next.catalog, next.paths, next.options, next.optionSupport
	result.Readiness.Sessions = state.Readiness.Sessions
	state.Readiness = result.Readiness
	m.RefreshDevices()
}
