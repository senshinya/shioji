package ui

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"time"
)

type workerMaintenanceUI struct {
	open      bool
	selected  map[string]bool
	details   map[string]bool
	confirm   string
	confirmed []domain.WorkerMaintenanceTarget
	next      time.Time
	error     string
	diagnosis time.Time
}

func (v *View) maintenanceUIFor(key string) *workerMaintenanceUI {
	if v.workerMaintenanceStates == nil {
		v.workerMaintenanceStates = map[string]*workerMaintenanceUI{}
	}
	f := v.workerMaintenanceStates[key]
	if f == nil {
		f = &workerMaintenanceUI{selected: map[string]bool{}, details: map[string]bool{}}
		v.workerMaintenanceStates[key] = f
	}
	return f
}
func (v *View) maintenanceJob(key string, job app.Job, err error) {
	f := v.maintenanceUIFor(key)
	if err != nil {
		f.error = err.Error()
		return
	}
	f.error = ""
	if job.Method == "worker.maintenance.apply" || job.Method == "worker.maintenance.cancel" {
		if v.Saver != nil {
			v.queueState()
			job.Prepare = v.Saver.FlushLatest
		}
	}
	if !v.do(job) {
		if s := v.Model.DeviceStates[key]; s != nil {
			client := v.Model.MaintenanceClient(s.Snapshot.DeviceID)
			client.Busy = false
			client.Error = "设备操作队列繁忙，请查询当前状态"
		}
	}
	f.next = v.frameTime.Add(time.Second)
}
func (v *View) openWorkerMaintenance(key string) {
	f := v.maintenanceUIFor(key)
	f.open = true
	f.confirm = ""
	f.selected = map[string]bool{}
	job, err := v.Model.InspectWorkerMaintenance(key, true)
	v.maintenanceJob(key, job, err)
}
func (v *View) maintenanceTargets(key string, client *app.WorkerMaintenanceClient, force bool) []domain.WorkerMaintenanceTarget {
	f := v.maintenanceUIFor(key)
	var out []domain.WorkerMaintenanceTarget
	if force && client.Operation != nil {
		for _, r := range client.Operation.Targets {
			if r.State == "awaitingForce" && f.selected[r.Target.SessionID] {
				target := r.Target
				target.ProcessRef = r.ProcessRef
				out = append(out, target)
			}
		}
	} else if client.Diagnosis != nil {
		for _, b := range client.Diagnosis.Blockers {
			if b.Target != nil && f.selected[b.SessionID] {
				out = append(out, *b.Target)
			}
		}
	}
	return out
}
func maintenanceTitle(op *domain.WorkerMaintenance) (string, string) {
	if op == nil {
		return "选择要结束的后台环境", "会话已经结束，但后台环境仍在运行。"
	}
	switch op.Phase {
	case domain.WorkerMaintenanceStopping:
		return "正在结束所选后台环境", "先正常关闭；未退出的环境需要另行确认。"
	case domain.WorkerMaintenanceNeedsForce:
		return "部分后台进程未退出", "可以单独确认强制结束，或取消本次处理。"
	case domain.WorkerMaintenanceWaiting:
		return "所选后台环境已结束", "等待设备的其他任务结束后安全更新。"
	case domain.WorkerMaintenanceUpdating:
		return "正在更新执行服务", "正在更新，请等待重新连接。"
	case domain.WorkerMaintenanceCompleted:
		return "执行服务已更新", "已重新连接并确认实际构建，会话历史与草稿已保留。"
	case domain.WorkerMaintenanceCancelled:
		return "已取消本次处理", "已结束的环境不会恢复，待发消息保持暂停。"
	default:
		return "本次处理未完成", op.Message
	}
}
func (v *View) workerMaintenancePage(c *n.Context, t *n.Theme, key, name string, state *app.DeviceState) {
	f := v.maintenanceUIFor(key)
	client := v.Model.MaintenanceClient(state.Snapshot.DeviceID)
	op := client.Operation
	if client.Diagnosis != nil && !client.Diagnosis.CheckedAt.Equal(f.diagnosis) {
		f.diagnosis = client.Diagnosis.CheckedAt
		if op == nil || !op.Phase.Active() {
			f.selected = map[string]bool{}
		}
	}
	if state.Connected && !client.Busy && f.confirm == "" && !v.frameTime.Before(f.next) && (client.QueryOnly || op != nil && op.Phase.Active()) {
		job, err := v.Model.QueryWorkerMaintenance(key)
		v.maintenanceJob(key, job, err)
	}
	c.After(time.Second)
	n.Column(c.Key("maintenance-header")).FillWidth().Padding(24, configurationInset, 16).Gap(8).Children(func() {
		if configurationAction(c, "设备详情").Label("返回设备详情").AlignSelf(n.Start).MarginStart(-8).Clicked() {
			f.open = false
			f.confirm = ""
		}
		n.Text(c, "更新执行服务").FontSize(configurationTitleSize).FixedLineHeight(configurationTitleLine)
		identity := name
		for _, connection := range v.Model.Connections {
			if connection.ID == key {
				identity += " · " + connection.ActiveSSH().Target
			}
		}
		n.Row(c).FillWidth().AlignItems(n.Center).Children(func() {
			n.Text(c, identity).Grow(1).MinWidth(0).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			if configurationAction(c, "重新检查").Disabled(client.Busy || !state.Connected).Clicked() {
				job, err := v.Model.InspectWorkerMaintenance(key, true)
				v.maintenanceJob(key, job, err)
			}
		})
	})
	n.Column(c).FillWidth().FillHeight().Grow(1).Padding(0, configurationInset, 24).Gap(16).Children(func() {
		title, description := maintenanceTitle(op)
		n.Column(c).FillWidth().Gap(6).Children(func() {
			n.Text(c, title).FontSize(15).FixedLineHeight(24)
			n.Text(c, description).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		})
		if client.QueryOnly {
			n.Text(c, "上次回执未确认，只查询原操作，不会重复结束进程。").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		}
		n.Scroll(c.Key("maintenance-list")).FillWidth().FillHeight().Grow(1).Children(func() {
			n.Column(c).FillWidth().Children(func() {
				if op != nil {
					for _, r := range op.Targets {
						r := r
						n.Column(c.Key(r.Target.SessionID)).FillWidth().PaddingY(12).Gap(6).BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Children(func() {
							n.Row(c).FillWidth().Gap(10).AlignItems(n.Start).Children(func() {
								if r.State == "awaitingForce" {
									on := f.selected[r.Target.SessionID]
									configurationCheckbox(c, &on, "", n.Font{Family: t.Font, Size: 13}, r.SessionName).Label("选择 " + r.SessionName)
									f.selected[r.Target.SessionID] = on
								}
								n.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
									n.Text(c, r.SessionName).FontSize(13).FixedLineHeight(20)
									n.Text(c, r.WorkspaceName).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
								})
								label := map[string]string{"stopped": "已结束", "stopping": "正在关闭", "awaitingForce": "尚未退出", "unknown": "归属待核对"}[r.State]
								n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
							})
							if r.Message != "" {
								n.Text(c, r.Message).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
							}
							v.maintenanceProcessDetails(c, t, key, r.Target.SessionID, r.Processes)
						})
					}
					if op.Phase == domain.WorkerMaintenanceUpdating {
						n.Text(c, "已完成更新前备份").PaddingY(12).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
					}
				} else if client.Diagnosis != nil {
					if len(client.Diagnosis.Blockers) == 0 {
						n.Text(c, "当前没有更新阻塞").FontSize(13).FixedLineHeight(20)
					}
					for _, b := range client.Diagnosis.Blockers {
						v.maintenanceBlocker(c.Key(b.ID), t, key, b)
					}
				} else {
					n.Text(c, "正在检查此设备的更新阻塞…").FontSize(12).TextColor(t.TextMuted)
				}
			})
		})
		message := f.error
		if message == "" {
			message = client.Error
		}
		if message != "" {
			n.Text(c, message).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Label("后台环境处理错误")
		}
		deviceDivider(c)
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
			summary := "待发消息将保留暂停，草稿仍可编辑"
			if op == nil {
				summary = fmt.Sprintf("已选 %d 个后台环境", len(v.maintenanceTargets(key, client, false)))
			}
			n.Text(c, summary).Grow(1).MinWidth(0).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
			disabled := client.Busy || !state.Connected || v.Controller == nil
			if op != nil && op.Phase.Active() {
				if op.Phase != domain.WorkerMaintenanceUpdating {
					if configurationAction(c, "取消本次处理").Disabled(disabled || client.QueryOnly).Clicked() {
						job, err := v.Model.CancelWorkerMaintenance(key)
						v.maintenanceJob(key, job, err)
					}
				}
				if op.Phase == domain.WorkerMaintenanceNeedsForce {
					targets := v.maintenanceTargets(key, client, true)
					if maintenancePrimary(c, "强制结束…").Label("强制结束所选环境").Disabled(disabled || client.QueryOnly || len(targets) == 0).Clicked() {
						f.confirm = "force"
						f.confirmed = targets
					}
				}
			} else {
				selectable := false
				if client.Diagnosis != nil {
					for _, b := range client.Diagnosis.Blockers {
						selectable = selectable || b.Target != nil
					}
				}
				if !selectable {
					if configurationAction(c, "导出诊断").Disabled(client.Diagnosis == nil).Clicked() {
						v.exportWorkerMaintenance(key, client)
					}
				}
				if configurationAction(c, "继续等待").Clicked() {
					f.open = false
				}
				if op == nil {
					targets := v.maintenanceTargets(key, client, false)
					if maintenancePrimary(c, "结束所选环境并更新…").Label("结束所选环境并更新").Disabled(disabled || client.QueryOnly || len(targets) == 0).Clicked() {
						f.confirm = "stop"
						f.confirmed = targets
					}
				}
			}
			if client.QueryOnly {
				if configurationAction(c, "查询处理结果").Disabled(disabled).Clicked() {
					job, err := v.Model.QueryWorkerMaintenance(key)
					v.maintenanceJob(key, job, err)
				}
			}
		})
	})
	v.workerMaintenanceConfirm(c, t, key, name, state, client)
}
func (v *View) maintenanceBlocker(c *n.Context, t *n.Theme, key string, b domain.WorkerUpdateBlocker) {
	f := v.maintenanceUIFor(key)
	n.Column(c).FillWidth().PaddingY(12).Gap(6).BorderWidth(0, 0, .5, 0).BorderColor(t.Border).Children(func() {
		n.Row(c).FillWidth().Gap(10).AlignItems(n.Start).Children(func() {
			if b.Target != nil {
				on := f.selected[b.SessionID]
				configurationCheckbox(c, &on, "", n.Font{Family: t.Font, Size: 13}, b.SessionName).Label("选择 " + b.SessionName)
				f.selected[b.SessionID] = on
			}
			n.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				title := b.SessionName
				if title == "" {
					title = "后台环境的归属待核对"
				}
				n.Text(c, title).FontSize(13).FixedLineHeight(20)
				meta := b.WorkspaceName
				if b.Status != "" {
					meta += " · " + activityStatus(string(b.Status))
				}
				n.Text(c, meta).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
			})
			if b.SessionID != "" && b.Kind != domain.WorkerBlockerUnknown {
				if configurationAction(c, "查看会话").Label("查看会话 " + b.SessionName).Clicked() {
					v.navigateSession(b.SessionID, b.WorkspaceID)
					v.devices = false
				}
			}
		})
		n.Text(c, b.Reason).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		v.maintenanceProcessDetails(c, t, key, b.ID, b.Processes)
	})
}
func (v *View) maintenanceProcessDetails(c *n.Context, t *n.Theme, key, id string, processes []domain.WorkerProcessSummary) {
	if len(processes) == 0 {
		return
	}
	f := v.maintenanceUIFor(key)
	open := f.details[id]
	deviceDisclosure(c.Key("processes"), "进程详情", &open, func() {
		for _, p := range processes {
			n.Text(c, fmt.Sprintf("%s · PID %d", p.Name, p.PID)).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
		}
	})
	f.details[id] = open
}
func (v *View) workerMaintenanceConfirm(c *n.Context, t *n.Theme, key, name string, state *app.DeviceState, client *app.WorkerMaintenanceClient) {
	f := v.maintenanceUIFor(key)
	if f.confirm == "" {
		return
	}
	force := f.confirm == "force"
	open := true
	n.DialogBase(c.Key("maintenance-confirm"), &open, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .2))
		width, _ := c.Size()
		panel.Width(min(float32(460), width-32)).Padding(configurationDialogInset).Radius(10).Background(raised(t)).Border(1, t.Border).Gap(12).Label("确认结束后台环境")
		panel.Children(func() {
			title := fmt.Sprintf("结束 %d 个会话的后台环境并更新？", len(f.confirmed))
			if force {
				title = fmt.Sprintf("强制结束 %d 个后台环境？", len(f.confirmed))
			}
			n.Text(c, title).FontSize(15).FixedLineHeight(24)
			n.Text(c, name).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			n.Column(c).FillWidth().MaxHeight(200).Children(func() {
				n.Scroll(c).FillWidth().Children(func() {
					n.Column(c).FillWidth().Gap(6).Children(func() {
						for _, target := range f.confirmed {
							title := target.SessionID
							if force && client.Operation != nil {
								for _, r := range client.Operation.Targets {
									if r.Target.SessionID == target.SessionID {
										title = r.SessionName
									}
								}
							} else if client.Diagnosis != nil {
								for _, b := range client.Diagnosis.Blockers {
									if b.SessionID == target.SessionID {
										title = b.SessionName
									}
								}
							}
							n.Text(c, title).FontSize(13).FixedLineHeight(20)
						}
					})
				})
			})
			if force {
				n.Text(c, "这些进程将被立即终止").FontSize(13).FixedLineHeight(20)
			} else {
				n.Text(c, "Agent 和工具后台进程将结束。内存变量会丢失，后台任务会停止。").FontSize(12).FixedLineHeight(20)
			}
			n.Text(c, "尚未写入的内存状态可能丢失").FontSize(12).FixedLineHeight(20)
			n.Text(c, "已写入的文件不回滚；历史、草稿和待发消息保留。队列暂停，设备空闲后才会更新。").FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
			n.Row(c).FillWidth().Justify(n.End).Gap(8).Children(func() {
				cancelLabel := "取消正常结束"
				if force {
					cancelLabel = "取消强制结束"
				}
				if configurationAction(c, "取消").Label(cancelLabel).Clicked() {
					open = false
				}
				label := "确认正常结束"
				if force {
					label = "确认强制结束"
				}
				if maintenancePrimary(c, label).Disabled(client.Busy || !state.Connected || v.Controller == nil).Clicked() {
					job, err := v.Model.ApplyWorkerMaintenance(key, f.confirmed, force)
					v.maintenanceJob(key, job, err)
					open = false
				}
			})
		})
	})
	if !open {
		f.confirm = ""
		f.confirmed = nil
	}
}
func (v *View) exportWorkerMaintenance(key string, client *app.WorkerMaintenanceClient) {
	if v.Controller == nil || client.Diagnosis == nil {
		return
	}
	// Exclude raw command lines, environment, paths and opaque authorization refs.
	type record struct {
		Session, Workspace, Reason string
		Processes                  []domain.WorkerProcessSummary
	}
	report := struct {
		DeviceID, BuildID string
		Blockers          []record
	}{DeviceID: client.Diagnosis.DeviceID, BuildID: client.Diagnosis.BuildID}
	for _, b := range client.Diagnosis.Blockers {
		report.Blockers = append(report.Blockers, record{b.SessionName, b.WorkspaceName, b.Reason, b.Processes})
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return
	}
	parent := v.nativeWindow
	v.Controller.AttachmentTask(func() app.Update {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: parent, Title: "导出脱敏诊断", DefaultPath: "shioji-worker-diagnostic.json", CreateDirectories: true})
		if err == nil && strings.TrimSpace(path) != "" {
			err = os.WriteFile(path, data, 0600)
		}
		return func(*app.Model) {
			if err != nil {
				v.maintenanceUIFor(key).error = err.Error()
			}
		}
	})
}

func maintenancePrimary(c *n.Context, label string) n.Element {
	return configurationAction(c, label).Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
}
