package tailnet

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSSHBridgeIsPrivateAndClosesActiveStreams(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "shioji-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "ssh.sock")
	remote, server := net.Pipe()
	defer server.Close()
	b, err := newBridge(path, func(context.Context) (net.Conn, error) { return remote, nil })
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go server.Write([]byte("SSH-2.0-test\r\n"))
	buf := make([]byte, 14)
	if _, err = io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "SSH-2.0-test\r\n" {
		t.Fatal(string(buf))
	}
	b.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = c.Read(buf); err == nil {
		t.Fatal("stream survives bridge close")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("socket survives close", err)
	}
}

func TestDroppingBridgeRejectsADialThatFinishesAfterDisconnect(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "shioji-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	started, finish := make(chan struct{}), make(chan struct{})
	remote, server := net.Pipe()
	defer server.Close()
	b, err := newBridge(filepath.Join(dir, "ssh.sock"), func(context.Context) (net.Conn, error) { close(started); <-finish; return remote, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	c, err := net.Dial("unix", b.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-started
	b.Drop()
	close(finish)
	server.SetDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err = server.Write([]byte("late")); err == nil {
		t.Fatal("late SSH stream survived disconnect")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("late dial was not closed")
	}
}
