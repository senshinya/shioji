package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/transport"
)

var deviceSSHMark = svg("M3 2.2l7.8 7.8L3 17.8M12 17.8h5")

type deviceConnectionSave struct {
	connection                 app.DeviceConnection
	before                     *app.DeviceConnection
	merged, duplicate, removal bool
	request                    string
	result                     <-chan error
}
type devicePathNotice struct {
	text string
	at   time.Time
}

func (v *View) saveDeviceConnection(connection app.DeviceConnection, removal bool) {
	if v.deviceConnectionSave != nil {
		return
	}
	m := v.Model
	var before *app.DeviceConnection
	for _, old := range m.Connections {
		if old.DeviceID == connection.DeviceID && old.DeviceID != "" {
			copy := old.Normalized()
			before = &copy
			break
		}
	}
	duplicate := false
	if before != nil && !removal {
		incoming := connection.Normalized()
		duplicate = true
		for _, r := range incoming.Routes {
			found := false
			for _, old := range before.Routes {
				if old.ID == r.ID {
					found = true
				}
			}
			if !found {
				duplicate = false
			}
		}
	}
	if removal {
		for i := range m.Connections {
			if m.Connections[i].ID == connection.ID {
				m.Connections[i] = connection.Normalized()
			}
		}
	} else {
		if v.welcome != nil && connection.Icon == app.DeviceIconAuto {
			connection.Icon = v.welcome.icon
		}
		if connection.Icon == app.DeviceIconAuto {
			connection.Icon = v.deviceDefaultIcon("")
		}
		connection = m.BindDeviceConnection(connection)
	}
	result := make(chan error, 1)
	pending := &deviceConnectionSave{connection: connection, before: before, merged: before != nil, duplicate: duplicate, removal: removal, request: v.sshRequest, result: result}
	v.deviceConnectionSave = pending
	if !removal {
		v.sshCheck = sshSaving
	}
	data, err := m.EncodeState()
	if err != nil || v.Saver == nil {
		result <- err
		v.syncDeviceConnectionSave()
		return
	}
	writer, invalidate := v.Saver, v.services.Invalidate
	// Queue on the UI ordering boundary. The goroutine only waits for disk;
	// it must never re-queue an older captured whole-client snapshot.
	writer.Queue(data)
	go func() {
		result <- writer.FlushLatest()
		if invalidate != nil {
			invalidate()
		}
	}()
}

func (v *View) syncDeviceConnectionSave() {
	p := v.deviceConnectionSave
	if p == nil {
		return
	}
	var err error
	select {
	case err = <-p.result:
	default:
		return
	}
	v.deviceConnectionSave = nil
	if err != nil {
		for i, c := range v.Model.Connections {
			if c.ID == p.connection.ID {
				if p.before != nil {
					old := *p.before
					for _, r := range old.Routes {
						if r.ID == c.ActiveRouteID {
							old.ActiveRouteID = r.ID
						}
					}
					v.Model.Connections[i] = old
				} else {
					v.Model.Connections = append(v.Model.Connections[:i], v.Model.Connections[i+1:]...)
				}
				break
			}
		}
		v.sshError = "无法保存连接：" + err.Error()
		if !p.removal {
			v.sshCheck = sshEditing
		} else {
			v.Model.Error = v.sshError
		}
		return
	}
	var pending []string
	for id, key := range v.Model.PendingDevices {
		if key == p.connection.ID {
			pending = append(pending, id)
		}
	}
	if v.Controller != nil && !v.Controller.Do(app.Job{Method: "$device.configure", Input: app.ConfigureDevice{Connection: p.connection, Pending: pending}}) {
		v.sshError = "连接已保存，设备操作繁忙，请重试"
		if !p.removal {
			v.sshCheck = sshEditing
		} else {
			v.Model.Error = v.sshError
		}
		return
	}
	if p.removal {
		v.pathNotice = devicePathNotice{"连接已移除", v.frameTime}
		return
	}
	// Saving is already accepted, but cancelling the dialog must not later
	// steal navigation or reopen it when the disk result arrives.
	if p.request != v.sshRequest {
		return
	}
	if v.devices {
		v.deviceSelected = p.connection.ID
		v.deviceList = false
		if p.merged {
			v.deviceTab = "settings"
		}
	}
	v.retireSSHSetupTerminal()
	v.sshCheck = sshClosed
	v.sshExpectedDevice = ""
	if f := v.welcome; f != nil && f.key == "" {
		if v.Model.FirstRun {
			f.key = p.connection.ID
			v.welcomeStep(welcomeWorkspace, 1)
		} else {
			v.welcome = nil
		}
	}
	if p.merged {
		text := "已识别为 " + p.connection.Name + "，连接已添加"
		if p.duplicate {
			text = "已识别为 " + p.connection.Name + "，该连接已存在"
		}
		v.pathNotice = devicePathNotice{text, v.frameTime}
	}
}

func (v *View) devicePathFeedback(c *n.Context, t *n.Theme) {
	remaining := 4*time.Second - c.Now().Sub(v.pathNotice.at)
	n.Overlay(c.Key("device-path-feedback"), func() {
		if v.pathNotice.text == "" || remaining <= 0 {
			return
		}
		c.After(remaining)
		width, _ := c.Size()
		right := float32(configurationInset)
		if v.devices && width >= 900 {
			right += max(0, width-232-2*configurationInset-840)
		} else if v.Model.FirstRun {
			right = max(right, (width-560)/2)
		}
		n.Row(c).Absolute().Right(right).Bottom(24).MaxWidth(max(120, width-48)).Gap(8).AlignItems(n.Baseline).Padding(8, 12).Radius(8).Background(raised(t)).Border(1, t.Border).Transition(fadeMotion(c, 180*time.Millisecond)).Children(func() {
			font := n.Font{Family: t.Font, Size: 12, Weight: 400}
			inlineSymbol(c, check, v.pathNotice.text, font, 14, t.TextMuted)
			n.Text(c, v.pathNotice.text).FontSize(12).FixedLineHeight(20).Shrink(1).Label("设备连接提示")
		})
	})
}

func (v *View) mergeKnownDevice(result app.DeviceSetupCheck) bool {
	if result.Info.DeviceID == "" {
		return false
	}
	if v.sshExpectedDevice != "" && v.sshExpectedDevice != result.Info.DeviceID {
		v.sshError = "这条连接不属于所选设备"
		v.sshCheck = sshEditing
		return true
	}
	for _, old := range v.Model.Connections {
		if old.DeviceID != result.Info.DeviceID {
			continue
		}
		port, _ := strconv.Atoi(strings.TrimSpace(v.sshPort))
		ssh := v.setupWorkerSSH(port, result.Info)
		v.saveDeviceConnection(app.DeviceConnection{ID: old.ID, Name: old.Name, DeviceID: old.DeviceID, Home: result.Info.Home, SSH: ssh}, false)
		return true
	}
	return false
}

func pathTiming(s app.DevicePathStatus, now time.Time) string {
	if s.Timing == nil || now.Sub(s.Timing.ObservedAt) > 15*time.Second || s.Timing.ObservedAt.After(now) {
		return "—"
	}
	return fmt.Sprintf("%.0f ms", s.Timing.Milliseconds)
}
func pathPhaseLabel(phase app.DevicePathPhase) string {
	switch phase {
	case app.PathReady:
		return "就绪"
	case app.PathActive:
		return "使用中"
	case app.PathSwitching:
		return "正在切换"
	case app.PathUnavailable:
		return "不可用"
	}
	return "连接中"
}

func (v *View) devicePathsSection(c *n.Context, t *n.Theme, key string, state *app.DeviceState) {
	var connection app.DeviceConnection
	for _, saved := range v.Model.Connections {
		if saved.ID == key {
			connection = saved.Normalized()
			break
		}
	}
	if connection.ID == "" {
		return
	}
	n.Column(c).FillWidth().Children(func() {
		deviceHeading(c, "连接路径", func() {
			n.Row(c).Height(28).Gap(8).AlignItems(n.Center).Children(func() {
				font := n.Font{Family: t.Font, Size: 11, Weight: 400}
				n.Text(c, "自动选择").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted).Transform(n.Translation(0, inlineCenterOffset("自动选择", font)))
				add := iconButton(c, plus, "添加连接路径").MarginEnd(-6).Disabled(v.Controller == nil || v.deviceConnectionSave != nil)
				v.choiceMenu(c, t, "add-device-path:"+key, add, []menuChoice{{id: "ssh", name: "通过 SSH", icon: commandTerminalIcon, choose: func() {
					v.dismissDeviceSetup()
					v.sshCheck = sshEditing
					v.sshExpectedDevice = connection.DeviceID
				}}, {id: "tailnet", name: "通过 Tailnet", icon: tailscaleMark, choose: func() { v.dismissDeviceSetup(); v.beginTailnet(); v.sshExpectedDevice = connection.DeviceID }}})
			})
		})
		for index, route := range connection.Routes {
			s := app.DevicePathStatus{ID: route.ID, Phase: app.PathConnecting}
			for _, known := range state.Paths {
				if known.ID == route.ID {
					s = known
					break
				}
			}
			name, mark := "直连 SSH", deviceSSHMark
			if route.SSH.TailnetPeer != "" {
				name, mark = "Tailnet SSH", tailscaleMark
			}
			label := pathPhaseLabel(s.Phase)
			if !state.Connected && s.Phase == app.PathActive {
				label = "连接中"
				s.Phase = app.PathConnecting
			}
			row := n.Column(c.Key(route.ID)).FillWidth().Label("连接路径:" + route.ID).Transition(disclosureMotion(c, 180*time.Millisecond))
			open := n.Local(row, "details", func() bool { return false })
			row.ContextMenu(func(menu *n.Menu) {
				backup := false
				for _, other := range state.Paths {
					if other.ID != route.ID && (other.Phase == app.PathReady || other.Phase == app.PathActive) {
						backup = true
					}
				}
				if menu.Item("移除此连接").Disabled(len(connection.Routes) < 2 || v.deviceConnectionSave != nil || (route.ID == connection.ActiveRouteID && !backup)).Chosen() {
					v.removeDevicePath(connection, route.ID)
				}
			})
			row.Children(func() {
				if index < len(connection.Routes)-1 {
					n.Column(c).Absolute().Left(0).Right(0).Bottom(0).Height(1).Background(t.Border)
				}
				trigger := configurationRowControl(c.Key("header"), 6).Height(72).PaddingY(16).Label("展开连接路径:" + route.ID)
				trigger.Children(func() {
					n.Row(c).FillWidth().Gap(16).AlignItems(n.Baseline).Children(func() {
						font := n.Font{Family: t.Font, Size: 13, Weight: 400}
						inlineSymbol(c, mark, name, font, 16, t.TextMuted).Label("路径图标:" + route.ID)
						n.Column(c).MinWidth(0).Grow(1).Gap(2).Children(func() {
							n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
								n.Text(c, name).FontSize(13).FixedLineHeight(20).Label("路径名称:" + route.ID)
								angle := float32(-90)
								if *open {
									angle = 0
								}
								inlineSymbol(c, chevron, name, font, 12, t.TextMuted, angle)
							})
							address := devicePathAddress(route.SSH, s.Endpoint)
							n.Text(c, address).FontSize(11).FixedLineHeight(18).SingleLine().TextColor(t.TextMuted).Tooltip(address).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)}).Label("路径地址:" + route.ID)
						})
						n.Text(c, pathTiming(s, c.Now())).Width(64).TextAlign(n.End).FontSize(12).FixedLineHeight(20).FontFeatures("tnum").TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Numeric: true, Animation: animationPointer(c, 260*time.Millisecond)}).Label("路径延迟:" + route.ID)
						n.Row(c).Width(104).Gap(7).Justify(n.End).AlignItems(n.Baseline).Label("路径状态:" + route.ID).Tooltip(s.Error).Children(func() {
							font := n.Font{Family: t.Font, Size: 12, Weight: 400}
							if s.Phase == app.PathActive {
								inlineSymbol(c, check, label, font, 14, t.TextMuted).Transition(fadeMotion(c, 160*time.Millisecond))
							}
							if s.Phase == app.PathConnecting || s.Phase == app.PathSwitching {
								serviceSpinner(c.Key("path-spinner"), label, font, t.TextMuted)
							}
							n.Text(c.Key("caption"), label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
						})
					})
				})
				if trigger.Clicked() {
					*open = !*open
				}
				body := n.Column(c.Key("details")).FillWidth().Clip()
				animatedContentHeight(c, body, *open, 200*time.Millisecond, func() float32 {
					n.Column(c).FillWidth().Padding(0, 0, 8).Transition(disclosureMotion(c, 160*time.Millisecond)).Children(func() {
						v.devicePathDetails(c, route, s)
					})
					return 0
				})
			})
		}
		details := n.Column(c).FillWidth()
		open := n.Local(details, "device-identity", func() bool { return false })
		details.Children(func() {
			deviceDisclosure(c, "设备身份", open, func() {
				deviceMeta(c, "设备 ID", connection.DeviceID)
			})
		})
	})
}

func devicePathAddress(ssh transport.SSH, endpoint *transport.SSHEndpoint) string {
	address, port := ssh.Target, ssh.Port
	if endpoint != nil {
		if !strings.Contains(address, "@") {
			address = endpoint.User + "@" + address
		}
		port = endpoint.Port
	}
	if port != 0 && port != 22 {
		address += " · " + strconv.Itoa(port)
	}
	return address
}

func (v *View) devicePathDetails(c *n.Context, route app.DeviceRoute, status app.DevicePathStatus) {
	user, port := "使用 SSH 配置", "使用 SSH 配置"
	if before, _, ok := strings.Cut(route.SSH.Target, "@"); ok {
		user = before
	}
	if route.SSH.Port != 0 {
		port = strconv.Itoa(route.SSH.Port)
	}
	if e := status.Endpoint; e != nil {
		user, port = e.User, strconv.Itoa(e.Port)
	}
	deviceMeta(c, "登录身份", user)
	deviceMeta(c, "端口", port)
	if e := status.Endpoint; e != nil {
		_, host, ok := strings.Cut(route.SSH.Target, "@")
		if !ok {
			host = route.SSH.Target
		}
		if host != e.Host {
			deviceMeta(c, "实际主机", e.Host)
		}
	}
	v.tailnetPathDetails(c, route.SSH.TailnetPeer)
}

func (v *View) removeDevicePath(connection app.DeviceConnection, id string) {
	connection = connection.Normalized()
	if len(connection.Routes) < 2 {
		return
	}
	routes := make([]app.DeviceRoute, 0, len(connection.Routes)-1)
	for _, r := range connection.Routes {
		if r.ID != id {
			routes = append(routes, r)
		}
	}
	connection.Routes = routes
	connection = connection.Normalized()
	connection.SSH = connection.ActiveSSH()
	connection.OtherSSH = nil
	for _, r := range routes {
		if r.ID != connection.ActiveRouteID {
			connection.OtherSSH = append(connection.OtherSSH, r.SSH)
		}
	}
	v.saveDeviceConnection(connection, true)
}

// Build a single unconfigured route after authenticated worker discovery.
func newDeviceConnection(name string, info transport.WorkerInfo, ssh transport.SSH) app.DeviceConnection {
	return app.DeviceConnection{ID: domain.NewID(), Name: name, Home: info.Home, DeviceID: info.DeviceID, SSH: ssh}
}
