package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

func (a *mobileApp) workerBackgroundControls(c *ui.Context, parent *mygo.Window, req *mobileRequest, upgrading bool) {
	if a.preview || a.endpoint.DeviceID == "" {
		return
	}
	var state domain.BackgroundService
	if service := a.model.WorkerService; service != nil && service.Background != nil {
		state = *service.Background
	}
	const title = "登录后启动服务"
	ready := a.model.Connected && a.model.ConnectionEpoch != "" && a.model.Snapshot.DeviceID == a.endpoint.DeviceID
	busy := req.busy || state.State == "enabling" || state.State == "disabling"
	submit := func(method string) {
		a.workerUpgradeJob(parent, req, method, nil)
	}
	mobileGroupHeading(c, "后台服务")
	switch state.State {
	case "enabled", "requiresApproval", "notRegistered", "notInstalled", "enabling", "disabling":
		enabled := state.State == "enabled" || state.State == "requiresApproval" || state.State == "disabling"
		formRow(c.Key("background-toggle"), title, func() {
			mobileSwitch(c, title, &enabled, func() {
				method := "$background.disable"
				if enabled {
					method = "$background.enable"
				}
				submit(method)
			}).Disabled(!ready || busy || upgrading)
		})
	default:
		status := "待核对"
		if ready && state.State == "" {
			status = "正在检测…"
		}
		if state.State == "unavailable" || state.State == "unsupported" {
			status = "不可用"
		}
		mobileRow(c.Key("background-status"), title, "", status, nil, nil)
	}
	message := req.err
	if message == "" {
		message = state.Message
	}
	if message == "" && busy {
		message = "正在核对后台服务…"
	}
	if message != "" {
		label(c, mobileclient.RedactOperationDiagnostics(message), captionTextSize, palette(c).secondary).
			Role(ui.RoleStatus).Padding(6, 0)
	}
	if ready && (req.err != "" || state.State == "failed" || state.State == "notFound" || state.State == "requiresApproval") {
		action(c.Key("background-refresh"), "重新检测后台服务", func() { submit("$background.status") }).
			AlignSelf(ui.Start).PaddingX(0).Disabled(busy)
	}
}
