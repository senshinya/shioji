package transport

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestEnsureLocalWorkerRecognizesLegacyOwnerWithoutReplacingIt(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "shioji-legacy-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			codec := protocol.NewCodec(conn, conn)
			var hello protocol.Frame
			if codec.Read(&hello) == nil && hello.Major == 1 {
				codec.Write(protocol.Frame{Type: "welcome", Major: 1, Minor: 3, DeviceID: "existing-device", Epoch: "epoch", Nonce: hello.Nonce})
			}
			conn.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = EnsureLocalWorker(ctx, dir, "/not-an-installed-worker", "")
	var legacy *protocol.LegacyWorkerError
	if !errors.As(err, &legacy) || legacy.DeviceID != "existing-device" {
		t.Fatal("lost legacy identity/diagnostic", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "worker.log")); !os.IsNotExist(err) {
		t.Fatal("attempted to launch a replacement")
	}
	if _, err = os.Stat(filepath.Join(dir, "worker.sock")); err != nil {
		t.Fatal("removed the existing endpoint", err)
	}
	<-done
}
