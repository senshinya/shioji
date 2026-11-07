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
)

func (s sshCheckPhase) open() bool { return s == sshEditing || s == sshChecking }
func (s sshCheckPhase) busy() bool { return s == sshChecking || s == sshCanceledCheck }
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
	narrow := width < 1100
	if v.deviceSelected == "" {
		v.deviceSelected = m.ConnectionID()
	}
	n.Row(c).FillWidth().FillHeight().Grow(1).Children(func() {
		if !narrow || v.deviceList {
			collection := n.Column(c).FillHeight().Padding(16, 24, 16, 32).Gap(12).Background(t.Background).Label("设备列表")
			if narrow {
				collection.FillWidth().Grow(1)
			} else {
				collection.Width(300)
			}
			collection.Children(func() {
				textField(c.Key("device-search"), &v.deviceSearch).FillWidth().Height(28).FontSize(12).Label("搜索设备").Placeholder("搜索设备…")
				n.Scroll(c).FillWidth().FillHeight().Grow(1).Children(func() {
					row := func(key, name, address string) {
						if query := strings.ToLower(strings.TrimSpace(v.deviceSearch)); query != "" && !strings.Contains(strings.ToLower(name+" "+address), query) {
							return
						}
						state := v.deviceState(key)
						button := control(c.Key("device:"+key), 6).Radius(6).FillWidth().Height(32).PaddingX(8).Label("选择设备：" + name).Tooltip(name)
						if v.deviceSelected == key {
							button.Background(t.SurfacePressed)
						}
						button.Children(func() {
							n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
								inlineSymbol(c, monitor, name, n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}, 16, t.TextMuted)
								n.Text(c, name).SingleLine().FontSize(13).FixedLineHeight(20).Grow(1)
								running := 0
								for _, s := range state.Snapshot.Sessions {
									if s.Status.Active() {
										running++
									}
								}
								count := ""
								if running > 0 {
									count = strconv.Itoa(running)
								}
								n.Text(c, count).Width(16).TextAlign(n.End).FontSize(11).FontFeatures("tnum").TextColor(t.TextMuted)
								if key != "local" {
									n.Text(c, deviceLatency(state, c.Now())).Width(52).TextAlign(n.End).SingleLine().FontSize(11).FontFeatures("tnum").TextColor(t.TextMuted).Tooltip("执行服务往返耗时")
								}
								color := t.TextMuted
								if state.Connected {
									color = t.Success
								}
								n.Column(c).Size(4, 4).Radius(2).Background(color).AlignSelf(n.Center)
							})
						})
						if button.Clicked() {
							v.deviceSelected = key
							v.deviceList = false
						}
					}
					ghost(c, "应用内 Tailnet").FillWidth().Height(32).FontSize(12).TextColor(t.TextMuted).Disabled(true).Tooltip("尚未配置")
					n.Text(c, "本机").FontSize(11).TextColor(t.TextMuted).Padding(12, 8, 4)
					row("local", "这台 Mac", "")
					if len(m.Connections) > 0 {
						n.Text(c, "远程").FontSize(11).TextColor(t.TextMuted).Padding(16, 8, 4)
					}
					for _, connection := range m.Connections {
						row(connection.ID, connection.Name, connection.SSH.Target)
					}
				})
			})
		}
		if !narrow {
			n.Column(c).Width(1).FillHeight().Background(t.Border)
		}
		if !narrow || !v.deviceList {
			n.Scroll(c).FillHeight().Grow(1).Children(func() {
				n.Column(c).FillWidth().Padding(20, 32).AlignItems(n.Center).Children(func() {
					n.Column(c).FillWidth().MaxWidth(680).Gap(24).Children(func() {
						if narrow {
							if ghost(c, "返回设备列表").FontSize(12).Clicked() {
								v.deviceList = true
							}
						}
						key := v.deviceSelected
						name, address, deviceID := "这台 Mac", "本地", m.LocalDeviceID
						port, user := "使用 SSH 配置", "使用 SSH 配置"
						for _, connection := range m.Connections {
							if connection.ID == key {
								name, address, deviceID = connection.Name, connection.SSH.Target, connection.DeviceID
								if at := strings.LastIndex(address, "@"); at >= 0 {
									user, address = address[:at], address[at+1:]
								}
								if connection.SSH.Port != 0 {
									port = strconv.Itoa(connection.SSH.Port)
								}
							}
						}
						state := v.deviceState(key)
						status := "未连接"
						statusColor := t.TextMuted
						if state.Connected {
							status = "已连接"
							statusColor = t.Success
						}
						n.Row(c).FillWidth().Height(64).Gap(16).AlignItems(n.Center).Children(func() {
							n.Column(c).Size(64, 64).Radius(16).Background(t.Surface).AlignItems(n.Center).Justify(n.Center).Children(func() { n.Icon(c, monitor).Size(32, 32).TextColor(t.Text) })
							n.Column(c).Grow(1).Gap(6).Children(func() {
								n.Row(c).Gap(10).AlignItems(n.Center).Children(func() {
									n.Text(c, name).FontSize(20).FontWeight(500)
									n.Column(c).Size(4, 4).Radius(2).Background(statusColor)
									n.Text(c, status).FontSize(11).TextColor(t.TextMuted)
								})
								n.Text(c, "macOS · Apple silicon").FontSize(11).TextColor(t.TextMuted)
							})
						})
						meta := func(label, value string) {
							n.Column(c).FillWidth().Children(func() {
								n.Row(c).FillWidth().MinHeight(32).Gap(16).AlignItems(n.Baseline).Children(func() {
									n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).PaddingY(6)
									n.Spacer(c).Grow(1)
									n.Text(c, value).FontSize(12).FixedLineHeight(20).TextAlign(n.End).MaxWidth(400).PaddingY(6).Selectable()
								})
								n.Column(c).Height(1).FillWidth().Background(t.Border)
							})
						}
						n.Column(c).FillWidth().Gap(8).Children(func() {
							n.Row(c).FillWidth().AlignItems(n.Center).Children(func() {
								n.Text(c, "连接").FontSize(13).Grow(1)
								if ghost(c, "重新连接").FontSize(12).Disabled(v.Controller == nil).Clicked() {
									v.do(app.Job{ConnectionID: key, Method: "$reconnect"})
								}
							})
							n.Column(c).FillWidth().Children(func() {
								if key != "local" {
									meta("连接方式", "SSH")
									meta("主机", address)
									meta("登录身份", user)
									meta("端口", port)
									meta("往返耗时", deviceLatency(state, c.Now()))
								} else {
									meta("连接方式", "本地")
								}
								if deviceID != "" {
									meta("设备 ID", deviceID)
								}
							})
						})
						if state.Error != "" {
							n.Text(c, state.Error).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
						}
						n.Column(c).FillWidth().Gap(8).Children(func() {
							n.Text(c, "运行环境").FontSize(13)
							workerStatus := "未连接"
							if state.Connected {
								workerStatus = "已就绪"
							}
							meta("Shioji worker", workerStatus)
							runtime := "未就绪"
							if state.Readiness.State == "ready" {
								runtime = "已就绪"
								if !state.Connected {
									runtime = "上次已就绪"
								}
							}
							if len(state.Readiness.Harnesses) > 0 {
								for _, info := range state.Readiness.Harnesses {
									status := "未就绪"
									if info.State == domain.RuntimeReady {
										status = "已就绪"
										if !state.Connected {
											status = "上次已就绪"
										}
									}
									meta(info.Name, status)
								}
							} else {
								meta(state.Readiness.Descriptor().Name, runtime)
							}
							if state.Readiness.Error != "" {
								n.Text(c, state.Readiness.Error).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
							}
						})
						n.Column(c).FillWidth().Gap(8).Children(func() {
							n.Row(c).FillWidth().AlignItems(n.Center).Children(func() {
								n.Text(c, "工作区").FontSize(13).Grow(1)
								if iconButton(c, plus, "新建工作区").Disabled(!state.Connected || v.Controller == nil).Clicked() {
									v.beginWorkspace(key)
								}
							})
							count := 0
							for _, w := range m.Snapshot.Workspaces {
								if w.DeviceID == deviceID || (key == "local" && w.DeviceID == "") {
									count++
									entry := control(c, 6).FillWidth().MinHeight(48).Padding(6, 8).Radius(6).Label("打开工作区：" + w.Name).ContextMenu(func(menu *n.Menu) { v.workspaceMenu(menu, w) }).Children(func() {
										n.Row(c).FillWidth().Gap(12).AlignItems(n.Center).Children(func() {
											n.Icon(c, workspaceIcon).Size(18, 18).TextColor(t.TextMuted)
											n.Column(c).Grow(1).Gap(3).Children(func() {
												n.Text(c, w.Name).FontSize(13)
												n.Text(c, fmt.Sprintf("%d 个目录", len(w.Folders))).FontSize(11).TextColor(t.TextMuted)
											})
											n.Icon(c, disclosureRight).Size(12, 12).TextColor(t.TextMuted)
										})
									})
									if entry.Clicked() {
										m.WorkspaceID = w.ID
										m.ActiveConnection = key
										v.home()
									}
								}
							}
							if count == 0 {
								n.Text(c, "暂无工作区").FontSize(12).TextColor(t.TextMuted).PaddingY(8)
							}
						})
					})
				})
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
	sshOpen := v.sshCheck.open()
	sshDialog := dialog(&sshOpen, "add-ssh-dialog", func() {
		n.Text(c, "添加 SSH 设备").FontSize(14).FontWeight(500)
		field := func(label, placeholder string, value *string) {
			n.Text(c, label).FontSize(12).TextColor(t.TextMuted)
			textField(c.Key(label), value).FillWidth().Height(32).FontSize(13).Label(label).Placeholder(placeholder).Disabled(v.sshCheck.busy())
		}
		field("设备名称", "可选", &v.sshName)
		field("SSH 地址", "用户名@主机或 SSH 别名", &v.sshTarget)
		field("SSH 端口", "使用 SSH 配置", &v.sshPort)
		if v.sshError != "" {
			n.Text(c, v.sshError).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		}
		n.Row(c).FillWidth().Gap(8).Justify(n.End).Children(func() {
			if ghost(c, "取消").Clicked() {
				v.cancelSSHCheck()
			}
			label := "连接"
			if v.sshCheck.busy() {
				label = "检查中…"
			}
			if n.PrimaryButton(c, label).Height(28).Radius(6).FontSize(13).Disabled(v.sshCheck.busy() || strings.TrimSpace(v.sshTarget) == "" || v.Controller == nil).Clicked() {
				v.checkSSHDevice()
			}
		})
	})
	if sshDialog.Dismissed() {
		v.cancelSSHCheck()
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
		var info transport.WorkerInfo
		if err = json.Unmarshal(b, &info); err != nil {
			v.sshError = err.Error()
			return
		}
		if info.DeviceID == m.LocalDeviceID {
			v.sshError = "这个地址连接的是这台 Mac"
			return
		}
		connection := app.DeviceConnection{ID: domain.NewID(), Name: name, Home: info.Home, DeviceID: info.DeviceID, SSH: ssh}
		connection.SSH.Worker = info.Worker
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
		v.deviceSelected = connection.ID
		v.deviceList = false
		v.devices = true
		v.sshCheck = v.sshCheck.close()
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
