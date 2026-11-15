package ui

import (
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"time"
)

func (v *View) backgroundSwitch(c *n.Context, t *n.Theme, key string, state *app.DeviceState, setup ...bool) {
	var service *domain.BackgroundService
	if state.WorkerService != nil {
		service = state.WorkerService.Background
	}
	enabled, busy, unavailable := false, false, true
	if service != nil {
		enabled = service.State == "enabled" || service.State == "requiresApproval"
		busy = service.State == "enabling" || service.State == "disabling"
		unavailable = service.State == "unsupported" || service.State == "unavailable"
	}
	row := deviceConfigRow
	if len(setup) > 0 && setup[0] {
		row = setupRow
	}
	row(c, "登录后启动服务", func() {
		n.Row(c).FillWidth().Justify(n.End).Children(func() {
			tooltip := ""
			if unavailable && service != nil {
				tooltip = service.Message
			}
			if configurationSwitch(c, &enabled).Tooltip(tooltip).Label("登录后启动服务开关").Disabled(v.Controller == nil || !state.Connected || busy || unavailable).Changed() {
				method := "$background.disable"
				if enabled {
					method = "$background.enable"
				}
				v.do(app.Job{ConnectionID: key, Method: method})
			}
		})
	})
	message := ""
	if service != nil {
		if service.State != "unavailable" && service.State != "unsupported" {
			message = service.Message
		}
		if service.State == "enabling" {
			message = "正在启用…"
		}
		if service.State == "disabling" {
			message = "正在关闭…"
		}
	}
	frame := n.Column(c.Key("message")).FillWidth().Clip().Label("后台服务详情")
	retained := n.Local(frame, "text", func() string { return message })
	if message != "" {
		*retained = message
	}
	animatedContentHeight(c, frame, message != "", 180*time.Millisecond, func() float32 {
		n.Column(c).FillWidth().Gap(4).Transition(disclosureMotion(c, 160*time.Millisecond)).Children(func() {
			n.Text(c, *retained).FillWidth().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			if service != nil && (service.State == "requiresApproval" || service.State == "failed" || service.State == "notFound") {
				n.Row(c).Gap(6).Children(func() {
					if key == "local" && service.State == "requiresApproval" && configurationAction(c, "打开系统设置").Clicked() {
						if err := mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.LoginItems-Settings.extension"); err != nil {
							v.Model.Error = err.Error()
						}
					}
					if configurationAction(c, "重新检测").Label("重新检测后台服务").Disabled(v.Controller == nil).Clicked() {
						v.do(app.Job{ConnectionID: key, Method: "$background.status"})
					}
				})
			}
		})
		return 0
	})
}
