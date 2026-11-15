package portforward

import (
	"context"
	"errors"
	"io"
	"net"
)

// Relay preserves TCP half-close and bounds each direction to a 32 KiB buffer.
func Relay(ctx context.Context, a, b net.Conn) error {
	stop := context.AfterFunc(ctx, func() { a.Close(); b.Close() })
	defer stop()
	done := make(chan error, 2)
	copyOne := func(dst, src net.Conn) {
		_, err := io.CopyBuffer(dst, src, make([]byte, 32*1024))
		if half, ok := dst.(interface{ CloseWrite() error }); ok {
			half.CloseWrite()
		} else {
			dst.Close()
		}
		if err != nil {
			a.Close()
			b.Close()
		}
		done <- err
	}
	go copyOne(a, b)
	go copyOne(b, a)
	return errors.Join(<-done, <-done)
}
