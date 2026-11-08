package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"strconv"
	"strings"
	"time"
)

// Device actions share the same compact, neutral hover treatment.
func deviceAction(c *n.Context, label string) n.Element {
	return ghost(c, label, 6).Height(28).PaddingX(8).FontSize(12)
}
func deviceDivider(c *n.Context) { n.Column(c).FillWidth().Height(1).Background(c.Theme().Border) }
func deviceHeading(c *n.Context, label string, action func()) {
	n.Row(c).FillWidth().Height(36).AlignItems(n.Start).Children(func() {
		n.Text(c, label).Label(label).FontSize(14).FontWeight(500).FixedLineHeight(28).Grow(1)
		if action != nil {
			action()
		}
	})
}
func deviceMeta(c *n.Context, label, value string) {
	deviceConfigRow(c, label, func() {
		n.Text(c, value).FillWidth().FontSize(12).FixedLineHeight(20).TextAlign(n.End).Selectable().ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
	})
}

func deviceDisclosure(c *n.Context, label string, open *bool, body func()) {
	trigger := control(c.Key("toggle"), 6).FillWidth().Justify(n.Start).PaddingX(0).Height(40).Label(label).Children(func() {
		n.Row(c).FillWidth().Height(20).Gap(8).AlignItems(n.Baseline).Children(func() {
			n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(c.Theme().TextMuted).Grow(1)
			angle := float32(-90)
			if *open {
				angle = 0
			}
			inlineSymbol(c, chevron, label, n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}, 12, c.Theme().TextMuted, angle)
		})
	})
	if trigger.Clicked() {
		*open = !*open
	}
	frame := n.Column(c.Key("body")).FillWidth().Clip()
	animatedContentHeight(c, frame, *open, 200*time.Millisecond, func() float32 {
		n.Column(c).FillWidth().Transition(disclosureMotion(c, 160*time.Millisecond)).Children(body)
		return 0
	})
}

type deviceNameEditor struct {
	open, focus, pending bool
	name, error          string
}

func (v *View) deviceDetail(c *n.Context, t *n.Theme, key string) {
	m := v.Model
	name, address, deviceID := m.LocalName(), "", m.LocalDeviceID
	user, port := "使用 SSH 配置", "使用 SSH 配置"
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
	scope := n.Column(c).FillWidth().FillHeight().Grow(1)
	rename := n.Local(scope, "name-editor", func() deviceNameEditor { return deviceNameEditor{} })
	if rename.pending && v.Saver != nil {
		saved, err := v.Saver.Result()
		if err != nil {
			rename.error = "无法保存设备名称：" + err.Error()
			rename.open = true
			rename.pending = false
		} else {
			var disk struct {
				LocalDeviceName string                 `json:"localDeviceName"`
				Connections     []app.DeviceConnection `json:"connections"`
			}
			if json.Unmarshal(saved, &disk) == nil {
				persisted := disk.LocalDeviceName
				for _, connection := range disk.Connections {
					if connection.ID == key {
						persisted = connection.Name
					}
				}
				if persisted == rename.name {
					rename.pending = false
					rename.error = ""
					if strings.HasPrefix(m.Error, "无法保存草稿：") {
						m.Error = ""
					}
				}
			}
		}
	}

	scope.Children(func() {
		width, _ := c.Size()
		header := n.Column(c).FillWidth().Height(80).Padding(28, 32, 6)
		if width < 900 {
			header.Height(108).Padding(8, 24, 6)
		}
		header.Children(func() {
			if width < 900 {
				if deviceAction(c, "返回设备列表").Clicked() {
					v.deviceList = true
				}
			}
			n.Row(c).FillWidth().Height(28).Gap(12).AlignItems(n.Baseline).Children(func() {

				n.Row(c).Grow(1).Gap(4).AlignItems(n.Baseline).Children(func() {
					if rename.open {
						input := textField(c.Key("name"), &rename.name).Padding(0, 8).Grow(1).MaxWidth(360).Height(28).FixedLineHeight(28).FontSize(17).FontWeight(500).Label("设备显示名称").Transition(fadeMotion(c, 140*time.Millisecond))
						if rename.focus {
							input.Focus()
							rename.focus = false
						}
						save := iconButton(c, check, "保存设备名称").AlignSelf(n.Center).Clicked() || input.Submitted() && !input.Composing()
						cancel := iconButton(c, closeIcon, "取消修改设备名称").AlignSelf(n.Center).Clicked() || input.Shortcut(0, n.KeyEscape)
						if cancel {
							rename.open = false
							rename.error = ""
						}
						if save {
							if err := m.RenameDevice(key, rename.name); err != nil {
								rename.error = err.Error()
							} else {
								rename.open = false
								rename.error = ""
								rename.name = m.DeviceName(deviceID)
								rename.pending = v.Saver != nil
								if v.Saver != nil {
									if data, err := m.EncodeState(); err == nil {
										v.Saver.Queue(data)
									}
								}
								if strings.HasPrefix(m.Error, "无法保存草稿：") {
									m.Error = ""
								}
							}
						}
					} else {
						n.Text(c, name).SingleLine().Shrink(1).FontSize(17).FontWeight(500).FixedLineHeight(28).Tooltip(name).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
						if iconButton(c, queueEditIcon, "修改设备名称").AlignSelf(n.Center).Clicked() {
							rename.open = true
							rename.name = name
							rename.error = ""
							rename.focus = true
						}
					}
				})
			})
			if key == "local" {
				n.Text(c, "这台 Mac · Apple Silicon").FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
			}
			if key != "local" {
				n.Text(c, address).FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
			}
			errorFrame := n.Column(c.Key("rename-error")).FillWidth().Clip()
			retained := n.Local(errorFrame, "text", func() string { return rename.error })
			if rename.error != "" {
				*retained = rename.error
			}
			animatedContentHeight(c, errorFrame, rename.error != "", 180*time.Millisecond, func() float32 {
				n.Text(c, *retained).FillWidth().Padding(4, 0, 0, 38).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Transition(fadeMotion(c, 140*time.Millisecond))
				return 0
			})
		})
		n.Column(c).FillWidth().PaddingX(32).Children(func() {
			n.Row(c).Height(40).Gap(24).Children(func() {
				for _, tab := range []struct{ id, label string }{{"", "Agent"}, {"settings", "设备设置"}} {
					n.Column(c).Height(40).Children(func() {
						button := ghost(c.Key(tab.id), tab.label).Height(38).FontSize(13).Label(tab.label)
						if v.deviceTab != tab.id {
							button.TextColor(t.TextMuted)
						}
						if button.Clicked() {
							v.deviceTab = tab.id
						}
						if v.deviceTab == tab.id {
							n.Column(c).FillWidth().Height(2).Background(t.Text).Radius(1)
						}
					})
				}
			})
			deviceDivider(c)
		})
		n.Scroll(c.Key("device-body")).FillWidth().Grow(1).Children(func() {
			n.Column(c).FillWidth().Padding(20, 32, 24).AlignItems(n.Start).Children(func() {
				n.Column(c).FillWidth().MaxWidth(840).Gap(24).Children(func() {
					if v.deviceTab == "" {
						v.deviceAgents(c.Key("agents:"+key), t, key, state)
						return
					}

					n.Column(c).FillWidth().Gap(0).Children(func() {
						deviceHeading(c, "执行服务", nil)
						v.deviceWorker(c.Key("worker:"+key), t, key, state)
					})
					n.Column(c).FillWidth().Gap(0).Children(func() {
						deviceHeading(c, "工作区", func() {
							if iconButton(c, plus, "新建工作区").Disabled(!state.Connected || v.Controller == nil).Clicked() {
								v.beginWorkspace(key)
							}
						})
						count := 0
						for _, w := range m.Snapshot.Workspaces {
							if w.DeviceID != deviceID && !(key == "local" && w.DeviceID == "") {
								continue
							}
							count++
							entry := control(c.Key(w.ID), 6).FillWidth().Height(40).PaddingX(0).Radius(6).Label("打开工作区：" + w.Name).ContextMenu(func(menu *n.Menu) { v.workspaceMenu(menu, w) }).Children(func() {
								n.Row(c).FillWidth().Gap(10).AlignItems(n.Baseline).Children(func() {

									n.Text(c, w.Name).SingleLine().FontSize(12).FixedLineHeight(20).Grow(1)
									label := strconv.Itoa(len(w.Folders)) + " 个目录"
									n.Text(c, label).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
									inlineSymbol(c, disclosureRight, label, n.Font{Family: t.Font, Size: 11, Weight: 400}, 12, t.TextMuted)
								})
							})

							if entry.Clicked() {
								m.WorkspaceID = w.ID
								m.ActiveConnection = key
								v.home()
							}
						}
						if count == 0 {
							n.Row(c).FillWidth().Height(40).AlignItems(n.Center).Children(func() { n.Text(c, "暂无工作区").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted) })
						}
					})
					if key != "local" {
						n.Column(c).FillWidth().Gap(0).Children(func() {
							deviceHeading(c, "连接", func() {
								if deviceAction(c, "重新连接").Disabled(v.Controller == nil).Clicked() {
									v.do(app.Job{ConnectionID: key, Method: "$reconnect"})
								}
							})
							deviceConfigRow(c, "SSH", func() {
								n.Row(c).FillWidth().Gap(12).AlignItems(n.Baseline).Children(func() {
									n.Text(c, address).SingleLine().FontSize(12).FixedLineHeight(20).Grow(1).Tooltip(address)
									n.Text(c, deviceLatency(state, c.Now())).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
								})
							})

							owner := n.Column(c).FillWidth()
							open := n.Local(owner, "connection-details", func() bool { return false })
							owner.Children(func() {
								deviceDisclosure(c.Key("connection"), "连接详情", open, func() {
									deviceMeta(c, "登录身份", user)
									deviceMeta(c, "端口", port)
									deviceMeta(c, "设备 ID", deviceID)
								})
							})
						})
					}
					if m.Error != "" && m.Error != state.Error {
						n.Text(c, m.Error).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
					}
					if state.Error != "" {
						n.Text(c, state.Error).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
					}
				})
			})
		})
	})
}

func (v *View) deviceWorker(c *n.Context, t *n.Theme, key string, state *app.DeviceState) {
	service := state.WorkerService
	label := "服务未运行"
	if key != "local" {
		label = "未连接"
	}
	if state.Connected {
		label = "已就绪"
	}
	if service != nil && (state.Connected || service.Phase == app.WorkerUpdating || service.Phase == app.WorkerUpdateFailed) {
		label = service.Label()
	}
	action := "检查更新"
	method := "$worker.check"
	disabled := v.Controller == nil || !state.Connected
	if service != nil {
		switch service.Phase {
		case app.WorkerAvailable, app.WorkerUpdateFailed:
			action = "更新"
			method = "$worker.update"
		case app.WorkerWaiting:
			action = "等待空闲"
			disabled = true
		case app.WorkerUpdating:
			action = "更新中…"
			disabled = true
		case app.WorkerLegacy:
			disabled = true
		}
	}
	n.Row(c).FillWidth().Height(40).Gap(8).AlignItems(n.Center).Children(func() {
		n.Row(c).Gap(8).AlignItems(n.Baseline).Children(func() {
			n.Text(c, "Shioji worker").FontSize(12).FixedLineHeight(20).TextColor(t.Text).SingleLine()
			n.Text(c, label).FontSize(12).FixedLineHeight(20).SingleLine().TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		})
		n.Spacer(c).Grow(1)
		if deviceAction(c, action).Label(func() string {
			if method == "$worker.update" {
				return "更新执行服务"
			}
			return "检查执行服务更新"
		}()).Disabled(disabled).Clicked() {
			v.do(app.Job{ConnectionID: key, Method: method})
		}
	})

	v.backgroundSwitch(c.Key("background"), t, key, state)
	owner := n.Column(c).FillWidth()
	open := n.Local(owner, "service-details", func() bool { return false })
	owner.Children(func() {
		deviceDisclosure(c.Key("service"), "服务详情", open, func() {
			if service != nil {
				deviceMeta(c, "构建", app.WorkerBuildLabel(service.Build))
				if service.Available != nil && service.Phase != app.WorkerCurrent {
					deviceMeta(c, "可用构建", app.WorkerBuildLabel(service.Available))
				}
				if state.Connected && service.Build != nil {
					managed := "独立运行"
					if service.Build.Managed {
						managed = "由系统托管"
					}
					deviceMeta(c, "当前进程", managed)
				}
				if service.Background != nil && (service.Background.State == "unavailable" || service.Background.State == "unsupported") {
					deviceMeta(c, "后台服务", "不可用")
					if service.Background.Message != "" {
						n.Text(c, service.Background.Message).FillWidth().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
					}
				}
			}
		})
	})
	message := ""
	if service != nil {
		message = service.Message
	}
	frame := n.Column(c.Key("worker-message")).FillWidth().Clip().Label("执行服务版本详情")
	retained := n.Local(frame, "message", func() string { return message })
	if message != "" {
		*retained = message
	}
	animatedContentHeight(c, frame, message != "", 180*time.Millisecond, func() float32 {
		n.Text(c, *retained).FillWidth().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		return 0
	})
}
