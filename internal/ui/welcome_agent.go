package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
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
	v.welcomeHeading(c, t, "配置 "+info.Name, f.key, true)
	n.Row(c).FillWidth().Height(36).Margin(0, 0, 16).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Gap(24).Children(func() {
		for _, label := range []string{"常规", "模型", "高级"} {
			if label == "模型" && len(info.Models) == 0 {
				continue
			}
			support := domain.RuntimeOptionSupport{}
			if info.Installation != nil {
				support = info.Installation.OptionsSupported
			}
			if label == "高级" && !(support.Arguments || support.Directory || support.Environment || support.Instructions) {
				continue
			}
			color := t.TextMuted
			if e.tab == label {
				color = t.Text
			}
			button := n.ButtonBase(c).Height(36).Label(label).TextColor(color).Children(func() { n.Text(c, label).FontSize(13).FixedLineHeight(20).PaddingY(8) })
			if e.tab == label {
				button.BorderWidth(0, 0, 1, 0).BorderColor(t.Text)
			}
			if button.Clicked() {
				e.tab = label
			}
		}
	})
	e.setup = true
	e.workerMinor = v.deviceState(f.key).WorkerMinor
	if f.key == "" {
		e.workerMinor = v.sshSetup.Info.Minor
	}
	_, height := c.Size()
	top := max(float32(112), min(height/2-208, 256))
	// Reserve the steps, heading, tabs, footer and bottom window inset.
	available := height - top - 48 - 64 - 52 - 64 - 24
	if add {
		// The SSH dialog also owns a title row and 24 DIP outer insets.
		available = height - 48 - 48 - 48 - 48 - 64 - 52 - 64
	}
	// Let the error's actual animated height shrink the body in the same layout pass.
	n.Column(c.Key("configuration-region")).FillWidth().MaxHeight(max(96, available)).Children(func() {
		n.Scroll(c.Key("agent-configuration-scroll:" + e.tab)).Width(f.width + 16).MarginX(-8).PaddingX(8).MinHeight(0).Shrink(1).Label("Agent 配置内容").Children(func() {
			v.agentConfiguration(c.Key("agent-tab:"+e.tab), t, f.key, true, e, info, true, func() {})
		})
		n.Scroll(c.Key("agent-configuration-error")).FillWidth().MaxHeight(60).Shrink(0).Children(func() { v.welcomeError(c, e.error) })
	})
	v.welcomeFooter(c, "取消", "保存", e.busy, func() { e.edit(info); e.openID = ""; v.welcomeStep(welcomeAgents, -1) }, func() {
		options, err := e.draft.options()
		if err != nil {
			e.error = err.Error()
			return
		}
		e.paths[info.ID] = strings.TrimSpace(e.path)
		if e.workerMinor >= 18 {
			e.options[info.ID] = options
		}
		e.openID = ""
		v.welcomeStep(welcomeAgents, -1)
	})
}

func (v *View) recheckWelcomeAgent(key, id string) {
	f := v.welcome
	if v.Controller == nil || f == nil || f.agents == nil || f.agents.loading || f.agents.busy {
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
		job = app.Job{Method: "$device.check", Input: app.CheckSSHDevice{Name: v.sshName, SSH: transport.SSH{Target: strings.TrimSpace(v.sshTarget), Port: port, Worker: v.sshSetup.Info.Worker, StateDirectory: v.sshSetup.Info.Directory}}}
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
