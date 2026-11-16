package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

type workerUpdateConfirmation struct {
	open                      bool
	connection, device, build string
}

func (v *View) workerUpgradeConfirmation(c *n.Context, t *n.Theme, key string, state *app.DeviceState) {
	f := v.workerUpdateConfirm
	if f == nil || f.connection != key || !f.open {
		return
	}
	valid := state.Snapshot.DeviceID == f.device && state.WorkerService != nil && state.WorkerService.Phase == app.WorkerWaiting && state.WorkerService.Available != nil && state.WorkerService.Available.BuildID == f.build
	n.DialogBase(c.Key("immediate-worker-update"), &f.open, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .2))
		width, _ := c.Size()
		panel.Width(min(float32(420), width-32)).Padding(configurationDialogInset).Gap(12).Radius(10).Background(raised(t)).Border(1, t.Border).Children(func() {
			n.Text(c, "立即更新执行服务？").FontSize(15).FixedLineHeight(24)
			n.Text(c, "将停止该设备正在运行的任务。会话历史、草稿和暂停的队列会保留。").FillWidth().FontSize(13).FixedLineHeight(20)
			n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
				n.Spacer(c).Grow(1)
				if configurationAction(c, "取消").Clicked() {
					f.open = false
				}
				if configurationAction(c, "立即更新").Label("确认立即更新").Background(t.Border).Disabled(!valid || v.Controller == nil).Clicked() {
					f.open = false
					v.do(app.Job{ConnectionID: key, Method: "$worker.update.immediate"})
				}
			})
		})
	})
}
