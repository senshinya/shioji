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

func TestPausedAndSignedOutNodesDoNotStartOnRestore(t *testing.T) {
	for _, mode := range []string{"paused", "signed-out"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "identity"), []byte("existing encrypted identity"), 0600)
			if err := writeIntent(dir, nodeIntent{Mode: mode, Hostname: "my-node"}); err != nil {
				t.Fatal(err)
			}
			reads := 0
			n := New(dir, func() ([]byte, error) { reads++; return make([]byte, 32), nil })
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			updates := make(chan Snapshot, 8)
			go func() { n.Run(ctx, func(s Snapshot) { updates <- s }); close(done) }()
			select {
			case <-updates:
			case <-time.After(time.Second):
				t.Fatal("restore stalled")
			}
			cancel()
			<-done
			if reads != 0 {
				t.Fatal("paused/signed-out identity auto-started", reads)
			}
		})
	}
}
func TestCancelRetiresPendingKeysAndPreservesEnrolledIdentity(t *testing.T) {
	dir := t.TempDir()
	identity := filepath.Join(dir, "identity")
	os.WriteFile(identity, []byte("enrolled-key"), 0600)
	pending := filepath.Join(dir, "pending")
	os.MkdirAll(pending, 0700)
	os.WriteFile(filepath.Join(pending, "identity"), []byte("canceled-key"), 0600)
	n := New(dir, nil)
	if err := n.retirePending(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(identity); string(raw) != "enrolled-key" {
		t.Fatal("existing enrolled identity destroyed")
	}
	if _, err := os.Stat(filepath.Join(pending, "identity")); !os.IsNotExist(err) {
		t.Fatal("canceled node keys survive")
	}
}
func TestStatusLossClosesInboundListenerAndStreams(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "shioji-inbound-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	remote, server := net.Pipe()
	defer server.Close()
	b, err := newBridge(filepath.Join(dir, "ssh.sock"), func(context.Context) (net.Conn, error) { return remote, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	c, err := net.Dial("unix", b.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go server.Write([]byte("ok"))
	buf := make([]byte, 2)
	io.ReadFull(c, buf)
	n := New(t.TempDir(), nil)
	n.inbound = &inboundForwarder{relay: b}
	n.publish(Snapshot{State: "Offline"}, func(Snapshot) {})
	c.SetDeadline(time.Now().Add(time.Second))
	if _, err = c.Read(buf); err == nil {
		t.Fatal("inbound survives status loss")
	}
	if _, err = net.Dial("unix", b.listener.Addr().String()); err == nil {
		t.Fatal("listener survives status loss")
	}
}

func TestMissingEnrolledIdentityRequiresNewUserAction(t *testing.T) {
	dir := t.TempDir()
	writeIntent(dir, nodeIntent{Mode: "connected", Hostname: "old"})
	reads := 0
	n := New(dir, func() ([]byte, error) { reads++; return make([]byte, 32), nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	updates := make(chan Snapshot, 8)
	go func() { n.Run(ctx, func(s Snapshot) { updates <- s }); close(done) }()
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("restore stalled")
	}
	cancel()
	<-done
	if reads != 0 {
		t.Fatal("missing identity silently replaced", reads)
	}
}
