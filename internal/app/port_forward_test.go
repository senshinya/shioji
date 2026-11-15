package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/portforward"
	"shioji/internal/protocol"
)

// Real protocol bytes and TCP target; only the owning worker endpoint is a fixture.
func TestForwardJobsPinDeviceOpenAfterProbeAndCloseGlobally(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			c, e := target.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	dir, e := os.MkdirTemp("", "pf-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "worker.sock")
	server, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() {
		for {
			conn, err := server.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				codec, epoch, err := protocol.Welcome(conn, "remote-device")
				if err != nil {
					return
				}
				var f protocol.Frame
				if codec.Read(&f) != nil {
					return
				}
				var dest portforward.Target
				if json.Unmarshal(f.Body, &dest) != nil {
					return
				}
				remote, err := net.Dial("tcp", dest.Address())
				if err != nil {
					return
				}
				defer remote.Close()
				codec.Write(protocol.Frame{Type: "result", ID: f.ID, DeviceID: "remote-device", Epoch: epoch})
				portforward.Relay(context.Background(), conn, remote)
			}()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := NewController("")
	child := NewController("")
	child.ExpectedDeviceID = "remote-device"
	child.Dial = func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "unix", socket) }
	root.fleet = &deviceFleet{sources: map[string]*deviceSource{"remote": {ctx: ctx, controller: child, connection: DeviceConnection{DeviceID: "remote-device"}}}}
	root.initPortForwards(ctx)
	defer root.forwards.Close()
	opened := make(chan string, 1)
	completed := make(chan error, 1)
	input := ForwardInput{Request: portforward.Request{DeviceID: "remote-device", Target: portforward.Target{Host: "localhost", Port: target.Addr().(*net.TCPAddr).Port}, AutoPort: true}, URL: "http://localhost:" + portString(target) + "/a%2Fb?q=1#part", OpenURL: func(raw string) error { opened <- raw; return nil }}
	root.runForwardJob(ctx, Job{Method: "$forward.create", ConnectionID: "remote", Input: input, Apply: func(_ *Model, _ json.RawMessage, e error) { completed <- e }})
	m := NewModel()
	for {
		select {
		case u := <-root.Updates:
			u(m)
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
			goto done
		}
	}
done:
	items := root.forwards.Snapshot()
	if len(items) != 1 {
		t.Fatal(items)
	}
	want := "http://" + items[0].LocalAddress() + "/a%2Fb?q=1#part"
	if got := <-opened; got != want {
		t.Fatal(got, want)
	}
	conn, err := net.Dial("tcp", items[0].LocalAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("live"))
	buf := make([]byte, 4)
	if _, err = io.ReadFull(conn, buf); err != nil || string(buf) != "live" {
		t.Fatal(string(buf), err)
	}
	root.runForwardJob(ctx, Job{Method: "$forward.close", Input: ForwardInput{ID: items[0].ID}, Apply: func(_ *Model, _ json.RawMessage, e error) { completed <- e }})
	for {
		select {
		case u := <-root.Updates:
			u(m)
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
			if len(root.forwards.Snapshot()) != 0 {
				t.Fatal("close retained mapping")
			}
			return
		}
	}
}
func portString(l net.Listener) string { return fmt.Sprint(l.Addr().(*net.TCPAddr).Port) }

func TestForwardJobRejectsChangedDeviceBeforeDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := NewController("")
	root.fleet = &deviceFleet{sources: map[string]*deviceSource{"remote": {ctx: ctx, controller: &Controller{ExpectedDeviceID: "new-device"}}}}
	root.initPortForwards(ctx)
	defer root.forwards.Close()
	done := make(chan error, 1)
	root.runForwardJob(ctx, Job{Method: "$forward.create", ConnectionID: "remote", Input: ForwardInput{Request: portforward.Request{DeviceID: "old-device", Target: portforward.Target{Host: "localhost", Port: 3000}}}, Apply: func(_ *Model, _ json.RawMessage, e error) { done <- e }})
	m := NewModel()
	for {
		select {
		case u := <-root.Updates:
			u(m)
		case err := <-done:
			if err == nil {
				t.Fatal("changed device accepted")
			}
			if len(root.forwards.Snapshot()) != 0 {
				t.Fatal("listener created before identity check")
			}
			return
		}
	}
}

func TestForwardNetworkDoesNotWaitForUndrawnWindowUpdates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewController("")
	for len(c.Updates) < cap(c.Updates) {
		c.Updates <- func(*Model) {}
	}
	c.initPortForwards(ctx)
	defer c.forwards.Close()
	item, err := c.forwards.Create(ctx, portforward.Request{DeviceID: "device", Target: portforward.Target{Host: "localhost", Port: 3000}}, func(context.Context, portforward.Target) (net.Conn, error) {
		a, b := net.Pipe()
		b.Close()
		return a, nil
	})
	if err != nil {
		t.Fatal("forwarding waited for the window to drain state updates", err)
	}
	if item.State != "available" {
		t.Fatal(item)
	}
}
