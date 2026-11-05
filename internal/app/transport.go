package app

import (
	"context"
	"net"
	"path/filepath"
	"time"
)

func (c *Controller) dial(ctx context.Context) (net.Conn, error) {
	if c.Dial != nil {
		return c.Dial(ctx)
	}
	return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", filepath.Join(c.Directory, "worker.sock"))
}
