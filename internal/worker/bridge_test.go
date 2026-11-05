package worker

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBridgePreservesBinaryAndHalfClose(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sbridge-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	payload := []byte{0, 1, 2, 255, 0, 10, 13, 42}
	server := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			server <- e
			return
		}
		defer c.Close()
		data, e := io.ReadAll(c)
		if e == nil {
			_, e = c.Write(append(data, 99))
		}
		server <- e
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := Bridge(ctx, dir, bytes.NewReader(payload), &output); err != nil {
		t.Fatal(err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), append(payload, 99)) {
		t.Fatal("bridge corrupts bytes or drops trailing response", output.Bytes())
	}
}
