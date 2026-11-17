package supervisor

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"time"
)

type backgroundOperation struct {
	id, instance string
	result       domain.BackgroundService
	err          error
	done         bool
	stopping     bool
}

// Caller holds h.mu. A completed unregister still means stopped; only a
// confirmed undo of this exact child's preparation permits ordinary recovery.
func (h *host) backgroundStopping() bool {
	return h.background != nil && h.background.stopping
}

// The independent host owns the native action. It must not be joined by the
// worker's Close or by Run's shutdown: unregister may wait for both to exit.
func (h *host) backgroundControl(req Request) (domain.BackgroundService, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	refuse := func(message string) (domain.BackgroundService, func(), error) {
		return domain.BackgroundService{NotStarted: true}, nil, &protocol.RemoteError{Code: "notSent", Message: message}
	}
	if req.DeviceID == "" || req.DeviceID != h.device || !safeID(req.ID) {
		return refuse("后台操作或设备身份未确认")
	}
	if op := h.background; op != nil && op.id == req.ID {
		if req.Action == "background-stop" && req.Instance != op.instance {
			return refuse("后台操作不属于当前执行服务")
		}
		return op.result, nil, op.err
	}
	if req.Action == "background-status" {
		return domain.BackgroundService{}, nil, errors.New("后台操作回执暂不可用，请核对系统注册状态")
	}
	c := h.child
	if h.ctx.Err() != nil || h.blocked || h.updating || h.updateID != "" || !h.readyDone || c == nil || req.Instance != c.instance || h.options.DisableBackground == nil || h.backgroundStopping() || h.background != nil && !h.background.done {
		return refuse("执行服务尚未就绪或正在切换")
	}
	op := &backgroundOperation{id: req.ID, instance: c.instance, result: domain.BackgroundService{Format: 1, State: "disabling"}, stopping: true}
	h.background = op
	h.signalWake()
	return op.result, func() { h.stopBackground(c, op) }, nil
}

func (h *host) stopBackground(c *child, op *backgroundOperation) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var ready domain.WorkerUpdateResult
	check, end := context.WithTimeout(ctx, 5*time.Second)
	err := h.workerCall(check, c, "worker.local-background-ready", Control{Instance: c.instance, OperationID: op.id}, &ready)
	end()
	result := domain.BackgroundService{Format: 1, State: "unavailable", NotStarted: true}
	if err == nil && !ready.Ready {
		err = errors.New("执行服务未接纳后台关闭操作")
	}
	if err == nil {
		result, err = h.options.DisableBackground(ctx)
	}
	if result.NotStarted || err == nil && result.State != "notRegistered" && result.State != "notInstalled" {
		// Only this exact generation's unstarted action may reopen admission.
		undo, end := context.WithTimeout(ctx, 5*time.Second)
		cancelErr := h.workerCall(undo, c, "worker.local-background-cancel", Control{Instance: c.instance, OperationID: op.id}, nil)
		end()
		err = errors.Join(err, cancelErr)
		if cancelErr == nil {
			h.mu.Lock()
			op.stopping = false
			h.mu.Unlock()
			h.signalWake()
		}
	}
	h.mu.Lock()
	op.result, op.err = result, err
	op.done = err == nil || result.NotStarted
	h.mu.Unlock()
}
