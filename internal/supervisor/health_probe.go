package supervisor

import (
	"context"
	"time"
)

// Lost probes change observability, never authorize killing a live owner.
func (h *host) monitor() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var observed *child
	misses := 0
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
		}
		h.mu.Lock()
		c := h.child
		enabled := c != nil && h.readyDone && !h.updating && !h.blocked
		device := h.device
		h.mu.Unlock()
		if !enabled {
			observed = nil
			misses = 0
			continue
		}
		if observed != c {
			observed = c
			misses = 0
		}
		ctx, cancel := context.WithTimeout(h.ctx, 800*time.Millisecond)
		var ready Ready
		err := h.workerCall(ctx, c, "worker.local-ready", Control{Instance: c.instance}, &ready)
		cancel()
		known := err == nil && ready.PID == c.cmd.Process.Pid && ready.Instance == c.instance && ready.DeviceID == device && ready.Build.BuildID == c.build.BuildID
		if known {
			misses = 0
		} else {
			misses++
		}
		h.mu.Lock()
		if h.ctx.Err() != nil {
			h.mu.Unlock()
			return
		}
		if h.child != c || h.updating || h.blocked || !h.readyDone {
			h.mu.Unlock()
			continue
		}
		phase, message := h.health.Phase, h.health.Error
		if !known && misses >= 3 {
			phase, message = "reconnecting", "执行服务状态暂不可用，正在重新连接"
		}
		if known && phase == "reconnecting" {
			phase, message = "ready", ""
		}
		if phase != h.health.Phase || message != h.health.Error {
			h.health.Phase, h.health.Error = phase, message
			err = h.saveHealth()
		} else {
			err = nil
		}
		h.mu.Unlock()
		if err != nil {
			_ = h.blockHealth(err)
		}
	}
}
