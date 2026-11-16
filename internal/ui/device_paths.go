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
		if !p.removal && p.request != v.sshRequest {
			return
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
	v.continueWorkspaceAfterDeviceSave(p.connection)
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

func (v *View) deviceAddPath(c *n.Context, t *n.Theme, connection app.DeviceConnection) {
	add := iconButton(c, plus, "添加连接路径").MarginEnd(-configurationHoverInset).Disabled(v.Controller == nil || v.deviceConnectionSave != nil || connection.ID == "")
	v.choiceMenu(c, t, "add-device-path:"+connection.ID, add, []menuChoice{{id: "ssh", name: "通过 SSH", icon: commandTerminalIcon, choose: func() {
		v.dismissDeviceSetup()
		v.sshCheck = sshEditing
		v.sshExpectedDevice = connection.DeviceID
	}}, {id: "tailnet", name: "通过 Tailnet", icon: tailscaleMark, choose: func() {
		v.dismissDeviceSetup()
		v.beginTailnet()
		v.sshExpectedDevice = connection.DeviceID
	}}})
}

func (v *View) devicePathMenu(menu *n.Menu, connection app.DeviceConnection, route app.DeviceRoute, state *app.DeviceState) {
	backup := false
	for _, other := range state.Paths {
		if other.ID != route.ID && (other.Phase == app.PathReady || other.Phase == app.PathActive) {
			backup = true
		}
	}
	if menu.Item("移除此连接").Disabled(len(connection.Routes) < 2 || v.deviceConnectionSave != nil || (route.ID == connection.ActiveRouteID && !backup)).Chosen() {
		v.removeDevicePath(connection, route.ID)
	}
}

func (v *View) deviceScenePathDetails(c *n.Context, t *n.Theme, connection app.DeviceConnection, route app.DeviceRoute, status app.DevicePathStatus, state *app.DeviceState, dismiss func()) {
	name := "直连 SSH"
	if route.SSH.TailnetPeer != "" {
		name = "Tailnet SSH"
	}
	n.Column(c).FillWidth().ContextMenu(func(menu *n.Menu) { v.devicePathMenu(menu, connection, route, state) }).Children(func() {
		n.Row(c).FillWidth().Height(28).Gap(8).AlignItems(n.Baseline).Children(func() {
			n.Text(c, name).FontSize(13).FixedLineHeight(20).Grow(1)
			if iconButton(c, closeIcon, "收起连接路径:"+route.ID).MarginEnd(-configurationHoverInset).Clicked() {
				dismiss()
			}
		})
		address := devicePathAddress(route.SSH, status.Endpoint)
		n.Column(c).FillWidth().Label("路径地址:" + route.ID).Role(n.RoleNone).Children(func() {
			n.Text(c, address).FillWidth().SingleLine().TooltipWhenTruncated(address).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Selectable()
		})
		v.devicePathDetails(c, route, status)
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
