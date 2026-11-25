package ui

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func profileModeLabel(mode domain.AgentProfileMode) string {
	if mode == domain.AgentProfileAPI {
		return "自定义 API"
	}
	return "账号授权"
}

func (v *View) deviceProfileCreation(c *n.Context, key string, state *app.DeviceState, e *deviceAgentEditor) {
	var agents []domain.HarnessInfo
	for _, info := range e.catalog {
		if len(info.ProfileModes) != 0 {
			agents = append(agents, info)
		}
	}
	if len(agents) == 0 {
		return
	}
	disabled := !state.Connected || e.busy || e.loading || v.Controller == nil
	n.Row(c).FillWidth().Justify(n.End).Children(func() {
		if configurationAction(c.Key("open"), "新增 Profile").Disabled(disabled || e.newProfile != nil).Clicked() {
			e.newProfile = &domain.AgentProfile{HarnessID: agents[0].ID, Mode: agents[0].ProfileModes[0]}
			e.error = ""
		}
	})
	frame := n.Column(c.Key("form")).FillWidth().Clip()
	draft := e.newProfile
	animatedContentHeight(c, frame, draft != nil, configurationTabDuration, func() float32 {
		if draft == nil {
			return 0
		}
		n.Column(c).FillWidth().PaddingX(configurationHoverInset).Children(func() {
			deviceConfigRow(c, "名称", func() {
				configurationTextField(c.Key("profile-name"), &draft.Name).Label("Profile 名称").Disabled(disabled)
			})
			deviceConfigRow(c, "Agent", func() {
				var options []configurationTabItem
				for _, info := range agents {
					options = append(options, configurationTabItem{ID: info.ID, Label: info.Name})
				}
				draft.HarnessID = configurationTabs(c.Key("agent"), draft.HarnessID, options, configurationSegmented, disabled)
			})
			agent := agents[0]
			for _, next := range agents {
				if next.ID == draft.HarnessID {
					agent = next
					break
				}
			}
			if !slices.Contains(agent.ProfileModes, draft.Mode) {
				draft.Mode = agent.ProfileModes[0]
			}
			deviceConfigRow(c, "登录方式", func() {
				var options []configurationTabItem
				for _, mode := range agent.ProfileModes {
					options = append(options, configurationTabItem{ID: string(mode), Label: profileModeLabel(mode)})
				}
				draft.Mode = domain.AgentProfileMode(configurationTabs(c.Key("mode"), string(draft.Mode), options, configurationSegmented, disabled))
			})
			n.Row(c.Key("actions")).FillWidth().Height(36).Justify(n.End).Gap(8).Children(func() {
				if configurationAction(c.Key("cancel"), "取消").Disabled(e.busy).Clicked() {
					e.newProfile = nil
					e.requestedProfile = domain.AgentProfile{}
					e.error = ""
				}
				if configurationAction(c.Key("create"), "创建").Label("创建 Profile").Disabled(disabled || strings.TrimSpace(draft.Name) == "").Clicked() {
					v.createDeviceProfile(key, e, *draft)
				}
			})
			deviceDivider(c)
		})
		return 0
	})
}

func (v *View) createDeviceProfile(key string, e *deviceAgentEditor, draft domain.AgentProfile) {
	draft.Name = strings.TrimSpace(draft.Name)
	prior := e.requestedProfile
	id := prior.ID
	prior.ID = ""
	if prior != draft {
		id = domain.NewID()
	}
	draft.ID = id
	e.requestedProfile = draft
	v.changeDeviceProfile(key, e, "runtime.profile.create", draft, func(result protocol.AgentSetupResult) error {
		for _, info := range result.Catalog {
			if info.ID == draft.ID && info.Profile != nil {
				e.newProfile = nil
				e.requestedProfile = domain.AgentProfile{}
				e.edit(info)
				e.draft.login = true
				return nil
			}
		}
		return errors.New("设备未返回新建的 Profile")
	})
}

func (v *View) setDeviceProfileEnabled(key string, e *deviceAgentEditor, id string, enabled bool) {
	v.changeDeviceProfile(key, e, "runtime.agent.selection", protocol.RuntimeAgentSelection{HarnessID: id, Enabled: &enabled}, nil)
}

func (v *View) changeDeviceProfile(key string, e *deviceAgentEditor, method string, input any, applied func(protocol.AgentSetupResult) error) {
	e.busy, e.error = true, ""
	if !v.do(app.Job{ConnectionID: key, Method: method, Input: input, Apply: func(m *app.Model, data json.RawMessage, err error) {
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
		next := agentEditor(result.Catalog)
		e.catalog, e.paths, e.options, e.optionSupport = next.catalog, next.paths, next.options, next.optionSupport
		result.Readiness.Sessions = state.Readiness.Sessions
		state.Readiness = result.Readiness
		if applied != nil {
			if err := applied(result); err != nil {
				e.error = err.Error()
			}
		}
		m.RefreshDevices()
	}}) {
		e.busy = false
		e.error = "设备操作繁忙，请重试"
	}
}
