package ui

import (
	"fmt"
	"shioji/internal/app"

	n "github.com/egoist/mygo/ui"
)

type workerUpdateConfirmation struct {
	open, manage, migrate                       bool
	connection, device, build, operation, epoch string
	source                                      string
}

func (v *View) requestWorkerUpdate(key string, state *app.DeviceState) {
	if !state.Connected && key != "local" && state.WorkerService != nil {
		v.do(app.Job{ConnectionID: key, Method: "$worker.repair", Input: state.WorkerService.OperationID})
		return
	}
	if service := state.WorkerService; state.Connected && service.RequiresSchemaMigration() {
		v.beginWorkerUpdateConfirmation(key, state, false)
		v.workerUpdateConfirm.migrate = true
		v.workerUpdateConfirm.source = service.Build.BuildID
		return
	}
	v.do(app.Job{ConnectionID: key, Method: "$worker.update"})
}

func (v *View) beginWorkerUpdateConfirmation(key string, state *app.DeviceState, manage bool) {
	service := state.WorkerService
	if service == nil || service.Available == nil {
		return
	}
	v.workerUpdateConfirm = &workerUpdateConfirmation{open: true, manage: manage, connection: key, device: state.Snapshot.DeviceID, build: service.Available.BuildID, operation: service.OperationID, epoch: state.ConnectionEpoch}
}

func (v *View) workerUpgradeConfirmation(c *n.Context, t *n.Theme, key string, state *app.DeviceState) {
	f := v.workerUpdateConfirm
	if f == nil || f.connection != key || !f.open {
		return
	}
	service := state.WorkerService
	valid := state.Snapshot.DeviceID == f.device && state.ConnectionEpoch == f.epoch && service != nil &&
		service.OperationID == f.operation && service.Available != nil && service.Available.BuildID == f.build
	if valid && f.migrate {
		valid = state.Connected && service.RequiresSchemaMigration() && service.Build.BuildID == f.source &&
			(service.Phase == app.WorkerAvailable || service.Phase == app.WorkerUpdateFailed)
	} else if valid {
		valid = service.Phase == app.WorkerWaiting
	}
	if !valid {
		f.open = false
		return
	}

	compactDialog(c.Key("immediate-worker-update"), t, &f.open, 420, func(panel n.Element) {
		panel.Children(func() {
			title := "立即更新执行服务？"
			message := "将停止该设备正在运行的任务。会话历史、草稿和暂停的队列会保留。"
			if f.manage {
				title = "正在等待 " + v.Model.DeviceName(f.device) + " 空闲"
				message = "当前任务继续运行。选择“停止任务并更新”将停止这台设备上正在运行的任务，并暂停待发队列。会话历史、草稿和文件保留；更新后不会自动续答。"
			}
			if f.migrate {
				title = "备份并迁移设备数据库？"
				message = fmt.Sprintf("设备空闲后，Shioji 会保留原数据库，并将副本从 schema %d 迁移到 %d。完成核对后，执行服务会自动重新连接。", service.Build.Schema, service.Available.Schema)
			}
			compactDialogContent(c, title, func() {
				n.Text(c, message).FillWidth().FontSize(13).FixedLineHeight(20)
			}, func() {
				if f.manage && compactDialogLeadingAction(c, "取消更新").Label("取消执行服务更新").Disabled(!valid || v.Controller == nil).Clicked() {
					f.open = false
					v.do(app.Job{ConnectionID: key, Method: "$worker.update.cancel"})
				}
				n.Spacer(c).Grow(1)
				cancel, confirm := "取消", "立即更新"
				if f.manage {
					cancel, confirm = "继续等待", "停止任务并更新"
				}
				label, method := "确认立即更新", "$worker.update.immediate"
				if f.migrate {
					confirm, label, method = "备份并迁移", "确认备份并迁移", "$worker.update.migrate"
				}
				if configurationAction(c, cancel).Clicked() {
					f.open = false
				}
				if configurationAction(c, confirm).Label(label).Background(t.Border).Disabled(!valid || v.Controller == nil).Clicked() {
					f.open = false
					job := app.Job{ConnectionID: key, Method: method}
					if f.migrate {
						job.Input = app.WorkerSchemaConfirmation{SourceBuild: f.source, TargetBuild: f.build}
					}
					v.do(job)
				}
			})
		})
	})
}
