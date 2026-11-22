package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func (a *mobileApp) workerUpgradeView(c *ui.Context, w *mygo.Window, scope string, service *app.WorkerService, req, background *mobileRequest) {
	state, detail := service.Label(), service.Message
	var source, target string
	if service.Build != nil {
		source = service.Build.BuildID
	}
	if service.Available != nil {
		target = service.Available.BuildID
	}
	switch service.Phase {
	case app.WorkerAvailable:
		state, detail = "可更新", "升级包已随 App 提供。当前任务结束后切换执行服务。"
	case app.WorkerWaiting:
		state = "等待空闲"
	case app.WorkerCurrent:
		state = "执行服务已就绪"
		if service.OperationPhase == "applied" && a.model.Connected {
			state = "升级已完成"
			detail = "已重新连接，同一设备上的工作区与会话已保留。"
		}
	case app.WorkerUpdateFailed:
		if service.OperationPhase == "failed" && detail == "context canceled" {
			state, detail = "升级已取消", ""
		}
	}
	if !a.model.Connected && (service.Phase == app.WorkerCurrent || service.Phase == app.WorkerAvailable || service.Phase == app.WorkerNewer) {
		state, detail = "尚未连接执行服务", "恢复连接后可核对版本及升级状态。"
	}
	submit := func(method string, input any) { a.workerUpgradeJob(w, req, method, input) }
	confirmation := app.AuthenticatedWorkerConfirmation{SourceBuild: source, TargetBuild: target, OperationID: service.OperationID}
	confirm := func(migration bool) {
		message := "停止任务并升级？"
		explanation := "这台设备上运行中的任务将停止。会话历史、草稿和暂停的待发队列会保留。"
		button, method := "停止任务并升级", "$worker.update.immediate"
		if migration {
			message, button, method = "备份并迁移设备数据库？", "备份并迁移", "$worker.update.migrate"
			explanation = fmt.Sprintf("设备空闲后会保留原数据库，并将副本从 schema %d 迁移到 %d。", service.Build.Schema, service.Available.Schema)
		}
		epoch := a.model.ConnectionEpoch
		go func() {
			options := mygo.MessageOptions{Parent: w, Type: mygo.MessageWarning, Message: message, Detail: explanation, Buttons: []string{"取消", button}}
			if !migration {
				options.DestructiveButtons = []int{1}
			}
			result, err := mygo.Dialog.Message(options)
			if err == nil && result.Button == 1 {
				mygo.RunOnMain(func() {
					if a.model.ConnectionEpoch == epoch {
						submit(method, confirmation)
					}
				})
			}
		}()
	}
	canRetry := service.Phase == app.WorkerUpdateFailed && service.Build != nil && service.Available != nil &&
		(service.OperationPhase == "blocked" || source != target && service.Build.SafeUpdate && service.Build.BuiltAt <= service.Available.BuiltAt)
	canImmediate := a.model.Connected && !service.RequiresSchemaMigration() && source != "" && target != ""
	mobilePageWithFooter(c, func() {
		mobilePageContext(c, a.deviceName(), scope)
		remoteStatus(c, serverSymbol, state, detail)
		if source != "" {
			mobileRow(c, "当前构建", "", app.WorkerBuildLabel(service.Build), nil, nil)
		}
		if target != "" && source != target {
			mobileRow(c, "候选构建", "", app.WorkerBuildLabel(service.Available), nil, nil)
		}
		a.workerBackgroundControls(c, w, background, req.busy || service.Phase == app.WorkerPreparing ||
			service.Phase == app.WorkerUpdating || service.Phase == app.WorkerWaiting)
		req.status(c)
	}, func() {
		check := func() { submit("$worker.check", nil) }
		ui.Column(c).Gap(6).Children(func() {
			switch {
			case service.Phase == app.WorkerWaiting:
				primaryAction(c, "检查升级状态", check).Disabled(req.busy)
				ui.Row(c).Gap(8).Children(func() {
					secondaryAction(c, "取消升级", func() { submit("$worker.update.cancel", service.OperationID) }).Grow(1).Basis(0).Disabled(req.busy)
					if canImmediate {
						secondaryAction(c, "停止任务并升级", func() { confirm(false) }).Grow(1).Basis(0).Disabled(req.busy)
					}
				})
			case service.Phase == app.WorkerPreparing:
				primaryAction(c, "正在准备升级", nil).Disabled(true)
			case service.Phase == app.WorkerUpdating || !a.model.Connected:
				primaryAction(c, "检查升级状态", check).Disabled(req.busy)
			case service.Phase == app.WorkerAvailable || canRetry:
				primaryAction(c, "准备并升级", func() {
					if service.RequiresSchemaMigration() {
						confirm(true)
					} else {
						submit("$worker.update", confirmation)
					}
				}).Disabled(req.busy)
			default:
				primaryAction(c, "配置远程 Agent", func() { a.openRemoteAgents(w) })
			}
		})
	})
}

func (a *mobileApp) workerUpgradeJob(w *mygo.Window, req *mobileRequest, method string, input any) {
	if req.busy || !a.pageCurrent(w) {
		return
	}
	if a.preview {
		a.previewWorkerUpgrade(method)
		w.Invalidate()
		return
	}
	controller := a.controller
	if controller == nil {
		req.err = "请先连接执行设备"
		w.Invalidate()
		return
	}
	req.busy, req.err = true, ""
	if !controller.Do(app.Job{Method: method, Input: input, ConnectionEpoch: a.model.ConnectionEpoch,
		Apply: func(_ *app.Model, _ json.RawMessage, err error) {
			req.busy = false
			if !a.pageCurrent(w) || a.controller != controller {
				return
			}
			if err != nil {
				req.err = err.Error()
			}
			w.Invalidate()
		},
	}) {
		req.busy, req.err = false, "请求繁忙，请稍后重试"
	}
	w.Invalidate()
}

func (a *mobileApp) seedWorkerUpgrade() {
	a.model.Connected = true
	a.model.WorkerService = &app.WorkerService{
		Build:     &domain.WorkerBuild{BuildID: strings.Repeat("a", 64), BuiltAt: time.Date(2026, 11, 19, 7, 10, 0, 0, time.Local).Unix(), Schema: 60, SafeUpdate: true, Lifecycle: 1},
		Available: &domain.WorkerBuild{BuildID: strings.Repeat("b", 64), BuiltAt: time.Date(2026, 11, 19, 11, 25, 0, 0, time.Local).Unix(), Schema: 60, SafeUpdate: true, Lifecycle: 1},
		Phase:     app.WorkerAvailable,
	}
}

func (a *mobileApp) previewWorkerUpgrade(method string) {
	state := *a.model.WorkerService
	switch method {
	case "$worker.update", "$worker.update.migrate":
		state.Phase, state.OperationID, state.OperationPhase = app.WorkerWaiting, "preview-update", "waiting"
		state.Message = "等待当前任务结束，会话和待发队列已保留。"
	case "$worker.update.cancel":
		state.Phase, state.OperationPhase, state.Message = app.WorkerUpdateFailed, "failed", "context canceled"
	case "$worker.check", "$worker.update.immediate":
		if state.Phase == app.WorkerWaiting {
			state.Build, state.Phase, state.OperationPhase, state.Message = state.Available, app.WorkerCurrent, "applied", ""
		}
	}
	a.model.WorkerService = &state
}
