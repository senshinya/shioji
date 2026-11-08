package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"time"
)

func (v *View) workerUpdateDetails(c *n.Context, t *n.Theme, key string, state *app.DeviceState, meta func(string, string)) {
	service := state.WorkerService
	visible := service != nil
	frame := n.Column(c.Key("worker-update:"+key)).FillWidth().Clip().Margin(-8, 0, 0, 0).Label("执行服务版本详情")
	retained := n.Local(frame, "service", func() *app.WorkerService { return service })
	if visible {
		*retained = service
	}
	animatedContentHeight(c, frame, visible, 180*time.Millisecond, func() float32 {
		service := *retained
		if service == nil {
			return 0
		}
		n.Column(c).FillWidth().Gap(8).Padding(8, 0, 0, 0).Children(func() {
			label := "构建版本"
			if !state.Connected {
				label = "上次构建版本"
			}
			meta(label, app.WorkerBuildLabel(service.Build))
			if key == "local" && service.Available != nil && service.Phase != app.WorkerCurrent && service.Phase != app.WorkerNewer {
				meta("可更新至", app.WorkerBuildLabel(service.Available))
			}
			if service.Message != "" {
				n.Text(c.Key("message"), service.Message).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			}
			if key == "local" && service.Available != nil && service.Phase != app.WorkerLegacy && service.Phase != app.WorkerCurrent && service.Phase != app.WorkerNewer {
				label := "更新执行服务"
				busy := service.Phase == app.WorkerUpdating
				if busy {
					label = "正在更新"
				} else if service.Phase == app.WorkerWaiting || service.Phase == app.WorkerUpdateFailed {
					label = "重试更新"
				}
				button := ghost(c.Key("update"), label).Height(28).PaddingX(8).FontSize(12).AlignSelf(n.Start).Disabled(v.Controller == nil || busy || !state.Connected && service.Phase != app.WorkerUpdateFailed).Label(label)
				if button.Clicked() {
					v.do(app.Job{ConnectionID: key, ConnectionEpoch: state.ConnectionEpoch, Method: "$worker.update"})
				}
			}
		})
		return 0
	})
}
