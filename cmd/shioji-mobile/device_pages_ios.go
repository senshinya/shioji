package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

func (a *mobileApp) openDevice(parent *mygo.Window) {
	deviceID := a.endpoint.DeviceID
	var w *mygo.Window
	w = a.page(a.deviceName(), func(c *ui.Context) {
		mobilePageBody(c, func() {
			p := palette(c)
			mobileSection(c, "连接")
			phase := a.connectionPhase()
			status, statusInk := mobileConnectionLabel(phase), p.secondary
			diagnostic := mobileclient.RedactOperationDiagnostics(a.model.ConnectionError)
			if phase == app.ConnectionIdle {
				status, statusInk = "已连接", p.active
				diagnostic = ""
			}
			mobileDetailRow(c, "连接状态", diagnostic, "连接状态", infoSymbol, nil, func() {
				oneLine(c, status, 13, statusInk)
			})
			endpoint := a.activeEndpoint()
			route := a.deviceRouteDisplay(endpoint, a.activeDeviceTiming(endpoint.RouteID()), a.model.Connected, c.Now())
			if endpoint.TailnetPeerID != "" {
				devicePathRow(c, "当前线路", route.summary())
				devicePathRow(c, "连接方式", route.tailnet.Mode)
				if route.tailnet.Region != "" {
					devicePathRow(c, "DERP 区域", route.tailnet.Region)
				}
			} else {
				devicePathRow(c, "当前线路", route.name)
				latency := route.latency
				if latency == "" {
					latency = "尚未测得"
				}
				mobileRow(c, "往返延迟", "", latency, clockSymbol, nil)
			}
			host := deviceHost(endpoint)
			mobileDetailRow(c, "主机", "", "主机", deviceSymbol, nil, func() {
				oneLine(c, host, 11.5, p.secondary).Font("monospace").MaxWidthPercent(64)
			})
			mobileRow(c, "管理连接", "", "", linkSymbol, func() { a.openDevicePaths(w) })
			mobileRow(c, "设备诊断", "", "", infoSymbol, func() { a.openDeviceHealth(w) })
			mobileGroupHeading(c, "运行环境")
			workerStatus := status
			if service := a.model.WorkerService; service != nil && service.Phase != app.WorkerCurrent {
				workerStatus = service.Label()
			}
			mobileRow(c, "Shioji Worker", "", workerStatus, terminalSymbol, func() { a.openWorker(w) })
			agentStatus := readinessName(a.model.Readiness.State)
			if a.agentSetupNeedsPreparation() {
				agentStatus = "执行服务需要更新"
			}
			mobileRow(c, "远程 Agent", "", agentStatus, terminalSymbol, func() {
				if a.agentSetupNeedsPreparation() {
					a.openWorker(w)
					return
				}
				a.openRemoteAgents(w)
			})
			mobileGroupHeading(c, "工作区")
			for _, workspace := range a.model.Snapshot.Workspaces {
				if workspace.Archived {
					continue
				}
				mobileRow(c.Key(workspace.ID), workspace.Name, "", "", folderSymbol, func() { a.openWorkspace(w, workspace) })
			}
			mobileRow(c, "新建工作区", "", "", folderSymbol, func() { a.openWorkspace(w, domain.Workspace{}) })
			mobileGroupHeading(c, "自动化")
			mobileRow(c, "定时任务", "", "", terminalSymbol, func() { a.openSchedules(w, a.endpoint.DeviceID) })
		})
	})
	if deviceID != "" && a.splitRoot().savedDevice(deviceID).Endpoint.DeviceID == deviceID {
		mygo.SetBarItems(w, mygo.BarItem{Title: "编辑设备", Symbol: "pencil", OnClick: func() {
			if a.pageCurrent(w) && a.endpoint.DeviceID == deviceID {
				a.openDeviceAppearance(w, deviceID)
			}
		}})
	}
	a.pushNative(parent, w)
}

func (a *mobileApp) openDevicePaths(parent *mygo.Window) {
	deviceID := a.endpoint.DeviceID
	var menu mobileContextMenu
	var confirming, removed bool
	var problem string
	var w *mygo.Window
	remove := func(endpoint mobileclient.Endpoint) {
		if confirming || !a.pageCurrent(w) || a.endpoint.DeviceID != deviceID {
			return
		}
		confirming, problem = true, ""
		controller := a.controller
		ctx, cancel := context.WithCancel(context.Background())
		unregister := w.OnClosed(cancel)
		args := map[string]any{"window": w.NativeHandle(), "title": "移除此连接？", "detail": endpoint.User + " · " + endpoint.Address}
		go func() {
			defer cancel()
			var accepted bool
			err := uikit.Call(ctx, "confirmDevicePathRemoval", args, &accepted)
			mygo.RunOnMain(func() {
				unregister()
				confirming = false
				if !a.pageCurrent(w) || a.endpoint.DeviceID != deviceID || a.controller != controller {
					return
				}
				if err == nil && accepted {
					err = a.removeDevicePath(deviceID, endpoint.RouteID())
					if err == nil {
						removed = true
						parent.SetTitle(a.deviceName())
					}
				}
				if err != nil {
					problem = err.Error()
				}
				w.Invalidate()
			})
		}()
	}
	w = a.page("连接路径", func(c *ui.Context) {
		if removed {
			c.AddToast(ui.Toast{ID: "device-path-removed", Title: "连接已移除", Timeout: 2500 * time.Millisecond})
			removed = false
		}
		frame := contextMenuFrame{zoom: c.Zoom()}
		mobilePageWithFooter(c, func() {
			mobilePageContext(c, a.deviceName())
			if problem != "" {
				label(c, problem, captionTextSize, palette(c).warning).Role(ui.RoleStatus).Padding(6, 0)
			}
			for _, endpoint := range a.splitRoot().savedDevice(deviceID).Endpoints() {
				route := endpoint.RouteID()
				method := "直接 SSH"
				if endpoint.TailnetPeerID != "" {
					method = "Tailnet SSH"
				}
				state := "正在检查"
				for _, measured := range a.model.DevicePaths {
					if measured.ID == route {
						state = map[app.DevicePathPhase]string{app.PathActive: "当前", app.PathReady: "可用",
							app.PathConnecting: "正在检查", app.PathSwitching: "正在切换", app.PathUnavailable: "不可用"}[measured.Phase]
						break
					}
				}
				accessibilityName := method + "，" + endpoint.Address + "，" + state
				row := mobileDetailRow(c.Key(route), method, endpoint.User+" · "+endpoint.Address, accessibilityName, linkSymbol, func() {
					page := a.page(method, func(c *ui.Context) {
						mobilePageBody(c, func() {
							mobilePageContext(c, endpoint.Address, endpoint.User)
							if endpoint.TailnetPeerID != "" {
								observed := a.tailnetObservation(endpoint, true, c.Now())
								devicePathRow(c, "路径", observed.Path)
								mobileRow(c, "往返延迟", "", observed.Latency, clockSymbol, nil)
							}
							mobileSection(c, "主机公钥指纹")
							label(c, endpoint.HostFingerprint, captionTextSize, palette(c).secondary).Font("monospace").Selectable()
							mobileGroupHeading(c, "执行服务路径")
							label(c, endpoint.WorkerPath, captionTextSize, palette(c).secondary).Font("monospace").Selectable()
						})
					})
					a.pushNative(w, page)
				}, func() { label(c, state, captionTextSize, palette(c).secondary) })
				frame.add(row, route, contextMenuTarget{commands: []contextMenuCommand{{
					ID: "remove-path", Title: "移除此连接", Symbol: "trash", Destructive: true,
					Disabled: confirming || a.controller == nil || !a.controller.CanRemoveAuthenticatedDevicePath(route),
					run:      func() { remove(endpoint) },
				}}})
			}
		}, func() {
			primaryAction(c, "添加连接路径", func() {
				a.openPair(w, mobileclient.Endpoint{DeviceID: a.endpoint.DeviceID, User: a.activeEndpoint().User})
			})
		}).DrawOver(func(_ *ui.Painter, viewport ui.Rect) { menu.publish(a, w, frame, viewport) })
	})
	menu.listen(a, w)
	a.pushNative(parent, w)
}

func (a *mobileApp) removeDevicePath(deviceID, routeID string) error {
	root := a.splitRoot()
	controller := a.controller
	if a.endpoint.DeviceID != deviceID || controller == nil || controller.ExpectedDeviceID != deviceID {
		return fmt.Errorf("设备的连接路径已变化")
	}
	for index, saved := range root.savedDevices.Devices {
		if saved.Endpoint.DeviceID != deviceID {
			continue
		}
		var updated savedMobileDevice
		err := controller.RemoveAuthenticatedDevicePath(routeID, func(replacement string) error {
			var err error
			updated, err = saved.WithoutRoute(routeID, replacement)
			if err != nil {
				return err
			}
			next := root.savedDevices
			next.Devices = slices.Clone(next.Devices)
			next.Devices[index] = updated
			if err := root.saveDeviceRegistry(next); err != nil {
				return fmt.Errorf("无法保存连接：%w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		a.endpoint = updated.Endpoint
		if pane := root.parallelDevice; pane != nil {
			pane.savedDevices = root.savedDevices
		}
		root.updateChatHeader()
		a.refresh()
		return nil
	}
	return fmt.Errorf("设备的连接路径已变化")
}

func devicePathRow(c *ui.Context, title, path string) {
	mobileDetailRow(c, title, "", title, branchSymbol, nil, func() {
		label(c, path, 13, palette(c).secondary).MaxWidthPercent(65).MaxLines(2).Ellipsis("…")
	})
}

func (a *mobileApp) agentSetupNeedsPreparation() bool {
	return a.model.Connected && a.model.WorkerMinor > 0 && a.model.WorkerMinor < app.AgentSetupWorkerMinor
}

func (a *mobileApp) openWorker(parent *mygo.Window) {
	if receipt, ok := a.legacyMigrations[a.endpoint.DeviceID]; ok {
		a.openLegacyMigration(parent, receipt.Endpoint, transport.WorkerInfo{})
		return
	}
	if preparation, ok := a.workerPreparations[a.endpoint.DeviceID]; ok {
		a.openServicePreparation(parent, preparation)
		return
	}
	var w *mygo.Window
	var stop context.CancelFunc
	req, background := mobileRequest{}, mobileRequest{}
	w = a.page("执行服务", func(c *ui.Context) {
		scope := a.connectionName()
		if a.endpoint.HostFingerprint != "" {
			scope = "SSH 身份已验证"
		}
		requiresPreparation := a.agentSetupNeedsPreparation()
		if service := a.model.WorkerService; !requiresPreparation && service != nil && (service.Available != nil || service.OperationID != "") {
			a.workerUpgradeView(c, w, scope, service, &req, &background)
			return
		}
		state, detail := "尚未连接执行服务", "检查已保存的 SSH 连接和执行服务路径。"
		if a.model.Connected {
			state = "执行服务已就绪"
			detail = fmt.Sprintf("协议 2.%d · 保留设备身份、工作区与会话。", a.model.WorkerMinor)
		}
		if requiresPreparation {
			state, detail = "执行服务需要更新", "更新完成后即可配置远程 Agent。"
		}
		workerSetupView(c, a.deviceName(), scope, state, detail, &req, func() {
			a.workerBackgroundControls(c, w, &background, req.busy)
		}, func() {
			if a.model.Connected && !requiresPreparation {
				primaryAction(c, "配置远程 Agent", func() { a.openRemoteAgents(w) })
			} else {
				primaryAction(c, "检查连接", func() {
					stop = a.runWorkerSetup(w, a.activeEndpoint(), nil, "", &req, func(verified mobileclient.Endpoint, _ *transport.WorkerInfo) {
						if err := a.switchSavedDevice(verified); err != nil {
							req.err = err.Error()
						}
					})
				}).Disabled(a.preview || a.endpoint.Address == "" || req.busy)
			}
		})
	})
	w.OnClosed(func() {
		if stop != nil {
			stop()
		}
	})
	a.pushNative(parent, w)
}

func deviceLoadRow(c *ui.Context, title, subtitle, value string, icon *ui.SVG) {
	mobileRowContent(c, ui.Row(c).MinHeight(max(formRowHeight, mobileTapSize(c))), title, subtitle, icon, false, func() {
		oneLine(c, value, 13, palette(c).secondary).Shrink(0)
	})
}

func (a *mobileApp) openDeviceHealth(parent *mygo.Window) {
	device := a.model.Snapshot.DeviceID
	var report *domain.DeviceHealth
	if a.preview {
		now := time.Now()
		report = &domain.DeviceHealth{DeviceID: device, ObservedAt: now, KeepAwake: domain.KeepAwakeState{Available: true}}
		previewCPU := []float64{12, 12, 29, 38, 24, 27, 15, 18}
		for i, cpu := range previewCPU {
			report.Samples = append(report.Samples, domain.DeviceLoad{
				At:  now.Add(-time.Duration(len(previewCPU)-1-i) * 4 * time.Second),
				CPU: new(cpu),
				Memory: &domain.DeviceMemory{
					UsedBytes: (766 + uint64(2*i)) * (1 << 30) / 100, TotalBytes: 16 << 30,
				},
			})
		}
	}
	req := mobileRequest{}
	var checkAfter time.Time
	storage := a.deviceStorage()
	var w *mygo.Window
	accept := decodePage(func(in domain.DeviceHealth) error {
		if in.DeviceID != device || in.DeviceID != a.model.Snapshot.DeviceID {
			return fmt.Errorf("诊断结果来自其他设备")
		}
		a.model.SetDeviceHealth(in, time.Now())
		return nil
	})
	w = a.page("设备诊断", func(c *ui.Context) {
		mobilePageBody(c, func() {
			m := a.model
			current := m.DeviceHealth
			if current != nil && current.DeviceID == device {
				report = current
			}
			now := c.Now()
			fresh := a.preview || current != nil && current == report && m.Connected &&
				!current.ObservedAt.After(now) && now.Sub(current.ObservedAt) < 8*time.Second
			if fresh && !a.preview {
				c.After(current.ObservedAt.Add(8 * time.Second).Sub(now))
			}
			scope := a.connectionName()
			if observed := a.tailnetObservation(a.activeEndpoint(), m.Connected, now); observed.Measured {
				scope += " · " + observed.Latency
			}
			mobilePageContext(c, a.deviceName(), scope)
			mobileSection(c, "当前负载")
			var samples []domain.DeviceLoad
			if report != nil {
				samples = report.Samples
				if !fresh || a.preview {
					now = report.ObservedAt
				}
			}
			sample := domain.DeviceLoad{}
			if fresh && len(samples) != 0 {
				last := samples[len(samples)-1]
				if age := now.Sub(last.At); age >= 0 && age < 8*time.Second {
					sample = last
				}
			}
			cpu, used, total := "未知", "未知", ""
			if sample.CPU != nil {
				cpu = fmt.Sprintf("%.0f%%", *sample.CPU)
			}
			if memory := sample.Memory; app.DeviceLoadPercent(sample, true) != nil {
				used = fmt.Sprintf("%.1f GB", float64(memory.UsedBytes)/(1<<30))
				total = fmt.Sprintf("%.0f GB 总量", float64(memory.TotalBytes)/(1<<30))
			}
			deviceLoadRow(c.Key("cpu-value"), "CPU", "", cpu, processorSymbol)
			mobileDeviceLoadChart(c.Key("cpu-history"), "CPU", samples, now, false, fresh)
			deviceLoadRow(c.Key("memory-value"), "内存", total, used, memorySymbol)
			mobileDeviceLoadChart(c.Key("memory-history"), "内存", samples, now, true, fresh)
			if report != nil {
				if !fresh {
					mobileMessage(c, "上次采样")
				}
				state := report.KeepAwake
				if state.Available {
					enabled := state.Enabled
					checking := !checkAfter.IsZero() && !report.ObservedAt.After(checkAfter)
					change := func() {
						if a.preview {
							report.KeepAwake.Enabled = enabled
							return
						}
						input := domain.KeepAwakeInput{Enabled: enabled, Revision: state.Revision}
						a.runPageJobResult(nil, w, &req, "device.keepAwake", input, nil, func(data json.RawMessage, err error) error {
							if err == nil {
								err = accept(data)
							}
							if err != nil {
								checkAfter = time.Now()
							}
							return err
						})
					}
					formRow(c.Key("keep-awake"), "保持唤醒", func() {
						mobileSwitch(c, "保持唤醒", &enabled, change).Disabled(!fresh || req.busy || checking)
					})
				}
				if state.Error != "" {
					mobileMessage(c, state.Error)
				}
			}
			a.deviceStorageView(c, w, storage)
			mobileGroupHeading(c, "服务")
			workerDetail, workerStatus := "", readinessName(a.model.Readiness.State)
			if a.preview {
				workerDetail, workerStatus = "当前 v0.6.1", "可更新"
			} else if service := a.model.WorkerService; service != nil {
				workerDetail, workerStatus = app.WorkerBuildLabel(service.Build), service.Label()
			} else if a.model.WorkerMinor != 0 {
				workerDetail = fmt.Sprintf("协议 2.%d", a.model.WorkerMinor)
			}
			mobileRow(c, "执行服务", workerDetail, workerStatus, serverSymbol, nil)
			workerAction := "查看执行服务"
			if a.preview || a.model.WorkerService != nil && a.model.WorkerService.Phase == app.WorkerAvailable {
				workerAction = "准备执行服务升级"
			}
			mobileRow(c, workerAction, "", "", downloadSymbol, func() {
				if a.preview && a.model.WorkerService == nil {
					a.seedWorkerUpgrade()
				}
				a.openWorker(w)
			})
			req.status(c)
		})
	})
	a.pushNative(parent, w)
}

func mobileDeviceLoadChart(c *ui.Context, title string, samples []domain.DeviceLoad, at time.Time, memory, fresh bool) {
	p := palette(c)
	lines := app.DeviceLoadSegments(samples, at, memory)
	color := p.secondary
	if !fresh {
		color = color.Alpha(.45)
	}
	ui.Box(c).FillWidth().Height(46).Margin(0, 0, 8).Role(ui.RoleImage).Label(title + " 负载折线图").
		Draw(func(painter *ui.Painter, r ui.Rect) {
			for _, fraction := range []float32{0, .5, 1} {
				y := r.Y + 2 + (r.H-4)*fraction
				painter.Line(r.X, y, r.X+r.W, y, .5, p.line)
			}
			for _, line := range lines {
				var path ui.Path
				for i, point := range line {
					x, y := r.X+2+point.X*(r.W-4), r.Y+2+point.Y*(r.H-4)
					if i == 0 {
						path.MoveTo(x, y)
					} else {
						path.LineTo(x, y)
					}
				}
				if len(line) > 1 {
					painter.StrokePath(&path, 1.25, color)
				}
			}
			if len(lines) != 0 {
				line := lines[len(lines)-1]
				end := line[len(line)-1]
				var dot ui.Path
				dot.Circle(r.X+2+end.X*(r.W-4), r.Y+2+end.Y*(r.H-4), 1.75)
				painter.FillPath(&dot, color)
			}
		})
}

func (a *mobileApp) openRemoteAgents(parent *mygo.Window) {
	var refreshed *domain.Readiness
	var requestedEpoch, catalogEpoch string
	req := mobileRequest{}
	var w *mygo.Window
	load := func() {
		epoch := a.model.ConnectionEpoch
		requestedEpoch = epoch
		a.pageJob(w, &req, "runtime.catalog", nil, decodePage(func(r domain.Readiness) error {
			if a.model.ConnectionEpoch == epoch {
				r = redactRuntimeReadiness(r)
				refreshed, catalogEpoch = &r, epoch
			}
			return nil
		}))
	}
	change := func(id string, enabled bool) {
		m := a.model
		epoch := m.ConnectionEpoch
		if !m.Connected || epoch == "" || catalogEpoch != epoch || m.WorkerMinor < protocol.RuntimeAgentSelectionMinor {
			return
		}
		a.pageJob(w, &req, "runtime.agent.selection", protocol.RuntimeAgentSelection{HarnessID: id, Enabled: &enabled},
			decodePage(func(result protocol.AgentSetupResult) error {
				if a.model != m || m.ConnectionEpoch != epoch {
					return nil
				}
				if !result.Ready {
					if result.Error != "" {
						return fmt.Errorf("%s", result.Error)
					}
					return fmt.Errorf("设备尚未确认 Agent 设置")
				}
				if !slices.ContainsFunc(result.Catalog, func(agent domain.HarnessInfo) bool {
					return agent.ID == id && agent.Configured == enabled
				}) {
					return fmt.Errorf("设备返回的 Agent 设置与请求不一致")
				}
				catalog := redactRuntimeReadiness(domain.Readiness{Harnesses: result.Catalog})
				refreshed = &catalog
				result.Readiness.Sessions = m.Readiness.Sessions
				m.Readiness = redactRuntimeReadiness(result.Readiness)
				a.refresh()
				return nil
			}))
	}
	w = a.page("远程 Agent", func(c *ui.Context) {
		m := a.model
		if w != nil && !a.preview && !req.busy && m.Connected && m.ConnectionEpoch != "" && requestedEpoch != m.ConnectionEpoch {
			load()
		}
		mobilePageBody(c, func() {
			ready := m.Readiness
			if refreshed != nil && catalogEpoch == m.ConnectionEpoch {
				ready = *refreshed
			}
			catalog := ready.Harnesses
			if len(catalog) == 0 && ready.HarnessID != "" {
				catalog = []domain.HarnessInfo{{ID: ready.HarnessID, Name: ready.Descriptor().Name, State: ready.State, Models: ready.Models, Account: ready.Account}}
			}
			for _, info := range catalog {
				agent := a.signedInAgent(info)
				status := readinessName(agent.State)
				enabled := info.Configured
				if refreshed != nil && !enabled {
					status = "未启用"
				}
				open := func() {
					a.openAgentSettingsAt(w, agent, "常规", "", func() {
						if !a.preview && m.Connected {
							load()
						}
					})
				}
				if m.WorkerMinor < protocol.RuntimeAgentSelectionMinor {
					mobileRow(c.Key(agent.ID), agent.Name, "", status, terminalSymbol, open)
					continue
				}
				row := c.Key(agent.ID)
				ui.Row(row).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Box(row).Absolute().Height(1).Left(30).Right(0).Bottom(0).Background(palette(c).line).PassThrough()
					detail := ui.ButtonBase(row.Key("details")).MinHeight(max(contentRowHeight, mobileTapSize(c))).Grow(1).MinWidth(0).
						Role(ui.RoleButton).Label(agent.Name).Value(status).OnClick(open)
					mobileRowContent(row.Key("details"), detail, agent.Name, status, terminalSymbol, true, nil)
					mobileSwitch(row.Key("enabled"), "启用 "+agent.Name, &enabled, func() { change(agent.ID, enabled) }).
						Disabled(req.busy || !m.Connected || catalogEpoch == "" || catalogEpoch != m.ConnectionEpoch || agent.Installation == nil)
				})
			}
			if len(catalog) == 0 && !req.busy {
				message := "正在连接执行服务…"
				if a.model.Connected {
					message = "该设备未报告可用的 Agent"
				} else if a.model.ConnectionError != "" {
					message = a.model.ConnectionError
				}
				mobileMessage(c, message)
			}
			if m.Connected && m.WorkerMinor > 0 && m.WorkerMinor < protocol.RuntimeAgentSelectionMinor {
				mobileMessage(c, "更新设备执行服务后可启用或停用 Agent")
			}
			action(c, "刷新 Agent", load).Disabled(req.busy || !a.model.Connected)
			req.status(c)
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) openAgentSettings(parent *mygo.Window, agent domain.HarnessInfo) {
	a.openAgentSettingsAt(parent, agent, "常规", "", nil)
}

func (a *mobileApp) openAgentSettingsAt(parent *mygo.Window, agent domain.HarnessInfo, tab, detailID string, onReturn func()) {
	req := mobileRequest{}
	account := mobileAgentAccountDraft{}
	draftKey := [2]string{a.model.Snapshot.DeviceID, agent.ID}
	tools := mobileToolServices{harness: agent.ID, draft: a.mcpDrafts[draftKey]}
	skills := mobileManagedSkills{harness: agent.ID}
	hooks := mobileHooks{harness: agent.ID}
	switch tab {
	case "Skills":
		skills.detail = detailID
	case "Hooks":
		hooks.detail = detailID
	}
	tabs := []mobileSegment{{title: "常规"}, {title: "模型"}, {title: "Skills"}, {title: "MCP"}, {title: "Hooks"}, {title: "高级"}}
	delete(a.mcpDrafts, draftKey)
	var w *mygo.Window
	settings := a.agentSettingsDrafts[draftKey]
	if settings == nil {
		settings = &mobileAgentSettingsDraft{}
		if install := agent.Installation; install != nil {
			settings.accept(protocol.RuntimeSettings{Executable: install.Executable, Override: install.Override})
		}
	}
	delete(a.agentSettingsDrafts, draftKey)
	usesRuntimeSettings := func() bool { return !a.preview && a.model.WorkerMinor >= protocol.RuntimeSettingsMinor }
	acceptSettings := func(next protocol.RuntimeSettings) {
		settings.accept(next)
		if agent.Installation != nil {
			installation := *agent.Installation
			installation.Override, installation.Executable = next.Override, next.Executable
			agent.Installation = &installation
		}
	}
	settingsEpoch, accountEpoch := "", ""
	loadSettings := func(discard bool) {
		epoch := a.model.ConnectionEpoch
		settingsEpoch = epoch
		a.pageJob(w, &req, "runtime.settings", protocol.RuntimeSettingsQuery{HarnessID: agent.ID}, decodePage(func(next protocol.RuntimeSettings) error {
			if a.model.ConnectionEpoch != epoch {
				return nil
			}
			if next.Revision == "" {
				return fmt.Errorf("设备未返回运行设置的版本")
			}
			keepPath := false
			path := settings.path
			if !discard && settings.changed() {
				previous := settings.base
				if previous.Revision == next.Revision {
					settings.stale = false
					return nil
				}
				if previous.Revision != "" || previous.Override != next.Override {
					settings.stale = true
					return fmt.Errorf("设备上的运行设置已更新，草稿已保留")
				}
				keepPath = true
			}
			acceptSettings(next)
			if keepPath {
				settings.path = path
			}
			return nil
		}))
	}
	applyAgent := func(r domain.Readiness) bool {
		for _, next := range r.Harnesses {
			if next.ID == agent.ID {
				agent = redactRuntimeEnvironment(next)
				delete(a.agentSignIns, agent.ID)
				return true
			}
		}
		return false
	}
	applyReadiness := decodePage(func(r domain.Readiness) error {
		if !applyAgent(r) {
			// Readiness contains only enabled Agents. A disabled Agent still
			// has a configuration in the full device catalog.
			a.pageJob(w, &req, "runtime.catalog", nil, decodePage(func(catalog domain.Readiness) error {
				if !applyAgent(catalog) {
					return fmt.Errorf("设备未返回该 Agent 的设置")
				}
				return nil
			}))
		}
		return nil
	})
	save := func() {
		if usesRuntimeSettings() {
			a.saveRuntimeSettings(w, &req, agent.ID, settings, acceptSettings)
			return
		}
		a.pageJob(w, &req, "runtime.configure", protocol.RuntimeConfiguration{HarnessID: agent.ID, Executable: strings.TrimSpace(settings.path)},
			func(data json.RawMessage) error {
				err := applyReadiness(data)
				if err == nil {
					settings.base.Override = strings.TrimSpace(settings.path)
				}
				return err
			})
	}
	refetch := func() {
		accountEpoch = a.model.ConnectionEpoch
		a.pageJob(w, &req, "runtime.check", protocol.RuntimeConfiguration{HarnessID: agent.ID}, applyReadiness)
	}
	title := agent.Name
	if title == "" {
		title = agent.ID
	}
	w = a.page(title, func(c *ui.Context) {
		current := a.signedInAgent(agent)
		attempt := a.agentSignIns[agent.ID]
		authBusy := attempt != nil && attempt.status.Phase.Active()
		configured := current.Installation != nil && current.Installation.Configurable
		modern := usesRuntimeSettings()
		epoch := a.model.ConnectionEpoch
		if w != nil && modern && configured && !req.busy && a.model.Connected && epoch != "" && settingsEpoch != epoch {
			loadSettings(false)
		}
		if w != nil && !a.preview && tab == "常规" && !req.busy && a.model.Connected && epoch != "" &&
			current.Account == nil && current.Installation != nil && accountEpoch != epoch {
			refetch()
		}
		unsaved := configured && settings.changed()
		blocked := req.busy || !a.model.Connected || a.model.WorkerMinor < 16
		settingsBlocked := blocked || authBusy || modern && (settings.base.Revision == "" || settings.stale)
		apiSetup := tab == "常规" && account.selected(agentMobileSignInMethods(current, a.model.WorkerMinor)) == domain.SignInAPI
		tools.visible = tab == "MCP"
		settingsContent := func() {
			switch tab {
			case "常规":
				mobileSection(c, "账户")
				a.agentAccountView(c, w, current, &account, blocked || unsaved, refetch)
				if unsaved {
					mobileMessage(c, "保存运行设置后可登录")
				}
				if current.Error != "" && !authBusy {
					mobileMessage(c, current.Error)
				}
			case "模型":
				a.agentModels(c, w, draftKey[0], current, req.busy || authBusy, refetch)
				if modern && settings.base.Support.TitleModel {
					settings.titleModel(c, current.Models, settingsBlocked || current.State != domain.RuntimeReady || len(current.Models) == 0)
				}
			case "高级":
				mobileSection(c, "启动")
				if install := current.Installation; install != nil {
					reportedPath := install.Executable
					if modern && settings.base.Revision != "" {
						reportedPath = settings.base.Executable
					}
					mobileRow(c, "当前可执行文件", reportedPath, "", nil, nil)
					if install.Configurable {
						formField(c, "覆盖路径", "自动发现", &settings.path).Disabled(authBusy || req.busy)
						if modern {
							a.runtimeSettingsFields(c, w, agent.ID, settings, authBusy || req.busy)
						}
					}
				}
			}
		}
		footer := func() {
			req.status(c)
			if modern && settings.stale {
				action(c, "放弃修改并重新读取", func() { loadSettings(true) }).AlignSelf(ui.Start).Disabled(blocked)
			} else if modern && settings.base.Revision == "" && req.err != "" {
				action(c, "重新读取运行设置", func() { loadSettings(false) }).AlignSelf(ui.Start).Disabled(blocked)
			}
			if modern && settings.saved && !settings.changed() && req.err == "" {
				oneLine(c, "已保存运行设置", captionTextSize, palette(c).secondary).PaddingY(8).Role(ui.RoleStatus)
			}
			if apiSetup && !unsaved {
				primaryAction(c, "连接 API", func() { a.connectAgentAPI(current, &account) }).
					Disabled(blocked || authBusy || strings.TrimSpace(account.endpoint) == "")
			} else if configured {
				primaryAction(c, "保存并继续", save).Disabled(settingsBlocked)
			}
		}
		ui.Column(c).Fill().Background(palette(c).canvas).Children(func() {
			mobilePageContent(c.Key("agent-header"), 0).Shrink(0).Children(func() {
				mobilePageContext(c, a.deviceName())
				mobileSegments(c, tabs, tab, func(value string) { tab = value })
			})
			mobileSettingsPanel(c.Key("agent-tabs"), tab, mobileSegmentIndex(tabs, tab), func() {
				switch tab {
				case "Skills":
					skills.view(a, c, w, current, nil, unsaved || req.busy)
				case "MCP":
					tools.view(a, c, w, unsaved || req.busy)
				case "Hooks":
					hooks.view(a, c, w, current, nil, unsaved || req.busy)
				default:
					mobilePageWithFooter(c, settingsContent, footer)
				}
			})
		})
	})
	w.OnClosed(func() {
		tools.closeForward()
		if !a.preview && draftKey[0] != "" && settings.changed() {
			settings.clearPrivateValues()
			if a.agentSettingsDrafts == nil {
				a.agentSettingsDrafts = make(map[[2]string]*mobileAgentSettingsDraft)
			}
			a.agentSettingsDrafts[draftKey] = settings
		}
		if draft := tools.draft; draft != nil && !draft.readOnly && draftKey[0] != "" {
			draft.clearSecrets()
			if a.mcpDrafts == nil {
				a.mcpDrafts = make(map[[2]string]*mobileToolEditor)
			}
			a.mcpDrafts[draftKey] = draft
		}
		tools.draft = nil
		if onReturn != nil && a.pageCurrent(parent) {
			onReturn()
		}
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) openWorkspace(parent *mygo.Window, workspace domain.Workspace) {
	workspace.Folders = slices.Clone(workspace.Folders)
	originalPrimary := ""
	if len(workspace.Folders) > 0 {
		originalPrimary = workspace.Folders[0]
	}
	newWorkspace := workspace.ID == ""
	title := "工作区设置"
	if newWorkspace {
		title = "新建工作区"
	}
	req := mobileRequest{}
	tab := "常规"
	tabs := []mobileSegment{{title: "常规"}, {title: "Skills"}, {title: "MCP"}, {title: "Hooks"}}
	tools := mobileWorkspaceTools{workspaceID: workspace.ID}
	skills := mobileManagedSkills{workspace: workspace.ID}
	hooks := mobileHooks{workspace: workspace.ID}
	var directoryMenu mobileContextMenu
	var w *mygo.Window
	save := func() {
		if !a.pageCurrent(w) || req.busy {
			return
		}
		if len(workspace.Folders) == 0 || strings.TrimSpace(workspace.Name) == "" {
			req.err = "请填写名称并选择主目录"
			w.Invalidate()
			return
		}
		draft := workspace
		draft.Name, draft.Folders = strings.TrimSpace(workspace.Name), slices.Clone(workspace.Folders)
		method, input := "workspace.update", any(draft)
		if newWorkspace {
			method, input = "workspace.create", protocol.WorkspaceInput{Name: draft.Name, Folders: draft.Folders}
		}
		a.pageJob(w, &req, method, input, func(_ json.RawMessage) error {
			if !newWorkspace && draft.Folders[0] != originalPrimary {
				delete(a.model.GitStatuses, "workspace:"+draft.ID)
				a.loadGit(draft.ID)
			}
			w.Pop()
			return nil
		})
	}
	w = a.page(title, func(c *ui.Context) {
		menus := contextMenuFrame{zoom: c.Zoom()}
		publishDirectoryMenu := func(_ *ui.Painter, bounds ui.Rect) { directoryMenu.publish(a, w, menus, bounds) }
		general := func() {
			editFolders := func() bool { return !req.busy && tab == "常规" && a.pageCurrent(w) }
			formField(c, "名称", "工作区名称", &workspace.Name).Disabled(req.busy)
			current := a.workspace(workspace.ID)
			if git := a.model.GitStatuses["workspace:"+workspace.ID]; git.Applicable &&
				len(current.Folders) != 0 && len(workspace.Folders) != 0 && current.Folders[0] == workspace.Folders[0] {
				formRow(c, "默认分支", func() {
					oneLine(c, git.DefaultBranch, 15, palette(c).secondary).MaxWidthPercent(58)
				})
			}
			mobileGroupHeading(c, "目录")
			for index, path := range workspace.Folders {
				title := "附加目录"
				if index == 0 {
					title = "主目录"
				}
				row := c.Key(path)
				folder := mobileRow(row, title, path, "", folderSymbol, func() {
					a.openDirectory(w, path, func(value string) {
						if i := slices.Index(workspace.Folders, path); i >= 0 && editFolders() && !slices.Contains(workspace.Folders, value) {
							workspace.Folders[i] = value
						}
					})
				}).Value(path).Disabled(req.busy).Transition(mobilePanelTransition)
				if !req.busy && len(workspace.Folders) > 1 {
					var commands []contextMenuCommand
					if index > 0 {
						commands = append(commands, contextMenuCommand{ID: "primary", Title: "设为主目录", Symbol: "checkmark.circle", run: func() {
							if i := slices.Index(workspace.Folders, path); i > 0 && editFolders() {
								copy(workspace.Folders[1:i+1], workspace.Folders[:i])
								workspace.Folders[0] = path
								w.Invalidate()
							}
						}})
					}
					commands = append(commands, contextMenuCommand{ID: "remove", Title: "移除目录", Symbol: "minus.circle", Destructive: true, run: func() {
						if i := slices.Index(workspace.Folders, path); i >= 0 && len(workspace.Folders) > 1 && editFolders() {
							workspace.Folders = slices.Delete(workspace.Folders, i, i+1)
							w.Invalidate()
						}
					}})
					menus.add(folder, path, contextMenuTarget{commands: commands})
				}
			}
			addDirectory := "添加附加目录"
			if len(workspace.Folders) == 0 {
				addDirectory = "添加主目录"
			}
			mobileRow(c, addDirectory, "", "", plusSymbol, func() {
				a.openDirectory(w, "", func(value string) {
					if editFolders() && !slices.Contains(workspace.Folders, value) {
						workspace.Folders = append(workspace.Folders, value)
					}
				})
			}).Disabled(req.busy)
			if !newWorkspace {
				mobileGroupHeading(c, "会话")
				mobileRow(c, "导入会话", "浏览该设备上的历史", "", filesSymbol, func() { a.openConversationImport(w, workspace) })
				mobileDetailRow(c.Key("archive-workspace"), "归档工作区", "", "归档工作区", filesSymbol, func() {
					a.confirmWorkspaceArchive(w, &req, current, func() { w.Pop() })
				}, nil).Disabled(req.busy || current.Archived || !a.canChangeWorkspaceArchive(current))
			}
			req.status(c)
		}
		if newWorkspace {
			mobilePageBody(c, func() {
				mobilePageContext(c, a.deviceName())
				general()
			}).DrawOver(publishDirectoryMenu)
			return
		}
		ui.Column(c).Fill().Background(palette(c).canvas).Children(func() {
			mobilePageContent(c.Key("workspace-header"), 8).Shrink(0).Children(func() {
				mobilePageContext(c, a.deviceName())
				mobileSegments(c, tabs, tab, func(next string) {
					tab = next
					if tab != "常规" {
						directoryMenu.publish(a, w, contextMenuFrame{}, ui.Rect{})
					}
				})
			})
			mobileSettingsPanel(c.Key("workspace-tabs"), tab, mobileSegmentIndex(tabs, tab), func() {
				switch tab {
				case "Skills":
					skills.workspaceView(a, c, w)
				case "MCP":
					tools.view(a, c, w)
				case "Hooks":
					hooks.workspaceView(a, c, w)
				default:
					mobilePageBody(c, general).DrawOver(publishDirectoryMenu)
				}
				if tab != "常规" && (req.busy || req.err != "") {
					mobileContent(c.Key("workspace-save-status"), contentMaxWidth).PaddingX(pageInset).Children(func() { req.status(c) })
				}
			})
		})
	})
	directoryMenu.listen(a, w)
	a.pushWithCommit(parent, w, "保存工作区", save)
}

func (a *mobileApp) openDirectory(parent *mygo.Window, initial string, accept func(string)) {
	req := mobileRequest{}
	current := domain.DirectoryPage{}
	var w *mygo.Window
	read := func(path string, offset int) {
		if a.preview {
			if path == "" {
				path = "/Users/shinya/work"
			}
			current = domain.DirectoryPage{Path: path, Parent: filepath.Dir(path)}
			names := map[string][]string{"work": {"Shioji", "Design tools", "archive"}, "Shioji": {"internal", "scripts", "docs"}}
			for _, name := range names[filepath.Base(path)] {
				current.Entries = append(current.Entries, domain.DirectoryEntry{Name: name, Path: filepath.Join(path, name)})
			}
			w.Invalidate()
			return
		}
		a.pageJob(w, &req, "device.directories", protocol.DirectoryQuery{Path: path, Offset: offset},
			decodePage(func(in domain.DirectoryPage) error {
				if offset > 0 {
					if in.Path != current.Path {
						return fmt.Errorf("远程目录已改变")
					}
					in.Entries = append(current.Entries, in.Entries...)
				}
				current = in
				return nil
			}))
	}
	w = a.page("选择远程目录", func(c *ui.Context) {
		ui.Column(c).Fill().Background(palette(c).canvas).Children(func() {
			mobilePageContent(c, 0).Children(func() {
				mobilePageContext(c, a.deviceName())
				if current.Path != "" {
					label(c, current.Path, captionTextSize, palette(c).secondary).Font("monospace").Selectable().Padding(0, 0, 8)
				}
			})
			ui.Scroll(c.Key("folder:" + current.Path)).Grow(1).Basis(0).MinHeight(0).Children(func() {
				mobileContent(c, contentMaxWidth).Padding(0, pageInset, pageInset).Children(func() {
					if current.Parent != "" && current.Parent != current.Path {
						mobileRow(c, "上一级", "", "", parentFolderSymbol, func() { read(current.Parent, 0) })
					}
					for _, entry := range current.Entries {
						mobileRow(c.Key(entry.Path), entry.Name, "", "", folderSymbol, func() { read(entry.Path, 0) })
					}
					if current.Path != "" && len(current.Entries) == 0 && !req.busy {
						mobileMessage(c, "此目录为空，仍可选择。")
					}
					if current.More {
						action(c, "加载更多", func() { read(current.Path, current.Next) }).Disabled(req.busy)
					}
					req.status(c)
					if req.err != "" {
						action(c, "重试", func() { read(current.Path, 0) })
					}
				})
			})
			mobilePageFooter(c, func() {
				primaryAction(c, "选择此目录", func() {
					accept(current.Path)
					parent.Invalidate()
					w.Pop()
				}).Disabled(current.Path == "" || req.busy)
			})
		})
	})
	read(initial, 0)
	a.pushNative(parent, w)
}

func readinessName(state domain.ReadinessState) string {
	switch state {
	case domain.RuntimeReady:
		return "就绪"
	case domain.RuntimeRefreshing:
		return "正在检查"
	case domain.RuntimeUnavailable:
		return "不可用"
	default:
		return "待核对"
	}
}
