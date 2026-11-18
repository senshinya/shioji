package app

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"shioji/internal/protocol"
)

// A large archive can take time to commit even with indexed redaction. Keep
// its request off the command connection so navigation, Stop and steering can
// proceed. Pin the owning device and route; never replay a deletion on reconnect.
func (c *Controller) startArchiveDelete(parent context.Context, current *protocol.Client, job Job) {
	if current == nil || job.epoch != current.Epoch {
		if job.Apply != nil {
			c.postContext(parent, func(m *Model) {
				job.Apply(m, nil, &protocol.RemoteError{Code: "notSent", Message: "连接已改变，操作未发送；请重新提交"})
			})
		}
		return
	}
	device := current.DeviceID
	dial := c.dial
	if c.paths != nil {
		route := c.paths.active()
		dial = func(ctx context.Context) (net.Conn, error) { return c.paths.dialRoute(ctx, route) }
	}
	c.mu.Lock()
	epoch := c.epoch
	c.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 60*time.Second)
		defer cancel()
		var out json.RawMessage
		dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
		peer, err := protocol.Dial(dialCtx, func(_ context.Context) (net.Conn, error) { return dial(ctx) }, device)
		dialCancel()
		if err == nil {
			defer peer.Close()
			c.mu.Lock()
			stale := c.epoch != epoch
			c.mu.Unlock()
			if stale {
				err = &protocol.RemoteError{Code: "notSent", Message: "连接已改变，操作未发送；请重新提交"}
			} else {
				request := job
				request.epoch = peer.Epoch
				out, err = executeJob(ctx, peer, request)
			}
		}
		if job.Apply != nil {
			c.postContext(parent, func(m *Model) { job.Apply(m, out, err) })
		}
	}()
}
