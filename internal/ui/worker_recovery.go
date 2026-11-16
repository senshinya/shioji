package ui

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"slices"
	"strconv"
	"strings"
	"time"
)

type workerRecoveryUI struct {
	open, picker, all, update, risk, restoreUpdate bool
	selected                                       map[string]bool
	processes                                      map[string]bool
	query, confirm, error, initial                 string
	selection                                      app.WorkerCloseSelection
	deviceInput                                    domain.WorkerRecoveryInput
	names                                          []string
	next                                           time.Time
}

func (v *View) recoveryUIFor(key string) *workerRecoveryUI {
	if v.workerRecoveryStates == nil {
		v.workerRecoveryStates = map[string]*workerRecoveryUI{}
	}
	f := v.workerRecoveryStates[key]
	if f == nil {
		f = &workerRecoveryUI{selected: map[string]bool{}, processes: map[string]bool{}}
		v.workerRecoveryStates[key] = f
	}
	return f
}
func (v *View) openWorkerRecovery(key, session string, update bool) {
	f := v.recoveryUIFor(key)
	f.open = true
	f.confirm = ""
	f.initial = session
	f.update = update
	f.selected = map[string]bool{}
	f.processes = map[string]bool{}
	v.maintenanceUIFor(key).open = false
	if state := v.deviceState(key); state.Snapshot.DeviceID != "" {
		rc := v.Model.RecoveryClient(state.Snapshot.DeviceID)
		if !rc.QueryOnly && rc.Operation != nil && !rc.Operation.Active() {
			rc.Operation = nil
		}
	}

	v.devices = true
	v.deviceSelected = key
	v.deviceList = false
	s := v.deviceState(key)
	if s.Connected && s.WorkerService != nil && s.WorkerService.Build != nil && s.WorkerService.Build.ForceRecovery {
		job, err := v.Model.InspectWorkerMaintenance(key, update)
		v.recoverySubmit(key, job, err)
	} else {
		v.inspectIndependentRecovery(key)
	}
}
func (v *View) recoverySubmit(key string, job app.Job, err error) {
	f := v.recoveryUIFor(key)
	if err != nil {
		f.error = err.Error()
		return
	}
	f.error = ""
	mutating := job.Method == "worker.maintenance.apply" || job.Method == "worker.maintenance.cancel"
	if in, ok := job.Input.(app.WorkerRecoveryAction); ok {
		mutating = in.Action == "start" || in.Action == "cancel"
	}
	if mutating && v.Saver != nil {
		v.queueState()
		job.Prepare = v.Saver.FlushLatest
	}
	if !v.do(job) {
		f.error = "操作队列繁忙，尚未发送，请查询原操作"
		id := v.deviceState(key).Snapshot.DeviceID
		v.Model.RecoveryClient(id).Busy = false
		v.Model.MaintenanceClient(id).Busy = false
	}
	f.next = v.frameTime.Add(2 * time.Second)
}
func (v *View) inspectIndependentRecovery(key string) {
	job, err := v.Model.WorkerRecoveryJob(key, "inspect", domain.WorkerRecoveryInput{})
	v.recoverySubmit(key, job, err)
}
func recoveryEvidence(p domain.WorkerRecoveryProcess) string {
	text := map[string]string{"registered": "已登记", "observed": "由当前进程树观察", "worker": "执行服务", "historical": "历史身份记录"}[p.Evidence]
	if text == "" {
		text = "来源未确认"
	}
	if p.Protected {
		text = p.Reason
	}
	return text
}
func (v *View) recoverySelection(key string, mc *app.WorkerMaintenanceClient, rc *app.WorkerRecoveryClient, force bool) (app.WorkerCloseSelection, []string) {
	f := v.recoveryUIFor(key)
	out := app.WorkerCloseSelection{Force: force, Update: f.update}
	var names []string
	if op := mc.Operation; op != nil && op.Phase.Active() {
		for _, r := range op.Targets {
			if r.State == "awaitingForce" && f.selected[r.Target.SessionID] {
				target := r.Target
				target.ProcessRef = r.ProcessRef
				out.Targets = append(out.Targets, target)
				names = append(names, r.WorkspaceName+" · "+r.SessionName)
			}
		}
		out.ProcessPreviewRef = op.ProcessPreviewRef
		for _, r := range op.Processes {
			if r.State == "awaitingForce" && f.processes[r.Process.Ref] {
				out.ProcessRefs = append(out.ProcessRefs, r.Process.Ref)
				names = append(names, fmt.Sprintf("%s · PID %d", r.Process.Name, r.Process.PID))
			}
		}
	} else {
		if mc.Diagnosis != nil {
			for _, b := range mc.Diagnosis.Environments {
				target := b.ForceTarget
				if !force && b.Target == nil {
					target = nil
				}
				if target != nil && f.selected[b.SessionID] {
					out.Targets = append(out.Targets, *target)
					names = append(names, b.WorkspaceName+" · "+b.SessionName)
				}
			}
		}
		if rc.Preview != nil {
			out.ProcessPreviewRef = rc.Preview.Ref
			for _, p := range rc.Preview.Processes {
				if f.processes[p.Ref] && !p.Protected && p.SessionID == "" {
					out.ProcessRefs = append(out.ProcessRefs, p.Ref)
					names = append(names, fmt.Sprintf("%s · PID %d · %s", p.Name, p.PID, recoveryEvidence(p)))
				}
			}
		}
	}
	return out, names
}
func (v *View) workerRecoveryPage(c *n.Context, t *n.Theme, key, name string, s *app.DeviceState) {
	f := v.recoveryUIFor(key)
	id := s.Snapshot.DeviceID
	if id == "" {
		if key == "local" {
			id = v.Model.LocalDeviceID
		}
		for _, connection := range v.Model.Connections {
			if connection.ID == key {
				id = connection.DeviceID
			}
		}
	}
	mc, rc := v.Model.MaintenanceClient(id), v.Model.RecoveryClient(id)
	modern := s.Connected && s.WorkerService != nil && s.WorkerService.Build != nil && s.WorkerService.Build.ForceRecovery
	if f.initial != "" && mc.Diagnosis != nil {
		f.selected[f.initial] = true
		f.initial = ""
	}
	if f.confirm == "" && !f.picker && !v.frameTime.Before(f.next) {
		if !rc.Busy && (rc.QueryOnly || rc.Operation != nil && rc.Operation.Active()) {
			job, err := v.Model.WorkerRecoveryJob(key, "status", domain.WorkerRecoveryInput{})
			v.recoverySubmit(key, job, err)
		} else if modern && !mc.Busy && (mc.QueryOnly || mc.Operation != nil && mc.Operation.Phase.Active()) {
			job, err := v.Model.QueryWorkerMaintenance(key)
			v.recoverySubmit(key, job, err)
		}
	}
	c.After(time.Second)
	n.Column(c).FillWidth().Padding(24, configurationInset, 16).Gap(8).Children(func() {
		if configurationAction(c, "设备详情").Label("返回设备详情").AlignSelf(n.Start).MarginStart(-8).Clicked() {
			f.open = false
			f.confirm = ""
		}
		n.Text(c, "后台环境处理").FontSize(configurationTitleSize).FixedLineHeight(configurationTitleLine)
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
			n.Text(c, v.maintenanceDeviceIdentity(key, name)).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Grow(1)
			if configurationAction(c, "重新检查").Disabled(mc.Busy || rc.Busy).Clicked() {
				f.selected = map[string]bool{}
				f.processes = map[string]bool{}
				if modern {
					job, err := v.Model.InspectWorkerMaintenance(key, f.update)
					v.recoverySubmit(key, job, err)
				} else {
					v.inspectIndependentRecovery(key)
				}
			}
		})
	})
	n.Column(c).FillWidth().Grow(1).FillHeight().Padding(0, configurationInset, 20).Gap(12).Children(func() {
		title, message := "选择需要关闭的环境", "正在回复、未登记的后台服务均可明确选择。关闭后保留历史与草稿，待发队列暂停。"
		if !modern {
			title = "执行服务恢复"
			message = "此入口通过独立恢复程序检查设备，不依赖旧执行服务响应。"
		}
		if mc.Operation != nil {
			title, message = maintenanceTitle(mc)
			if mc.Operation.Standalone && mc.Operation.Phase == domain.WorkerMaintenanceCompleted {
				title = "所选环境已关闭"
				message = "可以返回会话核对旧轮，再继续；待发队列保持暂停。"
			}
		}
		if rc.Operation != nil {
			title = "执行服务 · " + recoveryPhase(rc.Operation.Phase)
			message = rc.Operation.Message
		}
		n.Text(c, title).FontSize(15).FixedLineHeight(24)
		n.Text(c, message).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		if mc.QueryOnly || rc.QueryOnly {
			n.Text(c, "回执尚未确认，只查询原操作，不会重复强制关闭。").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		}
		n.Scroll(c.Key("recovery-environments")).FillWidth().Grow(1).FillHeight().Children(func() {
			n.Column(c).FillWidth().Children(func() {
				if rc.Operation != nil {
					for _, r := range rc.Operation.Results {
						n.Text(c, fmt.Sprintf("%s · PID %d · %s", r.Process.Name, r.Process.PID, r.State)).FillWidth().PaddingY(5).FontSize(12).FixedLineHeight(20)
						if r.Message != "" {
							n.Text(c, r.Message).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
						}
					}
				} else if modern && mc.Operation != nil {
					for _, r := range mc.Operation.Targets {
						v.recoveryEnvironment(c.Key(r.Target.SessionID), t, key, r.Target.SessionID, r.SessionName, r.WorkspaceName, r.State, r.Message, r.State == "awaitingForce", r.Processes)
					}
					for _, r := range mc.Operation.Processes {
						n.Row(c.Key(r.Process.Ref)).FillWidth().PaddingY(8).Gap(8).Children(func() {
							if r.State == "awaitingForce" {
								on := f.processes[r.Process.Ref]
								configurationCheckbox(c, &on, "", n.Font{Family: t.Font, Size: 13}, r.Process.Name)
								f.processes[r.Process.Ref] = on
							}
							n.Text(c, fmt.Sprintf("%s · PID %d · %s", r.Process.Name, r.Process.PID, r.State)).FontSize(12).FixedLineHeight(20)
						})
						if r.Message != "" {
							n.Text(c, r.Message).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
						}
					}
				} else if modern && mc.Diagnosis != nil {
					for _, b := range mc.Diagnosis.Environments {
						v.recoveryEnvironment(c.Key(b.ID), t, key, b.SessionID, b.SessionName, b.WorkspaceName, string(b.Status), b.Reason, b.ForceTarget != nil, b.Processes)
					}
					if len(mc.Diagnosis.Environments) == 0 {
						n.Text(c, "未观察到常驻会话环境；可检查其他进程或恢复执行服务。").FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
					}
				} else if rc.Preview != nil {
					for _, w := range rc.Preview.Warnings {
						n.Text(c, w).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
					}
					for _, session := range rc.Preview.Sessions {
						n.Text(c, session.Title+" · "+activityStatus(string(session.Status))).FillWidth().PaddingY(6).FontSize(13).FixedLineHeight(20)
					}
				}
				if rc.Preview != nil {
					for _, p := range rc.Preview.Processes {
						if f.processes[p.Ref] {
							n.Text(c, fmt.Sprintf("另选进程 · %s · PID %d", p.Name, p.PID)).FillWidth().FontSize(12).FixedLineHeight(20)
						}
					}
				}
			})
		})
		errorText := f.error
		if errorText == "" {
			errorText = mc.Error
		}
		if errorText == "" {
			errorText = rc.Error
		}
		if errorText != "" {
			n.Text(c, errorText).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Label("恢复错误")
		}
		if !s.Connected && rc.Error != "" {
			n.Text(c, "SSH 无法到达时，请重新连接，或在该设备打开最新版 Shioji 的执行服务恢复入口。").FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			directory := ""
			for _, connection := range v.Model.Connections {
				if connection.ID == key {
					directory = connection.ActiveSSH().StateDirectory
				}
			}
			if directory != "" {
				quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
				command := "shioji-worker --state-dir " + quote(directory) + " --recovery-helper inspect --recovery-device " + quote(id)
				if configurationAction(c, "复制设备端检查命令").AlignSelf(n.Start).Clicked() {
					c.WriteClipboard(command)
				}
			}
		}

		deviceDivider(c)
		// A separate row keeps the escape path reachable at narrow widths.
		n.Row(c).FillWidth().Gap(8).Children(func() {
			if configurationAction(c, "选择其他进程…").Disabled(rc.Busy).Clicked() {
				f.picker = true
				v.inspectIndependentRecovery(key)
			}
			n.Spacer(c).Grow(1)
			if configurationAction(c, "导出诊断").Clicked() {
				v.exportRecovery(key, mc, rc)
			}
			if configurationAction(c, "强制重启执行服务…").Label("强制重启执行服务入口").Disabled(rc.Busy).Clicked() {
				v.inspectIndependentRecovery(key)
				f.confirm = "device-preview"
				f.risk = false
			}
		})
		if modern {
			if s.WorkerService.Available != nil {
				configurationCheckbox(c, &f.update, "关闭后更新执行服务", n.Font{Family: t.Font, Size: 12}, "关闭后更新执行服务")
			}
			n.Row(c).FillWidth().Gap(8).Children(func() {
				n.Spacer(c).Grow(1)
				if mc.Operation != nil && mc.Operation.Phase.Active() && mc.Operation.Phase != domain.WorkerMaintenanceUpdating {
					if configurationAction(c, "取消本次处理").Disabled(mc.Busy || mc.QueryOnly).Clicked() {
						job, err := v.Model.CancelWorkerMaintenance(key)
						v.recoverySubmit(key, job, err)
					}
				}
				if mc.QueryOnly {
					if configurationAction(c, "查询处理结果").Disabled(mc.Busy).Clicked() {
						job, err := v.Model.QueryWorkerMaintenance(key)
						v.recoverySubmit(key, job, err)
					}
				} else {
					for _, force := range []bool{false, true} {
						selection, names := v.recoverySelection(key, mc, rc, force)
						label := "正常结束…"
						accessibility := "正常结束所选环境"
						if force {
							label = "强制关闭…"
							accessibility = "强制关闭所选环境"
						}
						if configurationAction(c, label).Label(accessibility).Disabled(mc.Busy || len(names) == 0 || v.Controller == nil).Clicked() {
							f.selection = selection
							f.names = slices.Clone(names)
							f.risk = false
							f.confirm = "close"
						}
					}
				}
			})
		} else if rc.Preview != nil && rc.Operation == nil {
			if maintenancePrimary(c, "强制重启执行服务…").Label("预览强制重启执行服务").AlignSelf(n.End).Disabled(rc.Busy).Clicked() {
				f.confirm = "device-preview"
				f.risk = false
			}
		}
		if rc.Operation != nil && rc.Operation.Active() {
			if configurationAction(c, "取消尚未开始的动作").AlignSelf(n.End).Disabled(rc.Busy || rc.Operation.Phase == "starting" || rc.Operation.Phase == "verifying").Clicked() {
				job, err := v.Model.WorkerRecoveryJob(key, "cancel", domain.WorkerRecoveryInput{})
				v.recoverySubmit(key, job, err)
			}
		}
	})
	if f.confirm == "device-preview" && !rc.Busy && (rc.Error != "" || rc.QueryOnly || rc.Operation != nil && rc.Operation.Active()) {
		f.confirm = ""
	}
	if f.confirm == "device-preview" && !rc.Busy && rc.Preview != nil && !rc.QueryOnly && (rc.Operation == nil || !rc.Operation.Active()) {
		f.restoreUpdate = false
		f.deviceInput = domain.WorkerRecoveryInput{ID: domain.NewID(), ActionID: domain.NewID(), Action: "restart", DeviceID: id, PreviewRef: rc.Preview.Ref}
		f.names = nil
		active := 0
		for _, session := range rc.Preview.Sessions {
			f.names = append(f.names, session.Title+" · "+activityStatus(string(session.Status)))
			if session.Status == "running" || session.Status == "starting" || session.Status == "unknown" {
				active++
			}
		}
		impact := fmt.Sprintf("此设备所有执行会话都会断开；%d 个正在运行或待核对任务将中断", active)
		if rc.Preview.ScopeUnknown {
			impact = "任务数量和范围无法核对，确认将关闭此设备全部执行环境"
		}
		f.names = append([]string{impact}, f.names...)
		for _, p := range rc.Preview.Processes {
			if f.processes[p.Ref] && !p.Protected && p.SessionID == "" {
				f.deviceInput.ProcessRefs = append(f.deviceInput.ProcessRefs, p.Ref)
				f.names = append(f.names, fmt.Sprintf("另选 %s · PID %d", p.Name, p.PID))
			}
		}
		f.confirm = "device"
	}
	v.recoveryConfirm(c, t, key, name, mc, rc)
	v.recoveryPicker(c, t, key, rc)
}
func recoveryPhase(phase string) string {
	return map[string]string{"accepted": "已接纳", "fencing": "暂停派发", "stopping": "正在关闭", "starting": "正在启动", "verifying": "正在核对", "completed": "已恢复", "failed": "恢复失败", "partial": "部分完成，请重新检查", "cancelled": "已取消"}[phase]
}
func (v *View) recoveryEnvironment(c *n.Context, t *n.Theme, key, id, title, workspace, status, reason string, selectable bool, processes []domain.WorkerProcessSummary) {
	f := v.recoveryUIFor(key)
	n.Column(c).FillWidth().PaddingY(12).BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Gap(4).Children(func() {
		n.Row(c).FillWidth().Gap(10).AlignItems(n.Start).Children(func() {
			on := f.selected[id]
			configurationCheckbox(c, &on, "", n.Font{Family: t.Font, Size: 13}, title).Label("选择 " + title).Disabled(!selectable)
			f.selected[id] = on
			n.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				n.Text(c, title).FillWidth().FontSize(13).FixedLineHeight(20)
				label := activityStatus(status)
				if status == "running" {
					label = "正在运行 · 关闭会打断回复"
				}
				if status == "completed" {
					label = "会话已完成 · 仍有后台服务"
				}
				if status == "awaitingForce" {
					label = "尚未退出，可强制关闭"
				}
				if status == "stopped" {
					label = "已关闭"
				}
				n.Text(c, workspace+" · "+label).FillWidth().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
			})
			if configurationAction(c, "查看会话").Label("查看会话 " + title).Clicked() {
				for _, s := range v.Model.Snapshot.Sessions {
					if s.ID == id {
						v.navigateSession(id, s.WorkspaceID)
						v.devices = false
						break
					}
				}
			}
		})
		v.maintenanceProcessDetails(c, t, key, id, processes, reason)
	})
}
func (v *View) recoveryConfirm(c *n.Context, t *n.Theme, key, name string, mc *app.WorkerMaintenanceClient, rc *app.WorkerRecoveryClient) {
	f := v.recoveryUIFor(key)
	if f.confirm != "close" && f.confirm != "device" {
		return
	}
	device := f.confirm == "device"
	open := true
	n.DialogBase(c.Key("force-recovery-confirm"), &open, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .2))
		width, _ := c.Size()
		panel.Width(min(float32(480), width-32)).Padding(configurationDialogInset).Radius(10).Background(raised(t)).Border(1, t.Border).Gap(12).Label("确认恢复范围")
		panel.Children(func() {
			title := "正常结束所选环境？"
			if f.selection.Force {
				title = "强制关闭所选环境？"
			}
			if device {
				title = "强制重启执行服务？"
			}
			n.Text(c, title).FontSize(15).FixedLineHeight(24)
			n.Text(c, v.maintenanceDeviceIdentity(key, name)).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			n.Scroll(c).FillWidth().MaxHeight(220).Children(func() {
				n.Column(c).FillWidth().Gap(6).Children(func() {
					for _, name := range f.names {
						n.Text(c, name).FillWidth().FontSize(13).FixedLineHeight(20)
					}
				})
			})
			n.Text(c, "尚未写入的内存状态可能丢失").FontSize(13).FixedLineHeight(20)
			n.Text(c, "已写入文件、会话历史、附件与草稿保留。待发队列暂停，不会自动续答。").FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			if device {
				n.Text(c, "Tailnet 与设备 SSH 连接保留。").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			}
			if device && rc.Preview != nil {
				if rc.Preview.RestoreBuildID != "" {
					n.Text(c, "恢复原构建 · "+shortBuildID(rc.Preview.RestoreBuildID)).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
				}
				if rc.Preview.CandidateBuildID != "" {
					configurationCheckbox(c, &f.restoreUpdate, "使用当前应用构建恢复执行服务", n.Font{Family: t.Font, Size: 12}, "使用当前应用构建恢复执行服务").Label("使用当前应用构建恢复执行服务")
				}
			}

			required := device || f.selection.Force || len(f.selection.ProcessRefs) > 0
			if required {
				configurationCheckbox(c, &f.risk, "我已了解强制关闭的影响", n.Font{Family: t.Font, Size: 12}, "我已了解强制关闭的影响").Label("我已了解强制关闭的影响")
			}
			n.Row(c).FillWidth().Justify(n.End).Gap(8).Children(func() {
				if configurationAction(c, "取消").Label("取消恢复确认").Clicked() {
					open = false
				}
				label := "确认正常结束"
				if f.selection.Force {
					label = "确认强制关闭"
				}
				if device {
					label = "确认强制重启"
				}
				if maintenancePrimary(c, label).Disabled(required && !f.risk || mc.Busy || rc.Busy || v.Controller == nil).Clicked() {
					if device {
						f.deviceInput.RiskAccepted = f.risk
						f.deviceInput.Update = f.restoreUpdate
						if f.restoreUpdate && rc.Preview != nil {
							f.deviceInput.BuildID = rc.Preview.CandidateBuildID
						}
						job, err := v.Model.WorkerRecoveryJob(key, "start", f.deviceInput)
						v.recoverySubmit(key, job, err)
					} else {
						f.selection.RiskAccepted = f.risk
						job, err := v.Model.CloseWorkerEnvironments(key, f.selection)
						v.recoverySubmit(key, job, err)
					}
					open = false
				}
			})
		})
	})
	if !open {
		f.confirm = ""
		f.risk = false
	}
}
func (v *View) recoveryPicker(c *n.Context, t *n.Theme, key string, rc *app.WorkerRecoveryClient) {
	f := v.recoveryUIFor(key)
	if !f.picker {
		return
	}
	n.DialogBase(c.Key("recovery-picker"), &f.picker, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .2))
		width, height := c.Size()
		panel.Width(min(float32(600), width-32)).Height(min(float32(620), height-40)).Padding(configurationDialogInset).Radius(10).Background(raised(t)).Border(1, t.Border).Gap(10)
		panel.Children(func() {
			n.Text(c, "选择其他进程").FontSize(15).FixedLineHeight(24)
			configurationTextField(c.Key("process-search"), &f.query).Placeholder("搜索名称或 PID").Label("搜索进程名称或 PID")
			configurationCheckbox(c, &f.all, "显示此用户的所有进程", n.Font{Family: t.Font, Size: 12}, "显示此用户的所有进程")
			n.Scroll(c).FillWidth().Grow(1).FillHeight().Children(func() {
				n.Column(c).FillWidth().Children(func() {
					if rc.Preview != nil {
						for _, p := range rc.Preview.Processes {
							if !f.all && (p.Evidence == "" || p.Evidence == "unknown") && !p.Protected {
								continue
							}
							if q := strings.ToLower(strings.TrimSpace(f.query)); q != "" && !strings.Contains(strings.ToLower(p.Name)+" "+strconv.Itoa(p.PID), q) {
								continue
							}
							n.Column(c.Key(p.Ref)).FillWidth().PaddingY(8).Gap(2).Children(func() {
								on := f.processes[p.Ref] || p.SessionID != "" && f.selected[p.SessionID]
								configurationCheckbox(c, &on, fmt.Sprintf("%s · PID %d", p.Name, p.PID), n.Font{Family: t.Font, Size: 12}, p.Ref).Label("选择进程 " + p.Ref).Disabled(p.Protected)
								if p.SessionID != "" {
									f.selected[p.SessionID] = on
								} else {
									f.processes[p.Ref] = on
								}
								n.Text(c, p.StartedAt.Local().Format("01-02 15:04:05")+" · "+recoveryEvidence(p)).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
								if p.SessionID != "" {
									n.Text(c, "属于会话环境，选择将关闭完整环境").FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
								}
							})
						}
					}
				})
			})
			if rc.Error != "" {
				n.Text(c, rc.Error).FillWidth().FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			}
			n.Row(c).FillWidth().Justify(n.End).Gap(8).Children(func() {
				if configurationAction(c, "刷新进程").Disabled(rc.Busy).Clicked() {
					v.inspectIndependentRecovery(key)
				}
				if configurationAction(c, "完成选择").Clicked() {
					f.picker = false
				}
			})
		})
	})
}
func (v *View) exportRecovery(key string, mc *app.WorkerMaintenanceClient, rc *app.WorkerRecoveryClient) {
	if v.Controller == nil {
		return
	}
	data, _ := json.MarshalIndent(struct {
		Diagnosis   *domain.WorkerUpdateDiagnosis
		Maintenance *domain.WorkerMaintenance
		Recovery    *domain.WorkerRecoveryOperation
	}{mc.Diagnosis, mc.Operation, rc.Operation}, "", "  ")
	parent := v.nativeWindow
	v.Controller.AttachmentTask(func() app.Update {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: parent, Title: "导出恢复诊断", DefaultPath: "shioji-worker-diagnostic.json", CreateDirectories: true})
		if err == nil && path != "" {
			err = os.WriteFile(path, data, 0600)
		}
		return func(*app.Model) {
			if err != nil {
				v.recoveryUIFor(key).error = err.Error()
			}
		}
	})
}

func shortBuildID(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
