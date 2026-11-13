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

func (v *View) sshDeviceForm(c *n.Context, t *n.Theme) {
	f := v.setupFlow()
	f.width = 432
	if f.step == welcomeDevice {
		f.step = welcomeSSH
		f.key = ""
	}
	n.Column(c).FillWidth().Children(func() {
		n.Row(c).FillWidth().Height(configurationTitleLine).AlignItems(n.Center).Margin(0, 0, configurationHeadingGap).Children(func() {
			n.Text(c, "添加设备").FontSize(14).FontWeight(400).FixedLineHeight(configurationTitleLine).Grow(1)
			if iconButton(c, closeIcon, "取消添加设备").Clicked() {
				v.cancelSSHCheck()
				v.welcome = nil
			}
		})
		_, height := c.Size()
		n.Scroll(c.Key("ssh-body")).Width(448).MarginX(-8).PaddingX(8).MaxHeight(max(160, height-144)).Label("SSH 设置内容").Children(func() { v.welcomeSteps(c, t, f, true); v.welcomeStage(c, t, f, true) })
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
	ssh := v.setupSSH(0)
	ssh.Worker = info.Worker
	ssh.StateDirectory = info.Directory
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
			v.sshAgents.refresh(result.Catalog)
			return
		}
		name := strings.TrimSpace(v.sshName)
		if name == "" {
			name = ssh.Target
		}
		v.saveDeviceConnection(newDeviceConnection(name, info, ssh), false)
	}}) {
		v.sshCheck = sshSelecting
		v.sshAgents.busy = false
		v.sshError = "设备操作繁忙，请重试"
	}
}
