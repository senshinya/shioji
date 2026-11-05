package worker

import (
	"context"
	"io"
	"net"
	"path/filepath"
)

// Bridge connects an authenticated SSH user's stdio to that user's private
// worker. It never starts or owns a harness, and stdin EOF preserves the reply.
func Bridge(ctx context.Context, directory string, input io.Reader, output io.Writer) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "worker.sock"))
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	go func() { io.Copy(conn, input); conn.(*net.UnixConn).CloseWrite() }()
	_, err = io.Copy(output, conn)
	return err
}
