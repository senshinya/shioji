package worker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"testing"
)

func TestEnsureReusesExistingWorkerWithoutStartingExecutable(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sensure-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		protocol.Welcome(c, "stable-device")
	}()
	info, err := transport.EnsureLocalWorker(context.Background(), dir, "/does/not/exist", "")
	if err != nil || info.DeviceID != "stable-device" {
		t.Fatal("existing worker was not reused", info, err)
	}
}
