package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strconv"
	"strings"
)

func (v *View) welcomeAgentOptions(c *n.Context, t *n.Theme, f *welcomeFlow, add bool) {
	e := f.agents
	if e == nil {
		return
	}
	var info domain.HarnessInfo
	for _, candidate := range e.catalog {
		if candidate.ID == e.openID {
			info = candidate
			break
		}
	}
	if info.ID == "" {
		return
	}
	if add {
		n.Row(c.Key("agent-context")).FillWidth().Height(28).Margin(0, 0, 12).AlignItems(n.Baseline).Gap(16).Children(func() {
			n.Text(c, info.Name).FontSize(14).FontWeight(500).FixedLineHeight(20).Grow(1)
			name := v.welcomeDeviceName(f.key)
			inlineIconText(c, monitor, name, n.Font{Family: t.Font, Size: 12, Weight: 400}, 14, 8, t.TextMuted)
		})
	} else {
		n.Row(c).FillWidth().MinHeight(configurationTitleLine).Margin(0, 0, 12).Gap(8).AlignItems(n.Baseline).Children(func() {
			if iconButton(c, deviceBackIcon, "返回 Agent 列表").MarginStart(-6).Disabled(e.busy).Clicked() {
				v.keepWelcomeAgentDraft(e, info.ID)
				e.openID = ""
				v.welcomeStep(welcomeAgents, -1)
			}
			n.Text(c, info.Name).FontSize(configurationTitleSize).FixedLineHeight(configurationTitleLine).Grow(1)
			name := v.welcomeDeviceName(f.key)
			inlineIconText(c, monitor, name, n.Font{Family: t.Font, Size: 12, Weight: 400}, 14, 8, t.TextMuted)
		})
	}
	tabs := []configurationTabItem{{ID: "常规", Label: "常规"}, {ID: "模型", Label: "模型"}}
	support := domain.RuntimeOptionSupport{}
	if info.Installation != nil {
		support = info.Installation.OptionsSupported
	}
	if support.Arguments || support.Directory || support.Environment || support.Instructions {
		tabs = append(tabs, configurationTabItem{ID: "高级", Label: "高级"})
	}
	n.Column(c).FillWidth().Margin(0, 0, 12).Children(func() { e.tab = configurationTabs(c, e.tab, tabs, configurationUnderline, false) })
	e.setup = true
	e.workerMinor = v.deviceState(f.key).WorkerMinor
	if f.key == "" {
		e.workerMinor = v.sshSetup.Info.Minor
	}
	_, height := c.Size()
	top := max(float32(112), min(height/2-208, 256))
	// Reserve the steps, heading, tabs, footer and bottom window inset.
	available := height - top - 40 - 48 - 48 - 56 - 24
	if add {
		// The SSH dialog also owns a title row and 24 DIP outer insets.
		available = height - 48 - 48 - 48 - 40 - 40 - 48 - 56
	}
	// Let the error's actual animated height shrink the body in the same layout pass.
	configurationTabPanel(c.Key("agent-tab-panel"), e.tab, configurationTabIndex(tabs, e.tab), configurationPanelFit, func() {
		n.Column(c.Key("configuration-region")).FillWidth().MaxHeight(max(96, available)).Children(func() {
			n.Scroll(c.Key("agent-configuration-scroll:" + e.tab)).Width(f.width + 16).MarginX(-8).PaddingX(8).MinHeight(0).Shrink(1).Label("Agent 配置内容").Children(func() {
				connected := f.key != "" && v.deviceState(f.key).Connected || v.sshCheck == sshSelecting || v.sshCheck == sshSaving
				v.agentConfiguration(c.Key("agent-tab:"+e.tab), t, f.key, connected, e, info, true, func() {})
			})
			n.Scroll(c.Key("agent-configuration-error")).FillWidth().MaxHeight(60).Shrink(0).Children(func() { v.welcomeError(c, e.error) })
		})
	})
	v.welcomeFooter(c, "取消", "保存", e.busy, func() {
		delete(f.agentDrafts, v.welcomeAgentDraftKey(info.ID))
		e.edit(info)
		e.openID = ""
		v.welcomeStep(welcomeAgents, -1)
	}, func() {
		options, err := e.draft.options()
		if err != nil {
			e.error = err.Error()
			return
		}
		e.paths[info.ID] = strings.TrimSpace(e.path)
		if e.workerMinor >= 18 {
			e.options[info.ID] = options
		}
		delete(f.agentDrafts, v.welcomeAgentDraftKey(info.ID))
		e.openID = ""
		v.welcomeStep(welcomeAgents, -1)
	})
}

func (v *View) recheckWelcomeAgent(key, id string) {
	f := v.welcome
	if v.Controller == nil || f == nil || f.agents == nil || f.agents.loading || f.agents.busy {
		return
	}
	if key != "" && !v.deviceState(key).Connected {
		return
	}
	e := f.agents
	e.loading = true
	e.error = ""
	job := app.Job{ConnectionID: key, Method: "runtime.check", Input: protocol.RuntimeConfiguration{HarnessID: id}}
	epoch := v.deviceState(key).ConnectionEpoch
	job.ConnectionEpoch = epoch
	if key == "" {
		port, _ := strconv.Atoi(strings.TrimSpace(v.sshPort))
		job = app.Job{Method: "$device.check", Input: app.CheckSSHDevice{Name: v.sshName, SSH: v.setupWorkerSSH(port, v.sshSetup.Info)}}
	}
	job.Apply = func(m *app.Model, b json.RawMessage, err error) {
		e.loading = false
		if v.welcome != f || f.agents != e || f.key != key || key != "" && v.deviceState(key).ConnectionEpoch != epoch {
			return
		}
		if err != nil {
			e.error = err.Error()
			return
		}
		var catalog []domain.HarnessInfo
		if key == "" {
			var result app.DeviceSetupCheck
			if json.Unmarshal(b, &result) != nil || result.Info.DeviceID != v.sshSetup.Info.DeviceID {
				e.error = "设备身份或检测结果已改变"
				return
			}
			catalog = result.Catalog
		} else {
			var ready domain.Readiness
			if json.Unmarshal(b, &ready) != nil {
				e.error = "设备返回的 Agent 状态无效"
				return
			}
			v.deviceState(key).Readiness = ready
			catalog = ready.Harnesses
			m.RefreshDevices()
		}
		e.refresh(catalog)
	}
	if !v.Controller.Do(job) {
		e.loading = false
		e.error = "设备操作繁忙，请重试"
	}
}
