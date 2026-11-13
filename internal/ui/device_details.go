package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strconv"
	"strings"
	"time"
)

func deviceDivider(c *n.Context) { n.Column(c).FillWidth().Height(1).Background(c.Theme().Border) }
func deviceHeading(c *n.Context, label string, action func()) {
	n.Row(c).FillWidth().Height(configurationSectionHeader).AlignItems(n.Start).Children(func() {
		configurationSectionTitle(c, label).Label(label).Grow(1)
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
	trigger := configurationRowControl(c.Key("toggle"), 6).Justify(n.Start).Height(configurationRowHeight).Label(label).Children(func() {
		n.Row(c).FillWidth().Height(20).Gap(8).AlignItems(n.Baseline).Children(func() {
			n.Text(c, label).FontSize(configurationLabelSize).FixedLineHeight(configurationTextLine).TextColor(c.Theme().TextMuted).Grow(1)
			angle := float32(-90)
			if *open {
				angle = 0
			}
			inlineSymbol(c, chevron, label, n.Font{Family: c.Theme().Font, Size: configurationLabelSize, Weight: 400}, 12, c.Theme().TextMuted, angle)
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
	if key == "tailnet" {
		v.tailnetPage(c, t)
		return
	}
	m := v.Model
	name, address, deviceID := m.LocalName(), "", m.LocalDeviceID
	for _, connection := range m.Connections {
		if connection.ID == key {
			ssh := connection.ActiveSSH()
			name, address, deviceID = connection.Name, ssh.Target, connection.DeviceID
			if at := strings.LastIndex(address, "@"); at >= 0 {
				address = address[at+1:]
			}
		}
	}
	state := v.deviceState(key)
	scope := n.Column(c).FillWidth().FillHeight().Grow(1)
	agentState := n.Local(scope, "agent-editor", func() *deviceAgentEditor { return agentEditor(state.Readiness.Harnesses) })
	v.syncDeviceAgents(key, state, agentState)
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
		page, depth := "device", 0
		var selected *domain.HarnessInfo
		for i := range (*agentState).catalog {
			if (*agentState).catalog[i].ID == (*agentState).pageID {
				selected = &(*agentState).catalog[i]
				page, depth = "agent:"+selected.ID, 1
				break
			}
		}
		configurationTabPanel(c.Key("device-navigation"), page, depth, configurationPanelFill, func() {
			if selected != nil {
				v.deviceAgentPage(c, t, key, name, state, *agentState, *selected)
				return
			}
			width, _ := c.Size()
			header := n.Column(c).FillWidth().Height(80).Padding(28, configurationInset, 6)
			if width < 900 {
				header.Height(88).Padding(8, configurationInset, 6)
			}
			header.Children(func() {
				if width < 900 {
					if configurationAction(c, "返回设备列表").MarginStart(64).Clicked() {
						v.deviceList = true
					}
				}
				n.Row(c).FillWidth().Height(28).Gap(12).AlignItems(n.Baseline).Children(func() {
					v.deviceIconPicker(c, t, "detail:"+key, "设备图标", name, n.Font{Family: t.Font, Size: configurationTitleSize, Weight: 400}, m.DeviceIconChoice(key), v.deviceDefaultIcon(key), func(icon app.DeviceIcon) {
						if err := m.SetDeviceIcon(key, icon); err != nil {
							rename.error = err.Error()
						}
					}).MarginStart(-configurationHoverInset)
					n.Row(c).Grow(1).Gap(4).AlignItems(n.Baseline).Children(func() {
						if rename.open {
							input := textField(c.Key("name"), &rename.name).Padding(0, 8).Grow(1).MaxWidth(360).Height(28).FixedLineHeight(configurationTitleLine).FontSize(configurationTitleSize).FontWeight(400).Label("设备显示名称").Transition(fadeMotion(c, 140*time.Millisecond))
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
							n.Text(c, name).SingleLine().Shrink(1).FontSize(configurationTitleSize).FontWeight(400).FixedLineHeight(configurationTitleLine).Tooltip(name).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
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
				errorText := rename.error
				if errorText == "" && strings.HasPrefix(m.Error, "无法保存草稿：") {
					errorText = m.Error
				}
				retained := n.Local(errorFrame, "text", func() string { return errorText })
				if errorText != "" {
					*retained = errorText
				}
				animatedContentHeight(c, errorFrame, errorText != "", 180*time.Millisecond, func() float32 {
					n.Text(c, *retained).FillWidth().Padding(4, 0, 0, 38).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Transition(fadeMotion(c, 140*time.Millisecond))
					return 0
				})
			})
			tabs := []configurationTabItem{{ID: "", Label: "Agent"}, {ID: "settings", Label: "设备设置"}}
			n.Column(c).FillWidth().PaddingX(configurationInset).Children(func() { v.deviceTab = configurationTabs(c, v.deviceTab, tabs, configurationUnderline, false) })
			configurationTabPanel(c.Key("device-tab-panel"), v.deviceTab, configurationTabIndex(tabs, v.deviceTab), configurationPanelFill, func() {
				n.Scroll(c.Key("device-body:" + v.deviceTab)).FillWidth().Grow(1).Children(func() {
					n.Column(c).FillWidth().Padding(configurationHeadingGap, configurationInset, configurationSectionGap).AlignItems(n.Start).Children(func() {
						n.Column(c).FillWidth().Gap(configurationSectionGap).Children(func() {
							if v.deviceTab == "" {
								v.deviceAgents(c.Key("agents:"+key), t, key, state, agentState)
								return
							}

							if key != "local" {
								v.devicePathsSection(c.Key("paths:"+key), t, key, state)
							}
							n.Column(c).FillWidth().Gap(0).Children(func() {
								deviceHeading(c, "执行服务", nil)
								v.deviceWorker(c.Key("worker:"+key), t, key, state)
							})
							v.deviceStorage(c.Key("storage:"+key), t, key, state)
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
									entry := configurationRowControl(c.Key(w.ID), 6).Height(40).Label("打开工作区：" + w.Name).ContextMenu(func(menu *n.Menu) { v.workspaceMenu(menu, w) }).Children(func() {
										n.Row(c).FillWidth().Gap(10).AlignItems(n.Baseline).Children(func() {

											n.Text(c, w.Name).SingleLine().FontSize(configurationLabelSize).FixedLineHeight(configurationTextLine).Grow(1)
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

							if m.Error != "" && m.Error != state.Error {
								n.Text(c, m.Error).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
							}
							if state.Error != "" && v.migrationForConnection(key) == nil {
								n.Text(c, state.Error).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
							}
						})
					})
				})
			})
		}, false)
	})
}

func (v *View) deviceWorker(c *n.Context, t *n.Theme, key string, state *app.DeviceState) {
	service := state.WorkerService
	flow := v.migrationForConnection(key)
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
			action = "正在更新"
			disabled = true
		case app.WorkerLegacy:
			disabled = true
		}
	}
	if !state.Connected && (service == nil || service.Phase != app.WorkerUpdating && service.Phase != app.WorkerWaiting) {
		action, method, disabled = "检查服务", "$worker.migration.inspect", v.Controller == nil
	}
	if flow != nil {
		label = migrationCaption(flow)
		action, disabled = migrationAction(flow)
		disabled = disabled || v.Controller == nil
	} else if service != nil && service.Legacy != nil {
		label = "需要更新设备服务"
		action, disabled = "准备更新", v.Controller == nil
		method = "$worker.migration.inspect"
	}
	if service != nil && service.Phase == app.WorkerUpdating && flow == nil {
		label = ""
	}
	n.Row(c).FillWidth().Height(40).Gap(8).AlignItems(n.Center).Children(func() {
		n.Row(c).Gap(8).AlignItems(n.Baseline).Children(func() {
			n.Text(c, "Shioji worker").FontSize(configurationLabelSize).FixedLineHeight(20).TextColor(t.Text).SingleLine()
			n.Text(c, label).FontSize(12).FixedLineHeight(20).SingleLine().TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
		})
		n.Spacer(c).Grow(1)
		if configurationAction(c, action).MarginEnd(-8).Label(func() string {
			if flow != nil {
				return action
			}
			if method == "$worker.migration.inspect" {
				if service != nil && service.Legacy != nil {
					return "准备更新执行服务"
				}
				return "检查设备服务"
			}
			if method == "$worker.update" {
				return "更新执行服务"
			}
			return "检查执行服务更新"
		}()).Disabled(disabled).Clicked() {
			if flow != nil {
				v.performMigrationAction(flow)
			} else if method == "$worker.migration.inspect" {
				v.beginNormalMigration(key)
			} else {
				v.do(app.Job{ConnectionID: key, Method: method})
			}
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
	if flow != nil {
		message = migrationMessage(flow)
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
