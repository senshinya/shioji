package ui

import (
	"shioji/internal/app"

	n "github.com/egoist/mygo/ui"
)

type workerUpdateConfirmation struct {
	open, manage                                bool
	connection, device, build, operation, epoch string
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
	valid := state.Snapshot.DeviceID == f.device && state.ConnectionEpoch == f.epoch && state.WorkerService != nil && state.WorkerService.Phase == app.WorkerWaiting && state.WorkerService.OperationID == f.operation && state.WorkerService.Available != nil && state.WorkerService.Available.BuildID == f.build
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
				if configurationAction(c, cancel).Clicked() {
					f.open = false
				}
				if configurationAction(c, confirm).Label("确认立即更新").Background(t.Border).Disabled(!valid || v.Controller == nil).Clicked() {
					f.open = false
					v.do(app.Job{ConnectionID: key, Method: "$worker.update.immediate"})
				}
			})
		})
	})
}
