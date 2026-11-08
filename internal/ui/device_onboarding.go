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
	"time"
)

func (v *View) sshDeviceForm(c *n.Context, t *n.Theme) {
	n.Row(c).FillWidth().Height(40).AlignItems(n.Center).Children(func() {
		n.Text(c, "添加设备").FontSize(20).FontWeight(500).Grow(1)
		if deviceAction(c, "取消").Label("取消添加设备").Clicked() {
			v.cancelSSHCheck()
		}
	})
	field := func(label, placeholder string, value *string) {
		n.Column(c.Key(label)).FillWidth().Gap(8).Children(func() {
			n.Text(c, label).FontSize(12).TextColor(t.TextMuted)
			textField(c, value).FillWidth().Height(30).FontSize(13).Label(label).Placeholder(placeholder).Disabled(v.sshCheck.busy() || v.sshCheck == sshSelecting)
		})
	}
	n.Column(c).FillWidth().Gap(16).Children(func() {
		field("设备名称", "例如 MacBook Air", &v.sshName)
		field("SSH 地址", "用户名@主机或别名", &v.sshTarget)
		field("SSH 端口", "使用 SSH 配置", &v.sshPort)
	})
	if v.sshCheck == sshSelecting || v.sshCheck == sshSaving {
		e := v.sshAgents
		n.Column(c).FillWidth().Gap(4).Transition(disclosureMotion(c, 180*time.Millisecond)).Children(func() {
			deviceHeading(c, "Agent", nil)
			if e != nil {
				v.agentRows(c.Key("agents"), t, "", true, e, true, func() {})
			}
		})
	}
	if v.sshError != "" {
		n.Text(c, v.sshError).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
	}
	n.Row(c).FillWidth().Justify(n.End).Gap(6).Children(func() {
		selecting := v.sshCheck == sshSelecting || v.sshCheck == sshSaving
		if selecting && deviceAction(c, "返回").Disabled(v.sshCheck.busy()).Clicked() {
			v.sshCheck = sshEditing
		}
		label := "检查连接"
		if selecting {
			label = "检查并添加"
		}
		if v.sshCheck.busy() {
			label = "正在检查…"
		}
		if deviceAction(c, label).Disabled(v.sshCheck.busy() || v.Controller == nil || strings.TrimSpace(v.sshTarget) == "").Clicked() {
			if selecting {
				v.completeSSHDevice()
			} else {
				v.checkSSHDevice()
			}
		}
	})
}
func (v *View) completeSSHDevice() {
	if v.sshAgents == nil {
		return
	}
	if v.sshAgents.openID != "" {
		v.sshAgents.paths[v.sshAgents.openID] = v.sshAgents.path
	}
	input := v.sshAgents.input()
	if len(input.Agents) == 0 {
		v.sshError = "请至少选择一个 Agent"
		return
	}
	info := v.sshSetup.Info
	ssh := transport.SSH{Target: strings.TrimSpace(v.sshTarget), Worker: info.Worker, StateDirectory: info.Directory}
	// Preserve the already validated port from the first check.
	ssh.Port, _ = strconv.Atoi(strings.TrimSpace(v.sshPort))
	request := domain.NewID()
	v.sshRequest = request
	v.sshCheck = sshSaving
	v.sshError = ""
	v.sshAgents.busy = true
	if !v.Controller.Do(app.Job{Method: "$device.complete", Input: app.CompleteSSHDevice{SSH: ssh, DeviceID: info.DeviceID, Agents: input}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		v.sshCheck = v.sshCheck.finish()
		if v.sshAgents != nil {
			v.sshAgents.busy = false
		}
		if v.sshRequest != request || !v.sshCheck.open() {
			return
		}
		if err != nil {
			v.sshError = err.Error()
			return
		}
		var result protocol.AgentSetupResult
		if err = json.Unmarshal(b, &result); err != nil {
			v.sshError = "设备返回的配置结果无效"
			return
		}
		if !result.Ready {
			v.sshError = result.Error
			return
		}
		name := strings.TrimSpace(v.sshName)
		if name == "" {
			name = ssh.Target
		}
		connection := app.DeviceConnection{ID: domain.NewID(), Name: name, Home: info.Home, DeviceID: info.DeviceID, SSH: ssh}
		found := false
		for i, old := range m.Connections {
			if old.DeviceID == info.DeviceID {
				connection.ID = old.ID
				m.Connections[i] = connection
				found = true
				break
			}
		}
		if !found {
			m.Connections = append(m.Connections, connection)
		}
		var pending []string
		for id, key := range m.PendingDevices {
			if key == connection.ID {
				pending = append(pending, id)
			}
		}
		v.Controller.Do(app.Job{Method: "$device.configure", Input: app.ConfigureDevice{Connection: connection, Pending: pending}})
		if v.devices {
			v.deviceSelected = connection.ID
			v.deviceList = false
		}
		v.sshCheck = sshClosed
	}}) {
		v.sshCheck = sshSelecting
		v.sshAgents.busy = false
		v.sshError = "设备操作繁忙，请重试"
	}
}
