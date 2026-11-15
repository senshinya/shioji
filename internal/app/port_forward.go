package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"

	"shioji/internal/portforward"
	"shioji/internal/protocol"
)

// ForwardInput is app-local. Browser URLs and callbacks never enter worker RPC,
// logs, client persistence, or the harness contract.
type ForwardInput struct {
	Request portforward.Request
	ID, URL string
	OpenURL func(string) error
}

func (c *Controller) initPortForwards(ctx context.Context) {
	c.mu.Lock()
	c.forwardsStarted = true
	c.mu.Unlock()
	c.forwards = portforward.New(ctx, func(items []portforward.Mapping) {
		c.mu.Lock()
		c.forwardSnapshot = items
		invalidate := c.invalidate
		c.mu.Unlock()
		if invalidate != nil {
			invalidate()
		}
	})
}

// A single replaceable snapshot never waits for a hidden window's update queue.
func (c *Controller) ForwardSnapshot() ([]portforward.Mapping, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.forwardSnapshot), c.forwardsStarted
}
func (c *Controller) runForwardJob(ctx context.Context, job Job) {
	if c.forwards == nil {
		c.initPortForwards(ctx)
	}
	input, ok := job.Input.(ForwardInput)
	var dial portforward.Dial
	var setupErr error
	if !ok {
		setupErr = errors.New("端口转发操作无效")
	}
	if job.Method == "$forward.create" && setupErr == nil {
		source := &deviceSource{ctx: ctx, controller: c}
		if c.fleet != nil {
			source = c.fleet.sources[job.ConnectionID]
		}
		if source == nil || source.controller == nil || input.Request.DeviceID == "" || source.controller.ExpectedDeviceID != input.Request.DeviceID {
			setupErr = errors.New("设备身份或连接已改变，请重新创建转发")
		} else {
			input.Request.ConnectionID = job.ConnectionID
			if input.URL != "" {
				var target portforward.Target
				target, setupErr = portforward.URLTarget(input.URL)
				if setupErr == nil {
					input.Request.Target = target
					input.Request.LocalPort = target.Port
					input.Request.AutoPort = true
				}
			}
			owner, life, device := source.controller, source.ctx, input.Request.DeviceID
			dial = func(call context.Context, target portforward.Target) (net.Conn, error) {
				// SSH's process belongs to the source lifetime. The opening timeout only
				// bounds handshake/dial; cancelling that timeout must not kill a live stream.
				return protocol.OpenTCP(call, func(context.Context) (net.Conn, error) {
					if life.Err() != nil {
						return nil, life.Err()
					}
					if owner.paths != nil {
						return owner.paths.dialStream(life)
					}
					if owner.Dial != nil {
						return owner.Dial(life)
					}
					return nil, errors.New("设备转发连接尚未就绪")
				}, device, target)
			}
		}
	}
	callCtx := ctx
	if job.Context != nil {
		callCtx = job.Context
	}
	if job.Method == "$forward.close" && setupErr == nil {
		c.forwards.CancelID(input.ID)
	}
	stopCancellation := func() bool { return true }
	if job.Method == "$forward.create" && input.Request.ID != "" && job.Context != nil {
		stopCancellation = context.AfterFunc(job.Context, func() { c.forwards.CloseID(input.Request.ID) })
	}
	go func() {
		err := setupErr
		var item portforward.Mapping
		if err == nil {
			switch job.Method {
			case "$forward.create":
				item, err = c.forwards.Create(callCtx, input.Request, dial)
				if err == nil && item.State != "available" {
					err = c.forwards.Retry(callCtx, item.ID)
				}
				if err == nil && callCtx.Err() != nil {
					err = callCtx.Err()
				}
				if err == nil && input.URL != "" && input.OpenURL != nil {
					err = c.forwards.Open(item.ID, input.URL, input.OpenURL)
				}
			case "$forward.close":
				err = c.forwards.CloseID(input.ID)
			case "$forward.retry":
				err = c.forwards.Retry(callCtx, input.ID)
			case "$forward.open":
				if input.OpenURL == nil {
					err = errors.New("浏览器入口不可用")
				} else {
					err = c.forwards.Open(input.ID, "", input.OpenURL)
				}
			default:
				err = errors.New("未知端口转发操作")
			}
		}
		body, _ := json.Marshal(item)
		if job.Apply != nil {
			c.postContext(ctx, func(m *Model) {
				stopCancellation()
				if err == nil && callCtx.Err() != nil {
					err = callCtx.Err()
				}
				job.Apply(m, body, err)
			})
		} else if err != nil {
			stopCancellation()
			c.postContext(ctx, func(m *Model) { m.Error = err.Error() })
		} else {
			stopCancellation()
		}
	}()
}
func isForwardJob(method string) bool { return strings.HasPrefix(method, "$forward.") }
