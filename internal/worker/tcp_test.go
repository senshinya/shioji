package worker

import (
	"bytes"
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/portforward"
	"shioji/internal/protocol"
)

func TestAuthenticatedTCPStreamCarriesLargeResponseAndHalfClose(t *testing.T) {
	s, _ := memoryWorker(t)
	go s.Serve()
	remote, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	payload := bytes.Repeat([]byte{0, 255, 1, 3}, 256*1024)
	go func() {
		conn, err := remote.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		body, _ := io.ReadAll(conn)
		conn.Write(body)
	}()
	dial := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(s.Directory(), "worker.sock"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	stream, err := protocol.OpenTCP(ctx, dial, s.store.DeviceID(), portforward.Target{Host: "127.0.0.1", Port: remote.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	go func() { stream.Write(payload); stream.(interface{ CloseWrite() error }).CloseWrite() }()
	got, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("truncated stream: %d %v", len(got), err)
	}
	if conn, err := protocol.OpenTCP(ctx, dial, "wrong-device", portforward.Target{Host: "127.0.0.1", Port: 80}); err == nil {
		conn.Close()
		t.Fatal("wrong device accepted")
	}
	if conn, err := protocol.OpenTCP(ctx, dial, s.store.DeviceID(), portforward.Target{Host: "http://localhost", Port: 80}); err == nil {
		conn.Close()
		t.Fatal("invalid target accepted")
	}
}
