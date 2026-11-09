package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"time"
)

type deviceAgentEditor struct {
	signInMethod            domain.SignInMethod
	setup                   bool
	tab                     string
	workerMinor             int
	options                 map[string]domain.RuntimeOptions
	optionSupport           map[string]bool
	draft                   runtimeOptionDraft
	epoch                   string
	catalog                 []domain.HarnessInfo
	paths                   map[string]string
	openID                  string
	pageID                  string
	path                    string
	loading, busy, readOnly bool
	error                   string
}

func agentEditor(catalog []domain.HarnessInfo) *deviceAgentEditor {
	e := &deviceAgentEditor{catalog: catalog, paths: map[string]string{}, options: map[string]domain.RuntimeOptions{}, optionSupport: map[string]bool{}}
	for _, info := range catalog {
		if info.Installation != nil {
			e.paths[info.ID] = info.Installation.Override
			e.options[info.ID] = info.Installation.Options
			support := info.Installation.OptionsSupported
			e.optionSupport[info.ID] = support.Arguments || support.Directory || support.Environment || support.Instructions || support.TitleModel
		}
	}
	return e
}
func (e *deviceAgentEditor) input() protocol.DeviceAgents {
	in := protocol.DeviceAgents{Agents: []protocol.RuntimeConfiguration{}}
	for _, info := range e.catalog {
		if info.Configured {
			entry := protocol.RuntimeConfiguration{HarnessID: info.ID, Executable: e.paths[info.ID]}
			if e.optionSupport[info.ID] {
				options := e.options[info.ID]
				entry.Options = &options
			}
			in.Agents = append(in.Agents, entry)
		}
	}
	return in
}
func (v *View) deviceAgents(c *n.Context, t *n.Theme, key string, state *app.DeviceState, e **deviceAgentEditor) {
	scope := n.Column(c).FillWidth()
	v.syncDeviceAgents(key, state, e)
	editor := *e
	scope.Children(func() {
		n.Column(c).FillWidth().Gap(4).Children(func() {
			v.agentRows(c.Key("rows"), t, key, state.Connected, editor, false, func() { v.saveDeviceAgents(key, state, editor) })
			if len(editor.catalog) == 0 {
				label := "暂无 Agent"
				if editor.loading {
					label = "正在检测…"
				}
				n.Text(c, label).FontSize(12).TextColor(t.TextMuted).PaddingY(8)
			}
			if state.WorkerMinor < 16 && state.Connected {
				n.Text(c, "更新执行服务后可管理 Agent").FontSize(11).TextColor(t.TextMuted)
			}
		})
	})
}

func (v *View) syncDeviceAgents(key string, state *app.DeviceState, e **deviceAgentEditor) {
	if state.Connected && state.ConnectionEpoch != (*e).epoch {
		editor := agentEditor(state.Readiness.Harnesses)
		editor.epoch = state.ConnectionEpoch
		*e = editor
		editor.readOnly = state.WorkerMinor < 16
		if state.WorkerMinor < 16 {
			for i := range editor.catalog {
				editor.catalog[i].Configured = true
			}
		} else {
			v.loadDeviceAgents(key, state, editor)
		}
	}
	if !(*e).busy && !(*e).loading && state.ConnectionEpoch == (*e).epoch {
		authoritative := map[string]domain.HarnessInfo{}
		for _, info := range state.Readiness.Harnesses {
			authoritative[info.ID] = info
		}
		for i := range (*e).catalog {
			old := &(*e).catalog[i]
			if next, ok := authoritative[old.ID]; ok {
				next.Configured = true
				*old = next
				if (*e).openID != next.ID && next.Installation != nil {
					(*e).paths[next.ID] = next.Installation.Override
					(*e).options[next.ID] = next.Installation.Options
				}
			} else if state.WorkerMinor >= 16 {
				old.Configured = false
			}
		}
	}

}

func (v *View) loadDeviceAgents(key string, state *app.DeviceState, e *deviceAgentEditor) {
	e.loading = true
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.catalog", Apply: func(m *app.Model, b json.RawMessage, err error) {
		e.loading = false
		current := m.DeviceStates[key]
		if current == nil || current.ConnectionEpoch != e.epoch {
			return
		}
		if err != nil {
			e.error = err.Error()
			return
		}
		var catalog domain.Readiness
		if err = json.Unmarshal(b, &catalog); err != nil {
			e.error = "设备返回的 Agent 列表无效"
			return
		}
		next := agentEditor(catalog.Harnesses)
		e.catalog, e.paths, e.options, e.optionSupport = next.catalog, next.paths, next.options, next.optionSupport
	}}) {
		e.loading = false
		e.error = "设备操作繁忙，请重试"
	}
}

func (v *View) deviceAgentPage(c *n.Context, t *n.Theme, key, deviceName string, state *app.DeviceState, e *deviceAgentEditor, info domain.HarnessInfo) {
	header := n.Column(c.Key("agent-header")).FillWidth().Height(104).Padding(24, configurationInset, 12)
	width, _ := c.Size()
	if width < 900 {
		header.Height(88).Padding(8, configurationInset, 12)
	}
	header.Children(func() {
		n.Column(c).FillWidth().MaxWidth(840).Gap(8).Children(func() {
			back := control(c.Key("back"), 5).Height(28).MaxWidth(400).AlignSelf(n.Start).Justify(n.Start).PaddingX(4).MarginStart(-4).Label("返回 Agent 列表").Tooltip(deviceName).Disabled(e.busy)
			if width < 900 {
				back.MarginStart(60)
			}
			back.Children(func() {
				inlineIconText(c, deviceBackIcon, deviceName, n.Font{Family: t.Font, Size: configurationValueSize, Weight: 400}, 12, 4, t.TextMuted).Shrink(1)
			})
			if back.Clicked() {
				e.pageID = ""
			}
			n.Row(c).FillWidth().Height(32).Gap(16).AlignItems(n.Baseline).Children(func() {
				inlineIconText(c, harnessIcon(domain.HarnessDescriptor{ID: info.ID, Name: info.Name, Icon: info.Icon}), info.Name, n.Font{Family: t.Font, Size: configurationTitleSize, Weight: 400}, 24, 8, t.Text).FixedLineHeight(configurationTitleLine).Grow(1).MinWidth(0).Tooltip(info.Name)
				n.Text(c, runtimeSetupLabel(info, state.Connected)).FontSize(12).FixedLineHeight(20).SingleLine().TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			})
		})
	})
	n.Column(c.Key("agent-content")).FillWidth().FillHeight().Grow(1).Padding(0, configurationInset, configurationSectionGap).Children(func() {
		n.Column(c).FillWidth().FillHeight().Grow(1).MaxWidth(840).Children(func() {
			v.agentConfiguration(c, t, key, state.Connected, e, info, false, func() { v.saveDeviceAgents(key, state, e) })
			frame := n.Column(c.Key("agent-error")).FillWidth().Clip()
			message := n.Local(frame, "message", func() string { return e.error })
			if e.error != "" {
				*message = e.error
			}
			animatedContentHeight(c, frame, e.error != "", 180*time.Millisecond, func() float32 {
				n.Text(c, *message).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
				return 0
			})
		})
	})
}

func (v *View) saveDeviceAgents(key string, state *app.DeviceState, e *deviceAgentEditor) {
	e.busy = true
	e.error = ""
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.agents", Input: e.input(), Apply: func(m *app.Model, b json.RawMessage, err error) {
		e.busy = false
		current := m.DeviceStates[key]
		if current == nil || current.ConnectionEpoch != e.epoch {
			return
		}
		if err != nil {
			e.error = err.Error()
			v.loadDeviceAgents(key, current, e)
			return
		}
		var result protocol.AgentSetupResult
		if err = json.Unmarshal(b, &result); err != nil {
			e.error = "设备返回的配置结果无效"
			return
		}
		if !result.Ready {
			e.error = result.Error
			for _, info := range result.Catalog {
				for i := range e.catalog {
					if e.catalog[i].ID == info.ID {
						e.catalog[i].Installation = info.Installation
						e.catalog[i].State = info.State
						e.catalog[i].Configured = info.Configured
					}
				}
			}
			return
		}
		next := agentEditor(result.Catalog)
		e.catalog, e.paths, e.options, e.optionSupport = next.catalog, next.paths, next.options, next.optionSupport
		current.Readiness = result.Readiness
		m.RefreshDevices()
	}}) {
		e.busy = false
		e.error = "设备操作繁忙，请重试"
	}
}
func (v *View) agentRows(c *n.Context, t *n.Theme, key string, connected bool, e *deviceAgentEditor, onboarding bool, save func()) {
	for i := range e.catalog {
		info := &e.catalog[i]
		n.Column(c.Key(info.ID)).FillWidth().Children(func() {
			header := n.Row(c).FillWidth().Height(56).PaddingX(8).Gap(8).AlignItems(n.Center).Radius(6)
			controlFeedback(c, header, 6)
			header.Children(func() {
				toggle := func() {
					if e.openID != info.ID {
						e.edit(*info)
					}
					e.pageID = info.ID
				}
				button := n.ButtonBase(c).Background(n.Transparent).Grow(1).Height(56).Padding(10, 0).Label("配置 " + info.Name + " 路径").Disabled(info.Installation == nil).Children(func() {
					n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
						inlineSymbol(c, harnessIcon(domain.HarnessDescriptor{ID: info.ID, Name: info.Name, Icon: info.Icon}), info.Name, n.Font{Family: t.Font, Size: 14, Weight: 500}, 24, t.TextMuted)
						n.Column(c).Grow(1).Children(func() {
							n.Text(c, info.Name).FontSize(14).FontWeight(500).FixedLineHeight(20)
							label := runtimeSetupLabel(*info, connected)
							if key == "local" && !connected {
								label = "状态待核对"
							}
							if !info.Configured {
								label = "未启用"
							}
							if onboarding && info.State == "" {
								label = "自动检测"
							}
							n.Text(c, label).FontSize(12).FixedLineHeight(16).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
						})
					})
				})
				if button.Clicked() {
					toggle()
				}
				enabled := info.Configured
				if configurationSwitch(c, &enabled).Label("启用 " + info.Name).Disabled(e.readOnly || !connected || e.busy || e.loading || v.Controller == nil).Changed() {
					info.Configured = enabled
					if !onboarding {
						save()
					}
				}
				arrow := n.ButtonBase(c).Size(28, 28).AlignItems(n.Center).Justify(n.Center).Label("打开 " + info.Name + " 配置").Disabled(info.Installation == nil).Children(func() { n.Icon(c, disclosureRight).Size(16, 16).TextColor(t.TextMuted) })
				if arrow.Clicked() {
					toggle()
				}
			})
			deviceDivider(c)
		})
	}

	frame := n.Column(c.Key("error")).FillWidth().Clip()
	message := n.Local(frame, "text", func() string { return e.error })
	if e.error != "" {
		*message = e.error
	}
	animatedContentHeight(c, frame, e.error != "", 180*time.Millisecond, func() float32 {
		n.Text(c, *message).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		return 0
	})
}

func (v *View) saveDisabledAgentSettings(key string, e *deviceAgentEditor, id string) {
	e.busy = true
	e.error = ""
	in := protocol.RuntimeConfiguration{HarnessID: id, Executable: e.paths[id]}
	if e.optionSupport[id] {
		options := e.options[id]
		in.Options = &options
	}
	if !v.do(app.Job{ConnectionID: key, Method: "runtime.configure", Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		e.busy = false
		current := m.DeviceStates[key]
		if current == nil || current.ConnectionEpoch != e.epoch {
			return
		}
		if err != nil {
			e.error = err.Error()
		} else {
			var ready domain.Readiness
			if json.Unmarshal(b, &ready) != nil {
				e.error = "设备返回的配置结果无效"
			} else {
				current.Readiness = ready
				m.RefreshDevices()
			}
		}
		v.loadDeviceAgents(key, current, e)
	}}) {
		e.busy = false
		e.error = "设备操作繁忙，请重试"
	}
}

// Refresh public metadata/capabilities without replacing the user's unsaved configuration.
func (e *deviceAgentEditor) refresh(catalog []domain.HarnessInfo) {
	for _, info := range catalog {
		found := false
		for i, old := range e.catalog {
			if old.ID == info.ID {
				info.Configured = old.Configured
				e.catalog[i] = info
				found = true
				break
			}
		}
		if !found {
			e.catalog = append(e.catalog, info)
			if info.Installation != nil {
				e.paths[info.ID] = info.Installation.Override
				e.options[info.ID] = info.Installation.Options
			}
		}
		if info.Installation != nil {
			s := info.Installation.OptionsSupported
			e.optionSupport[info.ID] = s.Arguments || s.Directory || s.Environment || s.Instructions || s.TitleModel
		}
	}
}
