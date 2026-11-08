package ui

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

// Closing cancels presentation immediately; the in-flight check retains its
// own completion until the controller acknowledges cancellation or returns.
type sshCheckPhase uint8

const (
	sshClosed sshCheckPhase = iota
	sshEditing
	sshChecking
	sshCanceledCheck
	sshSelecting
	sshSaving
)

func (s sshCheckPhase) open() bool {
	return s == sshEditing || s == sshChecking || s == sshSelecting || s == sshSaving
}
func (s sshCheckPhase) busy() bool {
	return s == sshChecking || s == sshCanceledCheck || s == sshSaving
}
func (s sshCheckPhase) show() sshCheckPhase {
	if s.busy() {
		return sshChecking
	}
	return sshEditing
}
func (s sshCheckPhase) begin() sshCheckPhase {
	if s.open() {
		return sshChecking
	}
	return sshCanceledCheck
}
func (s sshCheckPhase) finish() sshCheckPhase {
	if s == sshSaving {
		return sshSelecting
	}
	if s.open() {
		return sshEditing
	}
	return sshClosed
}
func (s sshCheckPhase) close() sshCheckPhase {
	if s.busy() {
		return sshCanceledCheck
	}
	return sshClosed
}

var deviceBackIcon = svg("M12 5l-5 5 5 5")

var workspaceIcon = svg("M2 5h6l2 2h8v10H2z")

func (v *View) deviceState(key string) *app.DeviceState {
	if state := v.Model.DeviceStates[key]; state != nil {
		return state
	}
	if key == "local" && len(v.Model.DeviceStates) == 0 {
		return &app.DeviceState{Connected: v.Model.Connected, Readiness: v.Model.Readiness, Snapshot: v.Model.Snapshot}
	}
	return &app.DeviceState{}
}
func (v *View) deviceView(c *n.Context, t *n.Theme) {
	m := v.Model
	width, _ := c.Size()
	narrow := width < 900
	if v.deviceSelected == "" {
		v.deviceSelected = m.ConnectionID()
	}
	n.Row(c).Fill().Children(func() {
		if !narrow || v.deviceList {
			collection := n.Column(c.Key("device-collection")).Width(232).FillHeight().Padding(8).Label("设备列表")
			if narrow {
				collection.FillWidth().Grow(1)
			}
			collection.Children(func() {
				surface := t.Surface
				if t.Dark {
					surface = n.Hex("#1c1c1c")
				}
				n.Column(c).Fill().Radius(14).Padding(0, 8, 8).Background(surface).Border(1, t.Border).Gap(4).Children(func() {
					n.Row(c).FillWidth().Height(44).DragWindow()
					if navButton(c, deviceBackIcon, "返回会话").TextColor(t.TextMuted).Clicked() {
						v.devices = false
					}
					n.Row(c).FillWidth().Padding(8, 4).Gap(4).AlignItems(n.Center).Children(func() {
						textField(c.Key("device-search"), &v.deviceSearch).MinWidth(0).Grow(1).Height(28).FontSize(12).Label("搜索设备").Placeholder("搜索设备").Background(t.Background)
						add := iconButton(c, plus, "添加设备")
						v.choiceMenu(c, t, "add-device", add, []menuChoice{{id: "ssh", name: "直接 SSH", choose: func() { v.sshCheck = v.sshCheck.show(); v.sshError = "" }}, {id: "tailnet", name: "从 Tailnet 添加", disabled: true}})
					})
					n.Scroll(c).FillWidth().Grow(1).Children(func() {
						row := func(key, name, address string) {
							if q := strings.ToLower(strings.TrimSpace(v.deviceSearch)); q != "" && !strings.Contains(strings.ToLower(name+" "+address), q) {
								return
							}
							state := v.deviceState(key)
							btn := control(c.Key("device:"+key), 6).FillWidth().Height(52).Padding(6, 10).Radius(7).Label("选择设备：" + name).Tooltip(name)
							if key == v.deviceSelected {
								btn.Background(t.SurfacePressed)
							}
							btn.Children(func() {
								n.Row(c).FillWidth().Gap(10).AlignItems(n.Baseline).Children(func() {
									inlineSymbol(c, monitor, name, n.Font{Family: t.Font, Size: 13, Weight: 400}, 16, t.TextMuted)
									n.Column(c).Grow(1).Children(func() {
										weight := 400
										if key == v.deviceSelected {
											weight = 500
										}
										n.Text(c, name).FontSize(13).FontWeight(weight).FixedLineHeight(20).SingleLine()
										n.Row(c).Gap(5).AlignItems(n.Baseline).Children(func() {
											label := "这台 Mac"
											if key != "local" {
												label = "SSH · " + deviceLatency(state, c.Now())
												color := t.TextMuted
												if state.Connected {
													color = t.Success
												}
												statusDot(c, label, n.Font{Family: t.Font, Size: 11, Weight: 400}, color, false)
											}
											n.Text(c, label).FontSize(11).FixedLineHeight(16).TextColor(t.TextMuted)
										})
									})
								})
							})
							if btn.Clicked() {
								v.deviceSelected = key
								v.deviceList = false
								v.deviceTab = ""
							}
						}
						row("local", m.LocalName(), "")
						for _, connection := range m.Connections {
							row(connection.ID, connection.Name, connection.SSH.Target)
						}
					})
					deviceDivider(c)
					ghost(c, "Tailnet").FillWidth().Height(32).PaddingX(10).FontSize(12).TextColor(t.TextMuted).Disabled(true).Tooltip("尚未配置")
				})
			})
		}
		if !narrow || !v.deviceList {
			n.Column(c.Key("device-pane")).FillHeight().Grow(1).Children(func() {
				if v.sshCheck.open() {
					n.Row(c).Height(56).PaddingX(32).DragWindow().Children(func() {
						if deviceAction(c, "返回设备").Clicked() {
							v.cancelSSHCheck()
						}
					})
					n.Scroll(c).FillWidth().Grow(1).Padding(0, 32, 24).Children(func() { v.sshDeviceForm(c.Key("ssh-onboarding"), t) })
				} else {
					v.deviceDetail(c.Key("device-detail:"+v.deviceSelected), t, v.deviceSelected)
				}
			})
		}
	})
}

func (v *View) deviceDialogs(c *n.Context, t *n.Theme) {
	dialog := func(open *bool, key string, body func()) n.Element {
		return n.DialogBase(c.Key(key), open, func(backdrop, panel n.Element) {
			backdrop.Background(n.RGBA(0, 0, 0, .16))
			width, _ := c.Size()
			panel.Width(min(420, width-48)).Padding(20).Gap(12).Radius(12).Background(raised(t)).Border(1, t.Border).Shadow(0, 8, 28, 0, n.RGBA(0, 0, 0, .16)).Transition(popupMotion(c)).Children(body)
		})
	}

	dialog(&v.remoteWorkspaceOpen, "remote-workspace-dialog", func() {
		n.Text(c, "新建远程工作区").FontSize(14).FontWeight(500)
		n.Text(c, "主目录").FontSize(12).TextColor(t.TextMuted)
		textField(c.Key("remote-main"), &v.remoteWorkspacePath).FillWidth().Height(32).FontSize(13).Label("远程主目录").Placeholder("该 Mac 上的绝对路径").Disabled(v.remoteWorkspaceBusy)
		n.Text(c, "名称").FontSize(12).TextColor(t.TextMuted)
		textField(c.Key("remote-name"), &v.remoteWorkspaceName).FillWidth().Height(32).FontSize(13).Label("工作区名称").Placeholder("使用目录名称").Disabled(v.remoteWorkspaceBusy)
		n.Text(c, "附加目录").FontSize(12).TextColor(t.TextMuted)
		n.TextAreaBase(c.Key("remote-extra"), &v.remoteWorkspaceExtra).FillWidth().Lines(2, 4).Padding(8).Radius(6).Background(t.Surface).Border(1, t.Border).FontSize(12).FixedLineHeight(20).Label("远程附加目录").Placeholder("可选，每行一个绝对路径").Disabled(v.remoteWorkspaceBusy)
		if v.remoteWorkspaceError != "" {
			n.Text(c, v.remoteWorkspaceError).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		}
		n.Row(c).FillWidth().Gap(8).Justify(n.End).Children(func() {
			if ghost(c, "取消").Disabled(v.remoteWorkspaceBusy).Clicked() {
				v.remoteWorkspaceOpen = false
			}
			if n.PrimaryButton(c, "创建").Label("创建远程工作区").Height(28).Radius(6).FontSize(13).Disabled(v.remoteWorkspaceBusy || strings.TrimSpace(v.remoteWorkspacePath) == "").Clicked() {
				v.submitRemoteWorkspace()
			}
		})
	})
}
func (v *View) cancelSSHCheck() {
	v.sshCheck = v.sshCheck.close()
	v.sshRequest = ""
	if v.sshCheck.busy() && v.Controller != nil {
		v.Controller.Do(app.Job{Method: "$device.cancel"})
	}
}
func (v *View) checkSSHDevice() {
	port := 0
	if text := strings.TrimSpace(v.sshPort); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 65535 {
			v.sshError = "请输入有效的 SSH 端口"
			return
		}
		port = n
	}
	ssh := transport.SSH{Target: strings.TrimSpace(v.sshTarget), Port: port}
	if err := ssh.Validate(); err != nil {
		v.sshError = err.Error()
		return
	}
	request := domain.NewID()
	v.sshRequest = request
	v.sshCheck = v.sshCheck.begin()
	v.sshError = ""
	name := strings.TrimSpace(v.sshName)
	if name == "" {
		name = ssh.Target
	}
	if !v.Controller.Do(app.Job{Method: "$device.check", Input: app.CheckSSHDevice{Name: name, SSH: ssh}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		v.sshCheck = v.sshCheck.finish()
		if v.sshRequest != request || !v.sshCheck.open() {
			return
		}
		if err != nil {
			v.sshError = err.Error() + "；请确认已在终端核对主机指纹并可免交互登录"
			return
		}
		var result app.DeviceSetupCheck
		if err = json.Unmarshal(b, &result); err != nil {
			v.sshError = err.Error()
			return
		}
		if result.Info.DeviceID == m.LocalDeviceID {
			v.sshError = "这个地址连接的是这台 Mac"
			return
		}
		v.sshSetup = result
		v.sshAgents = agentEditor(result.Catalog)
		v.sshCheck = sshSelecting

	}}) {
		v.sshCheck = v.sshCheck.finish()
		v.sshError = "操作繁忙，请重试"
	}
}

func (v *View) beginWorkspace(key string) {
	if v.Controller == nil {
		return
	}
	if key != "local" {
		v.remoteWorkspaceConnection = key
		v.remoteWorkspaceRequest = domain.NewID()
		v.remoteWorkspaceBusy = false
		v.remoteWorkspaceOpen = true
		v.remoteWorkspaceError = ""
		return
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择工作区目录", Directory: true, Multiple: true})
	if err != nil {
		v.Model.Error = err.Error()
		return
	}
	if len(paths) == 0 {
		return
	}
	v.createWorkspaceOn(key, protocol.WorkspaceInput{Name: path.Base(paths[0]), Folders: paths})
}
func (v *View) submitRemoteWorkspace() {
	folders := []string{strings.TrimSpace(v.remoteWorkspacePath)}
	for _, line := range strings.Split(v.remoteWorkspaceExtra, "\n") {
		if folder := strings.TrimSpace(line); folder != "" {
			folders = append(folders, folder)
		}
	}
	for _, folder := range folders {
		if !path.IsAbs(folder) {
			v.remoteWorkspaceError = "请填写目标 Mac 上的绝对路径"
			return
		}
	}
	name := strings.TrimSpace(v.remoteWorkspaceName)
	if name == "" {
		name = path.Base(folders[0])
	}
	v.remoteWorkspaceBusy = true
	v.createWorkspaceOn(v.remoteWorkspaceConnection, protocol.WorkspaceInput{Name: name, Folders: folders})
}
func (v *View) createWorkspaceOn(key string, input protocol.WorkspaceInput) {
	request := v.remoteWorkspaceRequest
	if !v.do(app.Job{ConnectionID: key, Method: "workspace.create", Input: input, Apply: func(m *app.Model, b json.RawMessage, err error) {
		current := key == "local" || v.remoteWorkspaceOpen && v.remoteWorkspaceConnection == key && v.remoteWorkspaceRequest == request
		if current {
			v.remoteWorkspaceBusy = false
		}
		if err != nil {
			if !current {
				return
			}
			v.remoteWorkspaceError = err.Error()
			m.Error = err.Error()
			return
		}
		var w domain.Workspace
		if err = json.Unmarshal(b, &w); err != nil {
			m.Error = fmt.Sprint(err)
			return
		}
		if state := m.DeviceStates[key]; state != nil {
			found := false
			for i, old := range state.Snapshot.Workspaces {
				if old.ID == w.ID {
					state.Snapshot.Workspaces[i] = w
					found = true
					break
				}
			}
			if !found {
				state.Snapshot.Workspaces = append(state.Snapshot.Workspaces, w)
			}
		}
		if current {
			m.WorkspaceID = w.ID
			m.SessionID = ""
			m.ActiveConnection = key
			m.RefreshDevices()
			v.remoteWorkspaceOpen = false
			v.devices = false
		}
	}}) {
		v.remoteWorkspaceBusy = false
		v.remoteWorkspaceError = "操作繁忙，请重试"
	}
}

func deviceLatency(state *app.DeviceState, now time.Time) string {
	if !state.Connected {
		return "—"
	}
	if state.Timing == nil || now.Sub(state.Timing.ObservedAt) > 30*time.Second {
		return "待测"
	}
	return fmt.Sprintf("%.1f ms", state.Timing.Milliseconds)
}
